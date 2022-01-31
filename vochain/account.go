package vochain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/tendermint/tendermint/crypto"
	"github.com/vocdoni/arbo"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/dvote/types"
	"go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

// Account represents an amount of tokens, usually attached to an address.
// Account includes a Nonce which needs to be incremented by 1 on each transfer,
// an external URI link for metadata and a list of delegated addresses allowed
// to use the account on its behalf (in addition to himself).
type Account struct {
	models.Account
}

// Marshal encodes the Account and returns the serialized bytes.
func (a *Account) Marshal() ([]byte, error) {
	return proto.Marshal(a)
}

// Unmarshal decode a set of bytes.
func (a *Account) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
}

// Transfer moves amount from the origin Account to the dest Account.
func (a *Account) Transfer(dest *Account, amount uint64, cost uint64, nonce uint32) error {
	if amount == 0 {
		return fmt.Errorf("cannot transfer zero amount")
	}
	if dest == nil {
		return fmt.Errorf("destination account nil")
	}
	if a.Nonce != nonce {
		return ErrAccountNonceInvalid
	}
	a.Nonce++
	if (a.Balance + cost) < amount {
		return ErrNotEnoughBalance
	}
	if dest.Balance+amount <= dest.Balance {
		return ErrBalanceOverflow
	}
	dest.Balance += amount
	a.Balance -= amount + cost
	return nil
}

// IsDelegate checks if an address is a delegate for an account
func (a *Account) IsDelegate(addr common.Address) bool {
	for _, d := range a.DelegateAddrs {
		if bytes.Equal(addr.Bytes(), d) {
			return true
		}
	}
	return false
}

// AddDelegate adds an address to the list of delegates for an account
func (a *Account) AddDelegate(addr common.Address) error {
	if a.IsDelegate(addr) {
		return fmt.Errorf("address %s is already a delegate", addr.Hex())
	}
	a.DelegateAddrs = append(a.DelegateAddrs, addr.Bytes())
	return nil
}

// DelDelegate removes an address from the list of delegates for an account
func (a *Account) DelDelegate(addr common.Address) error {
	for i, d := range a.DelegateAddrs {
		if bytes.Equal(addr.Bytes(), d) {
			a.DelegateAddrs[i] = a.DelegateAddrs[len(a.DelegateAddrs)-1]
			a.DelegateAddrs = a.DelegateAddrs[:len(a.DelegateAddrs)-1]
			return nil
		}
	}
	return fmt.Errorf("cannot delete delegate, not found")
}

// TransferBalance transfers balance from origin address to destination address,
// and updates the state with the new values (including nonce).
// If origin address acc is not enough, ErrNotEnoughBalance is returned.
// If provided nonce does not match origin address nonce+1, ErrAccountNonceInvalid is returned.
func (v *State) TransferBalance(from, to common.Address, amount uint64, nonce uint64) error {
	var accFrom, accTo *Account
	var err error
	if accFrom, err = v.GetAccount(from, false); err != nil {
		return err
	}
	if accFrom == nil {
		return ErrAccountNotFound
	}
	if accTo, err = v.GetAccount(to, false); err != nil || accTo == nil {
		return err
	}
	if accTo == nil {
		return ErrAccountNotFound
	}
	transferCost, err := v.TxCost(models.TxType_SEND_TOKENS, false)
	if err != nil {
		return err
	}
	if err := accFrom.Transfer(accTo, amount, transferCost, uint32(nonce)); err != nil {
		return err
	}
	if err := v.SetAccount(from, accFrom); err != nil {
		return err
	}
	if err := v.SetAccount(to, accTo); err != nil {
		return err
	}
	return nil
}

// CollectFaucet transfers balance from faucet generated package address to collector address,
// and updates the state with the new values (including nonce).
func (v *State) CollectFaucet(from, to common.Address, amount uint64, nonce uint64) error {
	var accFrom, accTo *Account
	var err error
	if accFrom, err = v.GetAccount(from, false); err != nil || accFrom == nil {
		return err
	}
	if accFrom == nil {
		return ErrAccountNotFound
	}
	if accTo, err = v.GetAccount(to, false); err != nil {
		return err
	}
	if accTo == nil {
		return ErrAccountNotFound
	}
	if amount == 0 {
		return fmt.Errorf("cannot transfer zero amount")
	}
	transferCost, err := v.TxCost(models.TxType_COLLECT_FAUCET, false)
	if err != nil {
		return err
	}
	if accFrom.Balance < amount+transferCost {
		return ErrNotEnoughBalance
	}
	if accTo.Balance+amount <= accTo.Balance {
		return ErrBalanceOverflow
	}
	accTo.Balance += amount
	accFrom.Balance -= amount + transferCost
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, nonce)
	key := from.Bytes()
	key = append(key, b...)
	if err := v.Tx.DeepSet(crypto.Sha256(key), nil, FaucetNonceCfg); err != nil {
		return err
	}
	if err := v.SetAccount(from, accFrom); err != nil {
		return err
	}
	if err := v.SetAccount(to, accTo); err != nil {
		return err
	}
	return nil
}

