package client

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/dvote/vochain"
	models "go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

func TestCollectFaucet(t *testing.T) {
	app := vochain.TestBaseApplication(t)
	signer := &ethereum.SignKeys{}
	if err := signer.Generate(); err != nil {
		t.Fatal(err)
	}

	// we're going to test Client. Create it and feed it with a mock
	// http.Client{}.
	c, err := New("http://somewhere/dvote")
	c.HTTP = &mockHTTPClient{App: app}
	if err != nil {
		t.Fatal(err)
	}

	signer2 := &ethereum.SignKeys{}
	if err := signer2.Generate(); err != nil {
		t.Fatal(err)
	}

	if err := app.State.CreateAccount(signer.Address(), "ipfs://somewhere", []common.Address{}, 1000); err != nil {
		t.Fatal(err)
	}
	app.Commit()

	// generate faucet package directly
	faucetPkg, err := vochain.GenerateFaucetPackage(signer, signer2.Address(), 100)
	if err != nil {
		t.Fatal(err)
	}

	c.CollectFaucet(signer2, faucetPkg)

}

// checkTx is a test helper function that takes a marshaled transaction and
// ensures that the vochain BaseApplication finds it valid.
func checkTx(app *vochain.BaseApplication, signer *ethereum.SignKeys, stx *models.SignedTx) error {
	var cktx abcitypes.RequestCheckTx
	var detx abcitypes.RequestDeliverTx
	var cktxresp abcitypes.ResponseCheckTx
	var detxresp abcitypes.ResponseDeliverTx
	var err error

	if stx.Signature, err = signer.SignVocdoniTx(stx.Tx); err != nil {
		return err
	}
	stxBytes, err := proto.Marshal(stx)
	if err != nil {
		return err
	}

	cktx.Tx = stxBytes
	cktxresp = app.CheckTx(cktx)
	if cktxresp.Code != 0 {
		return fmt.Errorf("checkTx failed: %s", cktxresp.Data)
	}
	detx.Tx = stxBytes
	detxresp = app.DeliverTx(detx)
	if detxresp.Code != 0 {
		return fmt.Errorf("deliverTx failed: %s", detxresp.Data)
	}
	return nil
}

type mockHTTPClient struct {
	App *vochain.BaseApplication
}

func (m *mockHTTPClient) Post(url, contentType string, body io.Reader) (resp *http.Response, err error) {
	fmt.Println("inside mockHTTPClient.Post")
	checkTx(m.App)
	resp = &http.Response{
		Status:     "OK",
		StatusCode: 200,
		Body:       ioutil.NopCloser(bytes.NewBufferString("{\"ID\":\"mockHTTPClient\"}")),
	}
	return resp, nil
}

func (m *mockHTTPClient) CloseIdleConnections() {
	return
}
