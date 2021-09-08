package vochain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	lru "github.com/hashicorp/golang-lru"
	tmcrypto "github.com/tendermint/tendermint/crypto"
	ed25519 "github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/vocdoni/arbo"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/dvote/db/badgerdb"
	"go.vocdoni.io/dvote/log"
	"go.vocdoni.io/dvote/statedb"
	"go.vocdoni.io/dvote/tree"

	// "go.vocdoni.io/dvote/statedblegacy/iavlstate"
	"go.vocdoni.io/dvote/types"
	models "go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

// rootLeafGetRoot is the GetRootFn function for a leaf that is the root
// itself.
func rootLeafGetRoot(value []byte) ([]byte, error) {
	if len(value) != 32 {
		return nil, fmt.Errorf("len(value) = %v != 32", len(value))
	}
	return value, nil
}

// rootLeafSetRoot is the SetRootFn function for a leaf that is the root
// itself.
func rootLeafSetRoot(value []byte, root []byte) ([]byte, error) {
	if len(value) != 32 {
		return nil, fmt.Errorf("len(value) = %v != 32", len(value))
	}
	return root, nil
}

func processGetCensusRoot(value []byte) ([]byte, error) {
	var proc models.StateDBProcess
	if err := proto.Unmarshal(value, &proc); err != nil {
		return nil, fmt.Errorf("cannot unmarshal StateDBProcess: %w", err)
	}
	return proc.Process.CensusRoot, nil
}

func processSetCensusRoot(value []byte, root []byte) ([]byte, error) {
	var proc models.StateDBProcess
	if err := proto.Unmarshal(value, &proc); err != nil {
		return nil, fmt.Errorf("cannot unmarshal StateDBProcess: %w", err)
	}
	proc.Process.CensusRoot = root
	newValue, err := proto.Marshal(&proc)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal StateDBProcess: %w", err)
	}
	return newValue, nil
}

func processGetVotesRoot(value []byte) ([]byte, error) {
	var proc models.StateDBProcess
	if err := proto.Unmarshal(value, &proc); err != nil {
		return nil, fmt.Errorf("cannot unmarshal StateDBProcess: %w", err)
	}
	return proc.VotesRoot, nil
}

func processSetVotesRoot(value []byte, root []byte) ([]byte, error) {
	var proc models.StateDBProcess
	fmt.Printf("DBG processSetVotesRoot %x\n", value)
	if err := proto.Unmarshal(value, &proc); err != nil {
		return nil, fmt.Errorf("cannot unmarshal StateDBProcess: %w", err)
	}
	proc.VotesRoot = root
	newValue, err := proto.Marshal(&proc)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal StateDBProcess: %w", err)
	}
	return newValue, nil
}

