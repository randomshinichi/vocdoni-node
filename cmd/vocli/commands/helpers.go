package commands

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.vocdoni.io/dvote/api"
	"go.vocdoni.io/dvote/client"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

func writeKeyFile(filename string, k []byte) error {
	// Create the keystore directory with appropriate permissions
	// in case it is not present yet.
	const dirPerm = 0700
	if err := os.MkdirAll(filepath.Dir(filename), dirPerm); err != nil {
		return err
	}

	// Here we diverge from geth. No need for atomic write? just write it out
	// already
	if err := ioutil.WriteFile(filename, k, 0600); err != nil {
		return err
	}
	return nil
}

func getDataDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return h + "/.dvote", nil
}

func getKeysDir() (string, error) {
	dataDir, err := getDataDir()
	if err != nil {
		return "", err
	}
	return path.Join(dataDir, "keys"), nil
}

func generateKeyFilename(address common.Address) (string, error) {
	keysDir, err := getKeysDir()
	if err != nil {
		return "", fmt.Errorf("couldn't get a suitable datadir (normally ~/.dvote/keys) - {}", err)
	}

	t := time.Now()
	keyFilename := fmt.Sprintf("%s-%d-%d-%d%s", address.Hex(), t.Year(), t.Month(), t.Day(), keyExt) // 0xDad23752bB8F80Bb82E6A2422A762fdeAf8dAb74-2022-1-13.vokey
	return path.Join(keysDir, keyFilename), nil
}

func signTx(tx *models.Tx, signer *ethereum.SignKeys) (*models.SignedTx, error) {
	var err error
	stx := &models.SignedTx{}
	stx.Tx, err = proto.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("could not marshal transaction: %s", err)
	}

	stx.Signature, err = signer.Sign(stx.Tx)
	if err != nil {
		return nil, fmt.Errorf("could not sign transaction: %s", err)
	}
	return stx, nil
}

func submitRawTx(stx *models.SignedTx, c *client.Client) (*api.APIresponse, error) {
	var err error
	fmt.Println("Sending Transaction")
	req := &api.APIrequest{}
	req.Method = "submitRawTx"
	req.Payload, err = proto.Marshal(stx)
	if err != nil {
		return nil, fmt.Errorf("could not marshal transaction: %v", err)
	}

	return c.Request(*req, nil)
}