// mintBalance increments the existing acc of address by amount
func (v *State) MintBalance(address common.Address, amount uint64) error {
	if amount == 0 {
		return fmt.Errorf("cannot mint a zero amount balance")
	}
	var acc Account
	v.Tx.Lock()
	defer v.Tx.Unlock()
	raw, err := v.Tx.DeepGet(address.Bytes(), AccountsCfg)
	if err != nil && !errors.Is(err, arbo.ErrKeyNotFound) {
		return err
	} else if err == nil {
		if err := acc.Unmarshal(raw); err != nil {
			return err
		}
	}
	if acc.Balance+amount <= acc.Balance {
		return ErrBalanceOverflow
	}
	acc.Balance += amount
	accBytes, err := acc.Marshal()
	if err != nil {
		return err
	}
	return v.Tx.DeepSet(address.Bytes(), accBytes, AccountsCfg)
}

// GetAccount retrives the Account for an address.
// Returns a nil account and no error if the account does not exist.
func (v *State) GetAccount(address common.Address, isQuery bool) (*Account, error) {
	var acc Account
	if !isQuery {
		v.Tx.RLock()
		defer v.Tx.RUnlock()
	}
	raw, err := v.mainTreeViewer(isQuery).DeepGet(address.Bytes(), AccountsCfg)
	if errors.Is(err, arbo.ErrKeyNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &acc, acc.Unmarshal(raw)
}

// VerifyAccountBalanceFromSignature extracts an account address from a signed message, and verifies if
// there is enough balance to cover an amount expense
func (v *State) VerifyAccountBalanceFromSignature(message, signature []byte, amount uint64) (bool, common.Address, error) {
	var err error
	address := common.Address{}
	address, err = ethereum.AddrFromSignature(message, signature)
	if err != nil {
		return false, address, err
	}
	acc, err := v.GetAccount(address, false)
	if err != nil {
		return false, address, fmt.Errorf("VerifyAccountBalanceFromSignature: %v", err)
	}
	if acc == nil {
		return false, address, nil
	}
	return acc.Balance >= amount, address, nil
}

func (v *State) SetAccountInfoURI(accountAddress, txSender common.Address, infoURI string) error {
	acc, err := v.GetAccount(accountAddress, false)
	if err != nil {
		return err
	}
	if acc == nil {
		return ErrAccountNotFound
	}
	SetAccountInfoCost, err := v.TxCost(models.TxType_SET_ACCOUNT_INFO, false)
	if err != nil {
		return err
	}
	if accountAddress == txSender {
		acc.InfoURI = infoURI
		acc.Balance -= SetAccountInfoCost
		acc.Nonce++
		accBytes, err := acc.Marshal()
		if err != nil {
			return err
		}
		v.Tx.Lock()
		defer v.Tx.Unlock()
		return v.Tx.DeepSet(accountAddress.Bytes(), accBytes, AccountsCfg)
	}
	sender, err := v.GetAccount(txSender, false)
	if err != nil {
		return err
	}
	if sender == nil {
		return ErrAccountNotFound
	}
	sender.Balance -= SetAccountInfoCost
	sender.Nonce++
	senderBytes, err := sender.Marshal()
	if err != nil {
		return err
	}
	acc.InfoURI = infoURI
	accBytes, err := acc.Marshal()
	if err != nil {
		return err
	}
	v.Tx.Lock()
	defer v.Tx.Unlock()
	if err := v.Tx.DeepSet(accountAddress.Bytes(), accBytes, AccountsCfg); err != nil {
		return err
	}
	return v.Tx.DeepSet(txSender.Bytes(), senderBytes, AccountsCfg)
}

func (v *State) CreateAccount(accountAddress common.Address, infoURI string, delegates []common.Address, initBalance uint64) error {
	// check valid address
	if accountAddress.String() == types.EthereumZeroAddressString {
		return fmt.Errorf("invalid address")
	}
	// check not created
	acc, err := v.GetAccount(accountAddress, false)
	if err != nil {
		return fmt.Errorf("cannot create account %s: %v", accountAddress.String(), err)
	}
	if acc != nil {
		return fmt.Errorf("account %s already exists", accountAddress.String())
	}
	// account not found, creating it
	// check valid infoURI, must be set on creation
	newAccount := &Account{}
	newAccount.InfoURI = infoURI
	if len(delegates) > 0 {
		newAccount.DelegateAddrs = make([][]byte, len(delegates))
		for i := 0; i < len(delegates); i++ {
			if delegates[i].String() != types.EthereumZeroAddressString {
				newAccount.DelegateAddrs = append(newAccount.DelegateAddrs, delegates[i].Bytes())
			}
		}
	}
	newAccount.Balance = initBalance
	accBytes, err := newAccount.Marshal()
	if err != nil {
		return err
	}
	v.Tx.Lock()
	defer v.Tx.Unlock()
	return v.Tx.DeepSet(accountAddress.Bytes(), accBytes, AccountsCfg)
}

// SetAccountInfoTxCheck is an abstraction of ABCI checkTx for an SetAccountInfoTx transaction
// If the bool returned is true means that the account does not exist and is going to be created
func SetAccountInfoTxCheck(vtx *models.Tx, txBytes, signature []byte, state *State) (*setAccountInfoTxCheckValues, error) {
	tx := vtx.GetSetAccountInfo()
	// check signature available
	if signature == nil || tx == nil || txBytes == nil {
		return nil, fmt.Errorf("missing signature and/or transaction")
	}
	accountAddressBytes := tx.GetAccount()
	if accountAddressBytes == nil || len(accountAddressBytes) != types.EntityIDsize {
		return nil, fmt.Errorf("invalid account")
	}
	if bytes.Equal(accountAddressBytes, types.EthereumZeroAddressBytes[:]) {
		return nil, fmt.Errorf("invalid account")
	}
	cost, err := state.TxCost(models.TxType_SET_ACCOUNT_INFO, false)
	if err != nil {
		return nil, err
	}
	txSender, err := ethereum.AddrFromSignature(txBytes, signature)
	if err != nil {
		return nil, fmt.Errorf("cannot extract address from signature %s", err)
	}
	infoURI := tx.GetInfoURI()
	if infoURI == "" {
		return nil, fmt.Errorf("invalid URI, cannot be empty")
	}
	accountAddress := common.BytesToAddress(accountAddressBytes)
	// check account, if not exists a new one will be created
	acc, err := state.GetAccount(accountAddress, false)
	if err != nil {
		return nil, fmt.Errorf("cannot check if account %s exists: %v", accountAddress.String(), err)
	}
	if acc == nil {
		return &setAccountInfoTxCheckValues{Account: accountAddress, TxSender: accountAddress, Create: true}, nil
	}
	txSenderAccount, err := state.GetAccount(txSender, false)
	if err != nil {
		return nil, fmt.Errorf("cannot get nonce of tx sender: %v", err)
	}
	if txSenderAccount == nil {
		return nil, ErrAccountNotFound
	}
	if tx.Nonce != txSenderAccount.Nonce {
		return nil, fmt.Errorf("invalid nonce, expected %d got %d", txSenderAccount.Nonce, tx.Nonce)
	}
	if txSenderAccount.Balance < cost {
		return nil, fmt.Errorf("unauthorized: %s", ErrNotEnoughBalance)
	}
	if acc.InfoURI == infoURI {
		return nil, fmt.Errorf("same infoURI: %s", infoURI)
	}
	return &setAccountInfoTxCheckValues{Account: accountAddress, TxSender: txSender}, nil
}

func MintTokensTxCheck(vtx *models.Tx, txBytes, signature []byte, state *State) (common.Address, uint64, error) {
	tx := vtx.GetMintTokens()
	// check signature available
	if signature == nil || tx == nil || txBytes == nil {
		return common.Address{}, 0, fmt.Errorf("missing signature and/or transaction")
	}
	// check value
	if tx.Value <= 0 {
		return common.Address{}, 0, fmt.Errorf("invalid value")
	}
	// get address from signature
	sigAddress, err := ethereum.AddrFromSignature(txBytes, signature)
	if err != nil {
		return common.Address{}, 0, err
	}
	// get treasurer
	treasurer, err := state.Treasurer(true)
	if err != nil {
		return common.Address{}, 0, err
	}
	// check signature recovered address
	tAddr := common.BytesToAddress(treasurer.Address)
	if tAddr != sigAddress {
		return common.Address{}, 0, fmt.Errorf("address recovered not treasurer: expected %s got %s", treasurer.String(), sigAddress.String())
	}
	// check nonce
	if tx.Nonce != treasurer.Nonce {
		return common.Address{}, 0, fmt.Errorf("invalid nonce %d, expected: %d", tx.Nonce, treasurer.Nonce+1)
	}
	// check to
	if len(tx.To) != types.EntityIDsize || bytes.Equal(tx.To, types.EthereumZeroAddressBytes[:]) {
		return common.Address{}, 0, fmt.Errorf("invalid To address")
	}
	return common.BytesToAddress(tx.To), tx.Value, nil
}

func SetAccountDelegateTxCheck(vtx *models.Tx, txBytes, signature []byte, state *State) (common.Address, common.Address, error) {
	tx := vtx.GetSetAccountDelegateTx()
	// check signature available
	if signature == nil || tx == nil || txBytes == nil {
		return common.Address{}, common.Address{}, fmt.Errorf("missing signature and/or transaction")
	}
	cost, err := state.TxCost(tx.GetTxtype(), false)
	if err != nil {
		return common.Address{}, common.Address{}, err
	}
	authorized, sigAddress, err := state.VerifyAccountBalanceFromSignature(txBytes, signature, cost)
	if err != nil {
		return common.Address{}, common.Address{}, err
	}
	if !authorized {
		return common.Address{}, common.Address{}, ErrNotEnoughBalance
	}
	// check nonce
	acc, err := state.GetAccount(sigAddress, false)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("cannot get account info: %v", err)
	}
	if acc == nil {
		return common.Address{}, common.Address{}, ErrAccountNotFound
	}
	if tx.Nonce != acc.Nonce {
		return common.Address{}, common.Address{}, fmt.Errorf("invalid nonce, expected %d got %d", acc.Nonce, tx.Nonce)
	}
	// check delegate
	delAcc := common.BytesToAddress(tx.Delegate)
	if delAcc.String() == types.EthereumZeroAddressString {
		return common.Address{}, common.Address{}, fmt.Errorf("invalid delegate address")
	}
	switch tx.Txtype {
	case models.TxType_ADD_DELEGATE_FOR_ACCOUNT:
		for i := 0; i < len(acc.DelegateAddrs); i++ {
			delegateToCmp := common.BytesToAddress(acc.DelegateAddrs[i])
			if delegateToCmp == delAcc {
				return common.Address{}, common.Address{}, fmt.Errorf("delegate already added")
			}
		}
		return sigAddress, delAcc, nil
	case models.TxType_DEL_DELEGATE_FOR_ACCOUNT:
		for i := 0; i < len(acc.DelegateAddrs); i++ {
			delegateToCmp := common.BytesToAddress(acc.DelegateAddrs[i])
			if delegateToCmp == delAcc {
				return sigAddress, delAcc, nil
			}
		}
		return common.Address{}, common.Address{}, fmt.Errorf("cannot remove a non existent delegate")
	default:
		return common.Address{}, common.Address{}, fmt.Errorf("unsupported SetAccountDelegate operation")
	}
}