var OraclesCfg = statedb.NewTreeSingleConfig(
	arbo.HashFunctionSha256,
	"oracs",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var ValidatorsCfg = statedb.NewTreeSingleConfig(
	arbo.HashFunctionSha256,
	"valids",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var ProcessesCfg = statedb.NewTreeSingleConfig(
	arbo.HashFunctionSha256,
	"procs",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var CensusCfg = statedb.NewTreeNonSingleConfig(
	arbo.HashFunctionSha256,
	"cen",
	256,
	processGetCensusRoot,
	processSetCensusRoot,
)

var CensusPoseidonCfg = statedb.NewTreeNonSingleConfig(
	arbo.HashFunctionPoseidon,
	"cenPos",
	64,
	processGetCensusRoot,
	processSetCensusRoot,
)

var VotesCfg = statedb.NewTreeNonSingleConfig(
	arbo.HashFunctionSha256,
	"votes",
	256,
	processGetVotesRoot,
	processSetVotesRoot,
)

// EventListener is an interface used for executing custom functions during the
// events of the block creation process.
// The order in which events are executed is: Rollback, OnVote, Onprocess, On..., Commit.
// The process is concurrency safe, meaning that there cannot be two sequences
// happening in parallel.
//
// If Commit() returns ErrHaltVochain, the error is considered a consensus
// failure and the blockchain will halt.
//
// If OncProcessResults() returns an error, the results transaction won't be included
// in the blockchain. This event relays on the event handlers to decide if results are
// valid or not since the Vochain State do not validate results.
type EventListener interface {
	OnVote(vote *models.Vote, txIndex int32)
	OnNewTx(blockHeight uint32, txIndex int32)
	OnProcess(pid, eid []byte, censusRoot, censusURI string, txIndex int32)
	OnProcessStatusChange(pid []byte, status models.ProcessStatus, txIndex int32)
	OnCancel(pid []byte, txIndex int32)
	OnProcessKeys(pid []byte, encryptionPub, commitment string, txIndex int32)
	OnRevealKeys(pid []byte, encryptionPriv, reveal string, txIndex int32)
	OnProcessResults(pid []byte, results *models.ProcessResult, txIndex int32) error
	Commit(height uint32) (err error)
	Rollback()
}

type ErrHaltVochain struct {
	reason error
}

func (e ErrHaltVochain) Error() string { return fmt.Sprintf("halting vochain: %v", e.reason) }
func (e ErrHaltVochain) Unwrap() error { return e.reason }

// State represents the state of the vochain application
type State struct {
	Store             *statedb.StateDB
	Tx                *statedb.TreeTx
	mainTreeViewValue atomic.Value
	voteCache         *lru.Cache
	ImmutableState
	mempoolRemoveTxKeys func([][32]byte, bool)
	txCounter           int32
	eventListeners      []EventListener
	height              uint32
}

// ImmutableState holds the latest trees version saved on disk
type ImmutableState struct {
	// Note that the mutex locks the entirety of the three IAVL trees, both
	// their mutable and immutable components. An immutable tree is not safe
	// for concurrent use with its parent mutable tree.
	sync.RWMutex
}

// NewState creates a new State
func NewState(dataDir string) (*State, error) {
	var err error
	sdb, err := initStateDB(dataDir)
	if err != nil {
		return nil, fmt.Errorf("cannot init StateDB: %s", err)
	}
	voteCache, err := lru.New(voteCacheSize)
	if err != nil {
		return nil, err
	}
	version, err := sdb.Version()
	if err != nil {
		return nil, err
	}
	root, err := sdb.Hash()
	if err != nil {
		return nil, err
	}
	log.Infof("state database is ready at version %d with hash %x",
		version, root)
	tx, err := sdb.BeginTx()
	if err != nil {
		return nil, err
	}
	mainTreeView, err := sdb.TreeView(nil)
	if err != nil {
		return nil, err
	}
	s := &State{
		Store:     sdb,
		Tx:        tx,
		voteCache: voteCache,
	}
	s.setMainTreeView(mainTreeView)
	return s, nil
}

// initStateDB initializes the StateDB with the default subTrees
func initStateDB(dataDir string) (*statedb.StateDB, error) {
	log.Infof("initializing StateDB")
	db, err := badgerdb.New(badgerdb.Options{Path: dataDir})
	if err != nil {
		return nil, err
	}
	sdb := statedb.NewStateDB(db)
	startTime := time.Now()
	defer log.Infof("StateDB load took %s", time.Since(startTime))
	root, err := sdb.Hash()
	if err != nil {
		return nil, err
	}
	if bytes.Compare(root, make([]byte, len(root))) != 0 {
		// StateDB already initialized if StateDB.Root != emptyHash
		return sdb, nil
	}
	update, err := sdb.BeginTx()
	defer update.Discard()
	if err != nil {
		return nil, err
	}
	if err := update.Add(OraclesCfg.Key(),
		make([]byte, OraclesCfg.HashFunc().Len())); err != nil {
		return nil, err
	}
	if err := update.Add(ValidatorsCfg.Key(),
		make([]byte, ValidatorsCfg.HashFunc().Len())); err != nil {
		return nil, err
	}
	if err := update.Add(ProcessesCfg.Key(),
		make([]byte, ProcessesCfg.HashFunc().Len())); err != nil {
		return nil, err
	}
	header := models.TendermintHeader{
		Height:  0,
		AppHash: []byte{},
		ChainId: "empty",
	}
	headerBytes, err := proto.Marshal(&header)
	if err != nil {
		return nil, err
	}
	if err := update.Add(headerKey, headerBytes); err != nil {
		return nil, err
	}
	return sdb, update.Commit()
}

func (v *State) mainTreeView() *statedb.TreeView {
	return v.mainTreeViewValue.Load().(*statedb.TreeView)
}

// TODO: @mvdan is the usage of atomic.Value appropiate here?  Or should I
// better use atomic.LoadPointer & atomic.StorePointer?
func (v *State) setMainTreeView(treeView *statedb.TreeView) {
	v.mainTreeViewValue.Store(treeView)
}

func (v *State) mainTreeViewer(isQuery bool) statedb.TreeViewer {
	var mainTree statedb.TreeViewer
	if isQuery {
		mainTree = v.mainTreeView()
	} else {
		mainTree = v.Tx.AsTreeView()
	}
	return mainTree
}

// AddEventListener adds a new event listener, to receive method calls on block
// events as documented in EventListener.
func (v *State) AddEventListener(l EventListener) {
	v.eventListeners = append(v.eventListeners, l)
}

// AddOracle adds a trusted oracle given its address if not exists
func (v *State) AddOracle(address common.Address) error {
	v.Lock()
	defer v.Unlock()
	return v.Tx.DeepSet([]*statedb.TreeConfig{OraclesCfg},
		address.Bytes(), []byte{1})
}

// RemoveOracle removes a trusted oracle given its address if exists
func (v *State) RemoveOracle(address common.Address) error {
	v.Lock()
	defer v.Unlock()
	oracles, err := v.Tx.SubTree(OraclesCfg)
	if err != nil {
		return err
	}
	if _, err := oracles.Get(address.Bytes()); tree.IsNotFound(err) {
		return fmt.Errorf("oracle not found")
	} else if err != nil {
		return err
	}
	return oracles.Set(address.Bytes(), nil)
}

// Oracles returns the current oracle list
func (v *State) Oracles(isQuery bool) ([]common.Address, error) {
	v.RLock()
	defer v.RUnlock()

	oraclesTree, err := v.mainTreeViewer(isQuery).SubTree(OraclesCfg)
	if err != nil {
		return nil, err
	}

	var oracles []common.Address
	if err := oraclesTree.Iterate(func(key, value []byte) bool {
		if len(value) == 0 {
			return true
		}
		oracles = append(oracles, common.BytesToAddress(key))
		return true
	}); err != nil {
		return nil, err
	}
	return oracles, nil
}

// hexPubKeyToTendermintEd25519 decodes a pubKey string to a ed25519 pubKey
func hexPubKeyToTendermintEd25519(pubKey string) (tmcrypto.PubKey, error) {
	var tmkey ed25519.PubKey
	pubKeyBytes, err := hex.DecodeString(pubKey)
	if err != nil {
		return nil, err
	}
	if len(pubKeyBytes) != 32 {
		return nil, fmt.Errorf("pubKey length is invalid")
	}
	copy(tmkey[:], pubKeyBytes[:])
	return tmkey, nil
}

// AddValidator adds a tendemint validator if it is not already added
func (v *State) AddValidator(validator *models.Validator) error {
	v.Lock()
	defer v.Unlock()
	validatorBytes, err := proto.Marshal(validator)
	if err != nil {
		return err
	}
	return v.Tx.DeepSet([]*statedb.TreeConfig{ValidatorsCfg},
		validator.Address, validatorBytes)
}

// RemoveValidator removes a tendermint validator identified by its address
func (v *State) RemoveValidator(address []byte) error {
	v.Lock()
	defer v.Unlock()
	validators, err := v.Tx.SubTree(ValidatorsCfg)
	if err != nil {
		return err
	}
	if _, err := validators.Get(address); tree.IsNotFound(err) {
		return fmt.Errorf("validator not found")
	} else if err != nil {
		return err
	}
	return validators.Set(address, nil)
}

// Validators returns a list of the validators saved on persistent storage
func (v *State) Validators(isQuery bool) ([]models.Validator, error) {
	v.RLock()
	defer v.RUnlock()

	validatorsTree, err := v.mainTreeViewer(isQuery).SubTree(ValidatorsCfg)
	if err != nil {
		return nil, err
	}

	var validators []models.Validator
	var callbackErr error
	if err := validatorsTree.Iterate(func(key, value []byte) bool {
		if len(value) == 0 {
			return true
		}
		var validator models.Validator
		if err := proto.Unmarshal(value, &validator); err != nil {
			callbackErr = err
			return false
		}
		validators = append(validators, validator)
		return true
	}); err != nil {
		return nil, err
	}
	if callbackErr != nil {
		return nil, callbackErr
	}
	return validators, nil
}

// AddProcessKeys adds the keys to the process
func (v *State) AddProcessKeys(tx *models.AdminTx) error {
	if tx.ProcessId == nil || tx.KeyIndex == nil {
		return fmt.Errorf("no processId or keyIndex provided on AddProcessKeys")
	}
	process, err := v.Process(tx.ProcessId, false)
	if err != nil {
		return err
	}
	if tx.CommitmentKey != nil {
		process.CommitmentKeys[*tx.KeyIndex] = fmt.Sprintf("%x", tx.CommitmentKey)
		log.Debugf("added commitment key %d for process %x: %x",
			*tx.KeyIndex, tx.ProcessId, tx.CommitmentKey)
	}
	if tx.EncryptionPublicKey != nil {
		process.EncryptionPublicKeys[*tx.KeyIndex] = fmt.Sprintf("%x", tx.EncryptionPublicKey)
		log.Debugf("added encryption key %d for process %x: %x",
			*tx.KeyIndex, tx.ProcessId, tx.EncryptionPublicKey)
	}
	if process.KeyIndex == nil {
		process.KeyIndex = new(uint32)
	}
	*process.KeyIndex++
	if err := v.updateProcess(process, tx.ProcessId); err != nil {
		return err
	}
	for _, l := range v.eventListeners {
		l.OnProcessKeys(tx.ProcessId, fmt.Sprintf("%x", tx.EncryptionPublicKey),
			fmt.Sprintf("%x", tx.CommitmentKey), v.TxCounter())
	}
	return nil
}

// RevealProcessKeys reveals the keys of a process
func (v *State) RevealProcessKeys(tx *models.AdminTx) error {
	if tx.ProcessId == nil || tx.KeyIndex == nil {
		return fmt.Errorf("no processId or keyIndex provided on AddProcessKeys")
	}
	process, err := v.Process(tx.ProcessId, false)
	if err != nil {
		return err
	}
	if process.KeyIndex == nil || *process.KeyIndex < 1 {
		return fmt.Errorf("no keys to reveal, keyIndex is < 1")
	}
	rkey := ""
	if tx.RevealKey != nil {
		rkey = fmt.Sprintf("%x", tx.RevealKey)
		process.RevealKeys[*tx.KeyIndex] = rkey // TBD: Change hex strings for []byte
		log.Debugf("revealed commitment key %d for process %x: %x",
			*tx.KeyIndex, tx.ProcessId, tx.RevealKey)
	}
	ekey := ""
	if tx.EncryptionPrivateKey != nil {
		ekey = fmt.Sprintf("%x", tx.EncryptionPrivateKey)
		process.EncryptionPrivateKeys[*tx.KeyIndex] = ekey
		log.Debugf("revealed encryption key %d for process %x: %x",
			*tx.KeyIndex, tx.ProcessId, tx.EncryptionPrivateKey)
	}
	*process.KeyIndex--
	if err := v.updateProcess(process, tx.ProcessId); err != nil {
		return err
	}
	for _, l := range v.eventListeners {
		l.OnRevealKeys(tx.ProcessId, ekey, rkey, v.TxCounter())
	}
	return nil
}

// AddVote adds a new vote to a process and call the even listeners to OnVote.
// This method does not check if the vote already exist!
func (v *State) AddVote(vote *models.Vote) error {
	vid, err := v.voteID(vote.ProcessId, vote.Nullifier)
	if err != nil {
		return err
	}
	// save block number
	vote.Height = v.Height()
	newVoteBytes, err := proto.Marshal(vote)
	if err != nil {
		return fmt.Errorf("cannot marshal vote")
	}
	v.Lock()
	err = v.Tx.DeepAdd([]*statedb.TreeConfig{ProcessesCfg, VotesCfg.WithKey(vote.ProcessId)},
		vid, ethereum.HashRaw(newVoteBytes))
	v.Unlock()
	if err != nil {
		return err
	}
	for _, l := range v.eventListeners {
		l.OnVote(vote, v.TxCounter())
	}
	return nil
}

// voteID = byte( processID+nullifier )
func (v *State) voteID(pid, nullifier []byte) ([]byte, error) {
	if len(pid) != types.ProcessIDsize {
		return nil, fmt.Errorf("wrong processID size %d", len(pid))
	}
	if len(nullifier) != types.VoteNullifierSize {
		return nil, fmt.Errorf("wrong nullifier size %d", len(nullifier))
	}
	vid := bytes.Buffer{}
	vid.Write(pid)
	vid.Write(nullifier)
	return vid.Bytes(), nil
}

// Envelope returns the hash of a stored vote if exists.
func (v *State) Envelope(processID, nullifier []byte, isQuery bool) (_ []byte, err error) {
	// TODO(mvdan): remove the recover once
	// https://github.com/tendermint/iavl/issues/212 is fixed
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered panic: %v", r)
		}
	}()

	vid, err := v.voteID(processID, nullifier)
	if err != nil {
		return nil, err
	}
	v.RLock()
	defer v.RUnlock() // needs to be deferred due to the recover above
	votesTree, err := v.mainTreeViewer(isQuery).DeepSubTree(
		[]*statedb.TreeConfig{ProcessesCfg, VotesCfg.WithKey(processID)})
	if tree.IsNotFound(err) {
		return nil, ErrProcessNotFound
	} else if err != nil {
		return nil, err
	}
	voteHash, err := votesTree.Get(vid)
	if tree.IsNotFound(err) {
		return nil, ErrVoteDoesNotExist
	} else if err != nil {
		return nil, err
	}
	return voteHash, nil
}

// EnvelopeExists returns true if the envelope identified with voteID exists
func (v *State) EnvelopeExists(processID, nullifier []byte, isQuery bool) (bool, error) {
	e, err := v.Envelope(processID, nullifier, isQuery)
	if err != nil && err != ErrVoteDoesNotExist {
		return false, err
	}
	if err == ErrVoteDoesNotExist {
		return false, nil
	}
	return e != nil, nil
}

// iterateVotes iterates fn over state tree entries with the processID prefix.
// if isQuery, the IAVL tree is used, otherwise the AVL tree is used.
func (v *State) iterateVotes(processID []byte,
	fn func(key []byte, value []byte) bool, isQuery bool) error {
	v.RLock()
	defer v.RUnlock()
	votesTree, err := v.mainTreeViewer(isQuery).DeepSubTree(
		[]*statedb.TreeConfig{ProcessesCfg, VotesCfg.WithKey(processID)})
	if err != nil {
		return err
	}
	return votesTree.Iterate(fn)
}

// CountVotes returns the number of votes registered for a given process id
func (v *State) CountVotes(processID []byte, isQuery bool) uint32 {
	var count uint32
	// TODO: Once statedb.TreeView.Size() works, replace this by that.
	v.iterateVotes(processID, func(key []byte, value []byte) bool {
		count++
		return false
	}, isQuery)
	return count
}

// EnvelopeList returns a list of registered envelopes nullifiers given a processId
func (v *State) EnvelopeList(processID []byte, from, listSize int,
	isQuery bool) (nullifiers [][]byte) {
	// TODO(mvdan): remove the recover once
	// https://github.com/tendermint/iavl/issues/212 is fixed
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("recovered panic: %v", r)
			// TODO(mvdan): this func should return an error instead
			// err = fmt.Errorf("recovered panic: %v", r)
		}
	}()
	idx := 0
	v.iterateVotes(processID, func(key []byte, value []byte) bool {
		if idx >= from+listSize {
			return true
		}
		if idx >= from {
			nullifiers = append(nullifiers, key[32:])
		}
		idx++
		return false
	}, isQuery)
	return nullifiers
}

