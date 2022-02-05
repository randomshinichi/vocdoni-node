package vochain

import (
	"encoding/json"
	"fmt"
	"time"

	"go.vocdoni.io/dvote/types"
	"go.vocdoni.io/proto/build/go/models"
)

const (
	voteCachePurgeThreshold = uint32(180) // in blocks about 30 minutes
	voteCacheSize           = 100000
	NewProcessCost          = 0
	SetProcessCost          = 0
)

var (
	ErrVoteDoesNotExist    = fmt.Errorf("vote does not exist")
	ErrNotEnoughBalance    = fmt.Errorf("not enough balance to transfer")
	ErrAccountNonceInvalid = fmt.Errorf("invalid account nonce")
	ErrProcessNotFound     = fmt.Errorf("process not found")
	ErrBalanceOverflow     = fmt.Errorf("balance overflow")
	ErrAccountBalanceZero  = fmt.Errorf("zero balance account not valid")
	// keys; not constants because of []byte
	voteCountKey = []byte("voteCount")
)

// PrefixDBCacheSize is the size of the cache for the MutableTree IAVL databases
var PrefixDBCacheSize = 0

// VotePackage represents the payload of a vote (usually base64 encoded)
type VotePackage struct {
	Nonce string `json:"nonce,omitempty"`
	Votes []int  `json:"votes"`
}

// UniqID returns a uniq identifier for the VoteTX. It depends on the Type.
func UniqID(tx *models.SignedTx, isAnonymous bool) string {
	if !isAnonymous {
		if len(tx.Signature) > 32 {
			return string(tx.Signature[:32])
		}
	}
	return ""
}

// ________________________ QUERIES ________________________

// QueryData is an abstraction of any kind of data a query request could have
type QueryData struct {
	Method      string `json:"method"`
	ProcessID   string `json:"processId,omitempty"`
	Nullifier   string `json:"nullifier,omitempty"`
	From        int64  `json:"from,omitempty"`
	ListSize    int64  `json:"listSize,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	ProcessType string `json:"type,omitempty"`
}

// ________________________ GENESIS APP STATE ________________________

// GenesisAppState application state in genesis
type GenesisAppState struct {
	Validators []GenesisValidator `json:"validators"`
	Oracles    []string           `json:"oracles"`
	Treasurer  string             `json:"treasurer"`
}

// The rest of these genesis app state types are copied from
// github.com/tendermint/tendermint/types, for the sake of making this package
// lightweight and not have it import heavy indirect dependencies like grpc or
// crypto/*.

type GenesisDoc struct {
	GenesisTime     time.Time          `json:"genesis_time"`
	ChainID         string             `json:"chain_id"`
	ConsensusParams *ConsensusParams   `json:"consensus_params,omitempty"`
	Validators      []GenesisValidator `json:"validators,omitempty"`
	AppHash         types.HexBytes     `json:"app_hash"`
	AppState        json.RawMessage    `json:"app_state,omitempty"`
}

type ConsensusParams struct {
	Block     BlockParams     `json:"block"`
	Evidence  EvidenceParams  `json:"evidence"`
	Validator ValidatorParams `json:"validator"`
	Version   VersionParams   `json:"version"`
}

// BlockParams define limits on the block size and gas plus minimum time
// between blocks.
type BlockParams struct {
	MaxBytes   int64 `json:"max_bytes"`
	MaxGas     int64 `json:"max_gas"`
	TimeIotaMs int64 `json:"time_iota_ms"` // Deprecated on TENDERMINT 0.35

}

type EvidenceParams struct {
	MaxAgeNumBlocks int64 `json:"max_age_num_blocks"`
	// only accept new evidence more recent than this
	MaxAgeDuration time.Duration `json:"max_age_duration"`
}

type ValidatorParams struct {
	PubKeyTypes []string `json:"pub_key_types"`
}

type VersionParams struct {
	AppVersion uint64 `json:"app_version"`
}

type GenesisValidator struct {
	Address types.HexBytes   `json:"address"`
	PubKey  TendermintPubKey `json:"pub_key"`
	Power   string           `json:"power"`
	Name    string           `json:"name"`
}

type TendermintPubKey struct {
	Type  string `json:"type"`
	Value []byte `json:"value"`
}

// _________________________ CENSUS ORIGINS __________________________

type CensusProperties struct {
	Name              string
	AllowCensusUpdate bool
	NeedsDownload     bool
	NeedsIndexSlot    bool
	NeedsURI          bool
	WeightedSupport   bool
}

var CensusOrigins = map[models.CensusOrigin]CensusProperties{
	models.CensusOrigin_OFF_CHAIN_TREE: {Name: "offchain tree",
		NeedsDownload: true, NeedsURI: true, AllowCensusUpdate: true},
	models.CensusOrigin_OFF_CHAIN_TREE_WEIGHTED: {
		Name: "offchain weighted tree", NeedsDownload: true, NeedsURI: true,
		WeightedSupport: true, AllowCensusUpdate: true,
	},
	models.CensusOrigin_ERC20: {Name: "erc20", NeedsDownload: true,
		WeightedSupport: true, NeedsIndexSlot: true},
	models.CensusOrigin_OFF_CHAIN_CA: {Name: "ca", WeightedSupport: true,
		NeedsURI: true, AllowCensusUpdate: true},
}