func (v *State) SetDelegate(accountAddr, delegateAddr common.Address, txType models.TxType) error {
	// get account
	acc, err := v.GetAccount(accountAddr, false)
	if err != nil {
		return err
	}
	if acc == nil {
		return ErrAccountNotFound
	}
	setDelegateCost, err := v.TxCost(txType, false)
	if err != nil {
		return err
	}
	switch txType {
	case models.TxType_ADD_DELEGATE_FOR_ACCOUNT:
		acc.DelegateAddrs = append(acc.DelegateAddrs, delegateAddr.Bytes())
		acc.Nonce++
		acc.Balance -= setDelegateCost
		return v.SetAccount(accountAddr, acc)
	case models.TxType_DEL_DELEGATE_FOR_ACCOUNT:
		if err := acc.DelDelegate(delegateAddr); err != nil {
			return err
		}
		acc.Nonce++
		acc.Balance -= setDelegateCost
		return v.SetAccount(accountAddr, acc)
	default:
		return fmt.Errorf("invalid setDelegate tx type")
	}
}

func SendTokensTxCheck(vtx *models.Tx, txBytes, signature []byte, state *State) (*sendTokensTxCheckValues, error) {
	tx := vtx.GetSendTokens()
	// check signature available
	if signature == nil || tx == nil || txBytes == nil {
		return nil, fmt.Errorf("missing signature and/or transaction")
	}
	// get address from signature
	sigAddress, err := ethereum.AddrFromSignature(txBytes, signature)
	if err != nil {
		return nil, err
	}
	// check from
	txFromAddress := common.BytesToAddress(tx.From)
	if txFromAddress != sigAddress {
		return nil, fmt.Errorf("from (%s) field and extracted signature (%s) mismatch",
			txFromAddress.String(),
			sigAddress.String(),
		)
	}
	// check to
	txToAddress := common.BytesToAddress(tx.To)
	if txToAddress.String() == types.EthereumZeroAddressString {
		return nil, fmt.Errorf("invalid address")
	}
	toTxAccount, err := state.GetAccount(txToAddress, false)
	if err != nil {
		return nil, fmt.Errorf("cannot get to account info: %v", err)
	}
	if toTxAccount == nil {
		return nil, ErrAccountNotFound
	}
	// check nonce
	acc, err := state.GetAccount(sigAddress, false)
	if err != nil {
		return nil, fmt.Errorf("cannot get account info: %v", err)
	}
	if acc == nil {
		return nil, ErrAccountNotFound
	}
	if tx.Nonce != acc.Nonce {
		return nil, fmt.Errorf("invalid nonce, expected %d got %d", acc.Nonce, tx.Nonce)
	}
	cost, err := state.TxCost(models.TxType_SEND_TOKENS, false)
	if err != nil {
		return nil, err
	}
	// check value
	if (tx.Value + cost) > acc.Balance {
		return nil, ErrNotEnoughBalance
	}
	return &sendTokensTxCheckValues{sigAddress, txToAddress, tx.Value, tx.Nonce}, nil
}