// Header returns the blockchain last block committed height
func (v *State) Header(isQuery bool) *models.TendermintHeader {
	v.RLock()
	headerBytes, err := v.mainTreeViewer(isQuery).Get(headerKey)
	v.RUnlock()
	if err != nil {
		log.Fatalf("cannot get headerKey from mainTree: %s", err)
	}
	var header models.TendermintHeader
	if err := proto.Unmarshal(headerBytes, &header); err != nil {
		log.Fatalf("cannot get proto.Unmarshal header: %s", err)
	}
	return &header
}

// AppHash returns last hash of the application
// func (v *State) AppHash(isQuery bool) []byte {
// 	return v.Header(isQuery).AppHash
// }

// Save persistent save of vochain mem trees
func (v *State) Save() ([]byte, error) {
	v.Lock()
	err := func() error {
		if err := v.Tx.Commit(); err != nil {
			return fmt.Errorf("cannot commit statedb tx: %w", err)
		}
		var err error
		if v.Tx, err = v.Store.BeginTx(); err != nil {
			return fmt.Errorf("cannot begin statedb tx: %w", err)
		}
		return nil
	}()
	v.Unlock()
	if err != nil {
		return nil, err
	}
	mainTreeView, err := v.Store.TreeView(nil)
	if err != nil {
		return nil, fmt.Errorf("cannot get statdeb mainTreeView: %w", err)
	}
	v.setMainTreeView(mainTreeView)
	height := uint32(v.Header(false).Height)
	for _, l := range v.eventListeners {
		if err := l.Commit(height); err != nil {
			if _, fatal := err.(ErrHaltVochain); fatal {
				return nil, err
			}
			log.Warnf("event callback error on commit: %v", err)
		}
	}
	atomic.StoreUint32(&v.height, height)
	return v.Store.Hash()
}

// Rollback rollbacks to the last persistent db data version
func (v *State) Rollback() {
	for _, l := range v.eventListeners {
		l.Rollback()
	}
	v.Lock()
	defer v.Unlock()
	v.Tx.Discard()
	var err error
	if v.Tx, err = v.Store.BeginTx(); err != nil {
		log.Fatalf("cannot begin statedb tx: %s", err)
	}
	atomic.StoreInt32(&v.txCounter, 0)
}

// Height returns the current state height (block count)
func (v *State) Height() uint32 {
	return atomic.LoadUint32(&v.height)
}

// TODO: Return error
// WorkingHash returns the hash of the vochain trees censusRoots
// hash(appTree+processTree+voteTree)
func (v *State) WorkingHash() []byte {
	v.RLock()
	defer v.RUnlock()
	hash, err := v.Tx.Root()
	if err != nil {
		panic(fmt.Sprintf("cannot get statedb mainTree root: %s", err))
	}
	return hash
}

// TxCounterAdd adds to the atomic transaction counter
func (v *State) TxCounterAdd() {
	atomic.AddInt32(&v.txCounter, 1)
}

// TxCounter returns the current tx count
func (v *State) TxCounter() int32 {
	return atomic.LoadInt32(&v.txCounter)
}
