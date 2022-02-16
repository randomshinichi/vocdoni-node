package test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	vocli "go.vocdoni.io/dvote/cmd/vocli/commands"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/dvote/util"
	"go.vocdoni.io/dvote/vocone"
)

var addressRegexp = regexp.MustCompile("Public address of the key:.*")
var pathRegexp = regexp.MustCompile("Path of the secret key file:.*")

func TestVocli(t *testing.T) {
	dir := t.TempDir()

	alice := ethereum.NewSignKeys()
	if err := alice.Generate(); err != nil {
		t.Fatal(err)
	}
	url, _ := setupInternalVocone(t, dir, alice)
	// url := "http://localhost:9095/dvote"
	// SETUP done, let's start testing
	stdArgs := []string{"--password=password", fmt.Sprintf("--home=%s", dir)}

	t.Run("vocli keys import", func(t *testing.T) {
		privKey := crypto.FromECDSA(&alice.Private)
		if err := ioutil.WriteFile(path.Join(dir, "alicePrivateKey"), []byte(hex.EncodeToString(privKey)), 0700); err != nil {
			t.Fatal(err)
		}

		_, stdout, _, err := executeCommandC(t, vocli.RootCmd, append([]string{"keys", "import", path.Join(dir, "/alicePrivateKey")}, stdArgs...), "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, dir) {
			t.Errorf("vocli import should report that the imported key was stored in directory %s", dir)
		}
	})

	var aliceKeyPath string
	t.Run("vocli keys list", func(t *testing.T) {
		_, stdout, _, err := executeCommandC(t, vocli.RootCmd, append([]string{"keys", "list"}, stdArgs...), "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, dir) {
			t.Errorf("vocli list should have found and shown a key in dir %s", dir)
		}
		aliceKeyPath = strings.TrimSpace(stdout)
	})

	t.Run("vocli keys changepassword aliceKeyPath", func(t *testing.T) {
		_, _, _, err := executeCommandC(t, vocli.RootCmd, []string{"keys", "changepassword", aliceKeyPath, "--password=password"}, "fdsa\n")
		if err != nil {
			t.Fatal(err)
		}
		k, err := ioutil.ReadFile(aliceKeyPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ethkeystore.DecryptKey(k, "fdsa")
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("vocli keys new (no connection to chain, should report error and not crash)", func(t *testing.T) {
		_, _, stderr, err := executeCommandC(t, vocli.RootCmd, append([]string{"keys", "new", "-u=http://nonexistent:9095"}, stdArgs...), "")
		if err == nil {
			t.Error("vocli keys new without a connection to a working chain should fail, but got no error instead")
		}
		if !strings.Contains(stderr, "no such host") {
			t.Errorf("with the URL we specified, we should get an error message 'no such host' but got this instead: %s", stderr)
		}
	})

	t.Run("vocli keys new", func(t *testing.T) {
		_, stdout, _, err := executeCommandC(t, vocli.RootCmd, append([]string{"keys", "new", fmt.Sprintf("-u=%s", url)}, stdArgs...), "")
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(stdout, fmt.Sprintf("created on chain %s", url)) {
			t.Errorf("stdout should mention that the new account was created on the chain, but instead: %s", stdout)
		}
	})
}

func executeCommandC(t *testing.T, root *cobra.Command, args []string, input string) (*cobra.Command, string, string, error) {
	t.Helper()
	// setup stdout/stderr redirection.
	var stdoutBuf = new(bytes.Buffer)
	var stderrBuf = new(bytes.Buffer)
	vocli.Stdout = stdoutBuf
	vocli.Stderr = stderrBuf

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdinWriter.Write([]byte(input))
	vocli.Stdin = stdinReader

	// cobra.Command.SetOut/SetErr() is actually useless for most purposes as it
	// only includes the usage messages printed to stdout/stderr. For full
	// stdout/stderr capture it's still best to use vocli.Stdout/Stderr. Let's
	// just mock it out anyway.
	root.SetOut(stdoutBuf)
	root.SetErr(stderrBuf)
	root.SetArgs(args)
	t.Log("vocli", args)
	c, err := root.ExecuteC()

	fmt.Printf("\nSTDOUT\n%s", stdoutBuf)
	fmt.Printf("STDERR\n%s", stderrBuf)

	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetArgs([]string{})
	return c, stdoutBuf.String(), stderrBuf.String(), err
}

func setupInternalVocone(t *testing.T, dir string, treasurer *ethereum.SignKeys) (string, int) {
	t.Helper()

	oracle := ethereum.NewSignKeys()
	if err := oracle.Generate(); err != nil {
		t.Fatal(err)
	}
	vc, err := vocone.NewVocone(dir, oracle)
	if err != nil {
		t.Fatal(err)
	}

	if err = vc.SetTreasurer(common.HexToAddress(treasurer.Address().Hex())); err != nil {
		t.Fatal(err)
	}

	if err = vc.SetBulkTxCosts(10); err != nil {
		t.Fatal(err)
	}
	vc.SetBlockTimeTarget(time.Millisecond * 500)
	port := 13000 + util.RandomInt(0, 2000)
	vc.EnableAPI("127.0.0.1", port, "/dvote")
	go vc.Start()
	t.Log("internal vocone started")
	return fmt.Sprintf("http://127.0.0.1:%v/dvote", port), port
}

func generateKeyAndReturnAddress(t *testing.T, url string, stdArgs []string) (address string, keyPath string) {
	t.Helper()
	_, stdout, _, err := executeCommandC(t, vocli.RootCmd, append([]string{"keys", "new", fmt.Sprintf("-u=%s", url)}, stdArgs...), "")
	if err != nil {
		t.Fatal(err)
	}

	return parseImportOutput(t, stdout)
}

func parseImportOutput(t *testing.T, stdout string) (address, keyPath string) {
	t.Helper()
	a := strings.TrimSpace(addressRegexp.FindString(stdout))
	a2 := strings.Split(a, " ")
	newAddr := a2[len(a2)-1]

	k := strings.TrimSpace(pathRegexp.FindString(stdout))
	k2 := strings.Split(k, " ")
	keyPath = k2[len(k2)-1]
	return newAddr, keyPath
}