func CollectFaucetTxCheck(vtx *models.Tx, txBytes, signature []byte, state *State) (common.Address, error) {
	tx := vtx.GetCollectFaucet()
	// check signature available
	if signature == nil || tx == nil || txBytes == nil {
		return common.Address{}, fmt.Errorf("missing signature and/or transaction")
	}
	// get recipient address from signature
	recipientAddress, err := ethereum.AddrFromSignature(txBytes, signature)
	if err != nil {
		return common.Address{}, err
	}
	// get issuer address from faucetPayload
	faucetPkgPayload := tx.FaucetPackage.GetPayload()
	faucetPackageBytes, err := proto.Marshal(faucetPkgPayload)
	if err != nil {
		return common.Address{}, fmt.Errorf("cannot extract faucet package payload: %v", err)
	}
	issuerAddress, err := ethereum.AddrFromSignature(faucetPackageBytes, tx.FaucetPackage.Signature)
	if err != nil {
		return common.Address{}, err
	}
	// check recipient address extracted from signature matches with payload.To
	payloadToAddr := common.BytesToAddress(faucetPkgPayload.GetTo())
	if recipientAddress != payloadToAddr {
		return common.Address{}, fmt.Errorf("address extracted from tx (%s) does not match recipient address (%s)", recipientAddress.String(), payloadToAddr.String())
	}
	// check issuer nonce not used
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, faucetPkgPayload.Identifier)
	key := issuerAddress.Bytes()
	key = append(key, b...)
	used, err := state.FaucetNonce(crypto.Sha256(key), false)
	if err != nil {
		return common.Address{}, fmt.Errorf("cannot check faucet nonce: %v", err)
	}
	if used {
		return common.Address{}, fmt.Errorf("nonce %d already used", faucetPkgPayload.Identifier)
	}
	// check issuer have enough funds
	issuerAcc, err := state.GetAccount(issuerAddress, false)
	if err != nil {
		return common.Address{}, fmt.Errorf("cannot get faucet account: %v", err)
	}
	if issuerAcc == nil {
		return common.Address{}, ErrAccountNotFound
	}
	cost, err := state.TxCost(models.TxType_COLLECT_FAUCET, false)
	if err != nil {
		return common.Address{}, err
	}
	if (issuerAcc.Balance) < faucetPkgPayload.Amount+cost {
		return common.Address{}, fmt.Errorf("faucet does not have enough balance %d < %d", issuerAcc.Balance, faucetPkgPayload.Amount+cost)
	}
	return issuerAddress, nil
}

func (v *State) SetAccount(accountAddress common.Address, account *Account) error {
	accBytes, err := proto.Marshal(account)
	if err != nil {
		return err
	}
	v.Tx.Lock()
	defer v.Tx.Unlock()
	return v.Tx.DeepSet(accountAddress.Bytes(), accBytes, AccountsCfg)
}
