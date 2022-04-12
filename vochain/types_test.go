package vochain

import (
	"encoding/json"
	"reflect"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp"
	"go.vocdoni.io/proto/build/go/models"
)

func TestTransactionCostsAsMap(t *testing.T) {
	txCosts := TransactionCosts{
		SetProcessStatus:        100,
		SetProcessCensus:        200,
		SetProcessResults:       300,
		SetProcessQuestionIndex: 400,
		RegisterKey:             500,
		NewProcess:              600,
		SendTokens:              700,
		SetAccountInfo:          800,
		AddDelegateForAccount:   900,
		DelDelegateForAccount:   1000,
		CollectFaucet:           1100,
	}
	txCostsBytes := txCosts.AsMap()

	expected := map[models.TxType]uint64{
		models.TxType_SET_PROCESS_STATUS:         100,
		models.TxType_SET_PROCESS_CENSUS:         200,
		models.TxType_SET_PROCESS_RESULTS:        300,
		models.TxType_SET_PROCESS_QUESTION_INDEX: 400,
		models.TxType_REGISTER_VOTER_KEY:         500,
		models.TxType_NEW_PROCESS:                600,
		models.TxType_SEND_TOKENS:                700,
		models.TxType_SET_ACCOUNT_INFO:           800,
		models.TxType_ADD_DELEGATE_FOR_ACCOUNT:   900,
		models.TxType_DEL_DELEGATE_FOR_ACCOUNT:   1000,
		models.TxType_COLLECT_FAUCET:             1100,
	}
	qt.Assert(t, txCostsBytes, qt.DeepEquals, expected)
}
func TestTxCostNameToTxType(t *testing.T) {
	fields := map[string]models.TxType{
		"SetProcessStatus":        models.TxType_SET_PROCESS_STATUS,
		"SetProcessCensus":        models.TxType_SET_PROCESS_CENSUS,
		"SetProcessResults":       models.TxType_SET_PROCESS_RESULTS,
		"SetProcessQuestionIndex": models.TxType_SET_PROCESS_QUESTION_INDEX,
		"RegisterKey":             models.TxType_REGISTER_VOTER_KEY,
		"NewProcess":              models.TxType_NEW_PROCESS,
		"SendTokens":              models.TxType_SEND_TOKENS,
		"SetAccountInfo":          models.TxType_SET_ACCOUNT_INFO,
		"AddDelegateForAccount":   models.TxType_ADD_DELEGATE_FOR_ACCOUNT,
		"DelDelegateForAccount":   models.TxType_DEL_DELEGATE_FOR_ACCOUNT,
		"CollectFaucet":           models.TxType_COLLECT_FAUCET,
	}
	for k, v := range fields {
		qt.Assert(t, TxCostNameToTxType(k), qt.Equals, v)

		// check that TransactionCosts struct does have the fields that we
		// specify in this test
		_, found := reflect.TypeOf(TransactionCosts{}).FieldByName(k)
		qt.Assert(t, found, qt.IsTrue)
	}
}

// TestTransactionCostsUnmarshalJSON is useful because Tendermint serializes all
// integers as strings (for compatibility with JS, see
// https://github.com/tendermint/tendermint/issues/3898), so when unmarshaling
// back to a struct we must ensure strings are read into uint64s
func TestTransactionCostsUnmarshalJSON(t *testing.T) {
	j := []byte(`{"Tx_SetProcessStatus":"0","Tx_SetProcessCensus":"1","Tx_SetProcessResults":"2","Tx_SetProcessQuestionIndex":"3","Tx_RegisterKey":"4","Tx_NewProcess":"5","Tx_SendTokens":"6","Tx_SetAccountInfo":"7","Tx_AddDelegateForAccount":"8","Tx_DelDelegateForAccount":"9","Tx_CollectFaucet":"10"}`)
	txCosts := &TransactionCosts{}
	err := json.Unmarshal(j, txCosts)
	if err != nil {
		t.Error(err)
	}
	if txCosts.SendTokens != 6 || txCosts.CollectFaucet != 10 {
		t.Errorf("struct TransactionCosts has incorrect data after parsing JSON: %v", txCosts)
	}
}

func TestTransactionCostsMarshalJSON(t *testing.T) {
	txCosts := &TransactionCosts{
		SetProcessStatus:        0,
		SetProcessCensus:        1,
		SetProcessResults:       2,
		SetProcessQuestionIndex: 3,
		RegisterKey:             4,
		NewProcess:              5,
		SendTokens:              6,
		SetAccountInfo:          7,
		AddDelegateForAccount:   8,
		DelDelegateForAccount:   9,
		CollectFaucet:           10,
	}
	got, err := json.Marshal(txCosts)
	if err != nil {
		t.Error(err)
	}

	txCosts2 := new(TransactionCosts)
	json.Unmarshal(got, txCosts2)
	if !cmp.Equal(txCosts, txCosts2) {
		t.Errorf("TransactionCosts JSON should have become %v, but got: %v", txCosts, txCosts2)
	}
}
