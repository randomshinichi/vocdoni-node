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

var OraclesCfg = statedb.NewSubTreeSingleConfig(
	arbo.HashFunctionSha256,
	"oracs",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var ValidatorsCfg = statedb.NewSubTreeSingleConfig(
	arbo.HashFunctionSha256,
	"valids",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var ProcessesCfg = statedb.NewSubTreeSingleConfig(
	arbo.HashFunctionSha256,
	"procs",
	256,
	rootLeafGetRoot,
	rootLeafSetRoot,
)

var CensusCfg = statedb.NewSubTreeConfig(
	arbo.HashFunctionSha256,
	"cen",
	256,
	processGetCensusRoot,
	processSetCensusRoot,
)

var CensusPoseidonCfg = statedb.NewSubTreeConfig(
	arbo.HashFunctionPoseidon,
	"cenPos",
	64,
	processGetCensusRoot,
	processSetCensusRoot,
)

var VotesCfg = statedb.NewSubTreeConfig(
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
	Store     *statedb.StateDB
	Tx        *statedb.TreeTx
	voteCache *lru.Cache
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
	vs := &State{}
	if err := initStateDB(dataDir, vs); err != nil {
		return nil, fmt.Errorf("cannot init StateDB: %s", err)
	}
	// Must be -1 in order to get the last committed block state, if not block replay will fail
	// log.Infof("loading last safe state db version, this could take a while...")
	// if err = vs.Store.LoadVersion(-1); err != nil {
	// 	if err == iavl.ErrVersionDoesNotExist {
	// 		// restart data db
	// 		log.Warnf("no db version available: %s, restarting vochain database", err)
	// 		if err := os.RemoveAll(dataDir); err != nil {
	// 			return nil, fmt.Errorf("cannot remove dataDir %w", err)
	// 		}
	// 		_ = vs.Store.Close()
	// 		if err := initStateDB(dataDir, vs); err != nil {
	// 			return nil, fmt.Errorf("cannot init db: %w", err)
	// 		}
	// 		vs.voteCache, err = lru.New(voteCacheSize)
	// 		if err != nil {
	// 			return nil, err
	// 		}
	// 		log.Infof("application trees successfully loaded at version %d", vs.Store.Version())
	// 		return vs, nil
	// 	}
	// 	return nil, fmt.Errorf("unknown error loading state db: %w", err)
	// }

	vs.voteCache, err = lru.New(voteCacheSize)
	if err != nil {
		return nil, err
	}
	version, err := vs.Store.Version()
	if err != nil {
		return nil, err
	}
	root, err := vs.Store.Root()
	if err != nil {
		return nil, err
	}
	log.Infof("state database is ready at version %d with hash %x",
		version, root)
	return vs, nil
}

// initStateDB initializes the StateDB with the default subTrees
func initStateDB(dataDir string, state *State) error {
	log.Infof("initializing StateDB")
	db, err := badgerdb.New(badgerdb.Options{Path: dataDir})
	if err != nil {
		return err
	}
	state.Store = statedb.NewStateDB(db)
	startTime := time.Now()
	defer log.Infof("StateDB load took %s", time.Since(startTime))
	root, err := state.Store.Root()
	if err != nil {
		return err
	}
	if bytes.Compare(root, make([]byte, len(root))) != 0 {
		// StateDB already initialized if StateDB.Root != emptyHash
		return nil
	}
	update, err := state.Store.BeginTx()
	if err != nil {
		return err
	}
	if err := update.Add(OraclesCfg.Key(),
		make([]byte, OraclesCfg.HashFunc().Len())); err != nil {
		return err
	}
	if err := update.Add(ValidatorsCfg.Key(),
		make([]byte, ValidatorsCfg.HashFunc().Len())); err != nil {
		return err
	}
	if err := update.Add(ProcessesCfg.Key(),
		make([]byte, ProcessesCfg.HashFunc().Len())); err != nil {
		return err
	}
	return nil
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
	oracles, err := v.Tx.SubTreeSingle(OraclesCfg)
	if err != nil {
		return err
	}
	return oracles.Set(address.Bytes(), []byte{1})
}

// RemoveOracle removes a trusted oracle given its address if exists
func (v *State) RemoveOracle(address common.Address) error {
	v.Lock()
	defer v.Unlock()
	oracles, err := v.Tx.SubTreeSingle(OraclesCfg)
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

	var iterFn func(callback func(key, value []byte) bool) error
	if isQuery {
		mainTree, err := v.Store.TreeView(nil)
		if err != nil {
			return nil, err
		}
		oracles, err := mainTree.SubTreeSingle(OraclesCfg)
		if err != nil {
			return nil, err
		}
		iterFn = oracles.Iterate
	} else {
		oracles, err := v.Tx.SubTreeSingle(OraclesCfg)
		if err != nil {
			return nil, err
		}
		iterFn = oracles.Iterate
	}

	var oracles []common.Address
	if err := iterFn(func(key, value []byte) bool {
		if len(value) != 0 {
			oracles = append(oracles, common.BytesToAddress(key))
		}
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
	validators, err := v.Tx.SubTreeSingle(ValidatorsCfg)
	if err != nil {
		return err
	}
	return validators.Set(validator.Address, validatorBytes)
}

// RemoveValidator removes a tendermint validator identified by its address
func (v *State) RemoveValidator(address []byte) error {
	v.Lock()
	defer v.Unlock()
	validators, err := v.Tx.SubTreeSingle(ValidatorsCfg)
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
func (v *State) Validators(isQuery bool) ([]*models.Validator, error) {
	var validatorBytes []byte
	var err error
	v.RLock()
	if isQuery {
		if validatorBytes, err = v.Store.ImmutableTree(AppTree).Get(validatorKey); err != nil {
			return nil, err
		}
	} else {
		if validatorBytes, err = v.Store.Tree(AppTree).Get(validatorKey); err != nil {
			return nil, err
		}
	}
	v.RUnlock()
	var validators models.ValidatorList
	err = proto.Unmarshal(validatorBytes, &validators)
	return validators.Validators, err
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
	if err := v.setProcess(process, tx.ProcessId); err != nil {
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
	if err := v.setProcess(process, tx.ProcessId); err != nil {
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
	err = v.Store.Tree(VoteTree).Add(vid, ethereum.HashRaw(newVoteBytes))
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

	var voteHash []byte
	vid, err := v.voteID(processID, nullifier)
	if err != nil {
		return nil, err
	}
	v.RLock()
	defer v.RUnlock() // needs to be deferred due to the recover above
	if isQuery {
		if voteHash, err = v.Store.ImmutableTree(VoteTree).Get(vid); err != nil {
			return nil, err
		}
	} else {
		if voteHash, err = v.Store.Tree(VoteTree).Get(vid); err != nil {
			return nil, err
		}
	}
	if voteHash == nil {
		return nil, ErrVoteDoesNotExist
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

// iterateProcessID iterates fn over state tree entries with the processID prefix.
// if isQuery, the IAVL tree is used, otherwise the AVL tree is used.
func (v *State) iterateProcessID(processID []byte,
	fn func(key []byte, value []byte) bool, isQuery bool) bool {
	v.RLock()
	defer v.RUnlock()
	if isQuery {
		v.Store.ImmutableTree(VoteTree).Iterate(processID, fn)
	} else {
		v.Store.Tree(VoteTree).Iterate(processID, fn)
	}
	return true
}

// CountVotes returns the number of votes registered for a given process id
func (v *State) CountVotes(processID []byte, isQuery bool) uint32 {
	var count uint32
	v.iterateProcessID(processID, func(key []byte, value []byte) bool {
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
	v.iterateProcessID(processID, func(key []byte, value []byte) bool {
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
	var headerBytes []byte
	v.RLock()
	if isQuery {
		var err error
		if headerBytes, err = v.Store.ImmutableTree(AppTree).Get(headerKey); err != nil {
			log.Errorf("cannot get vochain height: %s", err)
			return nil
		}
	} else {
		var err error
		if headerBytes, err = v.Store.Tree(AppTree).Get(headerKey); err != nil {
			log.Errorf("cannot get vochain height: %s", err)
			return nil
		}
	}
	v.RUnlock()
	var header models.TendermintHeader
	err := proto.Unmarshal(headerBytes, &header)
	if err != nil {
		log.Errorf("cannot get vochain height: %s", err)
		return nil
	}
	return &header
}

// AppHash returns last hash of the application
func (v *State) AppHash(isQuery bool) []byte {
	var headerBytes []byte
	v.RLock()
	if isQuery {
		var err error
		if headerBytes, err = v.Store.ImmutableTree(AppTree).Get(headerKey); err != nil {
			return []byte{}
		}
	} else {
		var err error
		if headerBytes, err = v.Store.Tree(AppTree).Get(headerKey); err != nil {
			return []byte{}
		}
	}
	v.RUnlock()
	var header models.TendermintHeader
	err := proto.Unmarshal(headerBytes, &header)
	if err != nil {
		return []byte{}
	}
	return header.AppHash
}

// Save persistent save of vochain mem trees
func (v *State) Save() []byte {
	v.Lock()
	hash, err := v.Store.Commit()
	if err != nil {
		panic(fmt.Sprintf("cannot commit state trees: (%v)", err))
	}
	v.Unlock()
	if h := v.Header(false); h != nil {
		height := uint32(h.Height)
		for _, l := range v.eventListeners {
			err := l.Commit(height)
			if err != nil {
				if _, fatal := err.(ErrHaltVochain); fatal {
					panic(err)
				}
				log.Warnf("event callback error on commit: %v", err)
			}
		}
		atomic.StoreUint32(&v.height, height)
	}
	return hash
}

// Rollback rollbacks to the last persistent db data version
func (v *State) Rollback() {
	for _, l := range v.eventListeners {
		l.Rollback()
	}
	v.Lock()
	defer v.Unlock()
	if err := v.Store.Rollback(); err != nil {
		panic(fmt.Sprintf("cannot rollback state tree: (%s)", err))
	}
	atomic.StoreInt32(&v.txCounter, 0)
}

// Height returns the current state height (block count)
func (v *State) Height() uint32 {
	return atomic.LoadUint32(&v.height)
}

// WorkingHash returns the hash of the vochain trees censusRoots
// hash(appTree+processTree+voteTree)
func (v *State) WorkingHash() []byte {
	v.RLock()
	defer v.RUnlock()
	return v.Store.Hash()
}

// TxCounterAdd adds to the atomic transaction counter
func (v *State) TxCounterAdd() {
	atomic.AddInt32(&v.txCounter, 1)
}

// TxCounter returns the current tx count
func (v *State) TxCounter() int32 {
	return atomic.LoadInt32(&v.txCounter)
}
