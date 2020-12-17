package types

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// MessageRequest holds a decoded request but does not decode the body
type RequestMessage struct {
	MetaRequest json.RawMessage `json:"request"`

	ID        string `json:"id"`
	Signature string `json:"signature"`
}

// MetaRequest contains all of the possible request fields.
// Fields must be in alphabetical order
type MetaRequest struct {
	CensusID   string   `json:"censusId,omitempty"`
	CensusURI  string   `json:"censusUri,omitempty"`
	ClaimData  []byte   `json:"claimData,omitempty"`
	ClaimsData [][]byte `json:"claimsData,omitempty"`
	Content    string   `json:"content,omitempty"`
	Digested   bool     `json:"digested,omitempty"`
	EntityId   string   `json:"entityId,omitempty"`
	From       int64    `json:"from,omitempty"`
	FromID     string   `json:"fromId,omitempty"`
	ListSize   int64    `json:"listSize,omitempty"`
	Method     string   `json:"method"`
	Name       string   `json:"name,omitempty"`
	Nullifier  string   `json:"nullifier,omitempty"`
	Payload    []byte   `json:"payload,omitempty"`
	ProcessID  string   `json:"processId,omitempty"`
	ProofData  string   `json:"proofData,omitempty"`
	PubKeys    []string `json:"pubKeys,omitempty"`
	RootHash   string   `json:"rootHash,omitempty"`
	Signature  string   `json:"signature,omitempty"`
	Timestamp  int32    `json:"timestamp"`
	Type       string   `json:"type,omitempty"`
	URI        string   `json:"uri,omitempty"`
}

func (r MetaRequest) String() string {
	v := reflect.ValueOf(r)
	t := v.Type()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s:{", r.Method))
	first := true
	for i := 0; i < t.NumField(); i++ {
		fv := v.Field(i)
		if fv.IsZero() {
			// omit zero values
			continue
		}
		ft := t.Field(i)
		if ft.Name == "Method" {
			continue
		}
		if first {
			first = false
		} else {
			b.WriteString(" ")
		}
		b.WriteString(ft.Name)
		b.WriteString(":")
		if ft.Type.Kind() == reflect.Slice && ft.Type.Elem().Kind() == reflect.Uint8 {
			// print []byte as hexadecimal
			fmt.Fprintf(&b, "%x", fv.Bytes())
		} else {
			fv = reflect.Indirect(fv) // print *T as T
			fmt.Fprintf(&b, "%v", fv.Interface())
		}
	}
	b.WriteString("}")
	return b.String()
}

// ResponseMessage wraps an api response
type ResponseMessage struct {
	MetaResponse json.RawMessage `json:"response"`

	ID        string `json:"id"`
	Signature string `json:"signature"`
}

// MetaResponse contains all of the possible request fields.
// Fields must be in alphabetical order
// Those fields with valid zero-values (such as bool) must be pointers
type MetaResponse struct {
	APIList              []string   `json:"apiList,omitempty"`
	BlockTime            *[5]int32  `json:"blockTime,omitempty"`
	BlockTimestamp       int32      `json:"blockTimestamp,omitempty"`
	CensusID             string     `json:"censusId,omitempty"`
	CensusList           []string   `json:"censusList,omitempty"`
	ClaimsData           [][]byte   `json:"claimsData,omitempty"`
	CommitmentKeys       []Key      `json:"commitmentKeys,omitempty"`
	Content              string     `json:"content,omitempty"`
	EncryptionPrivKeys   []Key      `json:"encryptionPrivKeys,omitempty"`
	EncryptionPublicKeys []Key      `json:"encryptionPubKeys,omitempty"`
	EntityID             string     `json:"entityId,omitempty"`
	EntityIDs            []string   `json:"entityIds,omitempty"`
	Files                []byte     `json:"files,omitempty"`
	Finished             *bool      `json:"finished,omitempty"`
	Health               int32      `json:"health,omitempty"`
	Height               *uint32    `json:"height,omitempty"`
	InvalidClaims        []int      `json:"invalidClaims,omitempty"`
	Message              string     `json:"message,omitempty"`
	Nullifier            string     `json:"nullifier,omitempty"`
	Nullifiers           *[]string  `json:"nullifiers,omitempty"`
	Ok                   bool       `json:"ok"`
	Paused               *bool      `json:"paused,omitempty"`
	Payload              string     `json:"payload,omitempty"`
	ProcessIDs           []string   `json:"processIds,omitempty"`
	ProcessList          []string   `json:"processList,omitempty"`
	Registered           *bool      `json:"registered,omitempty"`
	Request              string     `json:"request"`
	Results              [][]uint32 `json:"results,omitempty"`
	RevealKeys           []Key      `json:"revealKeys,omitempty"`
	Root                 string     `json:"root,omitempty"`
	Siblings             string     `json:"siblings,omitempty"`
	Size                 *int64     `json:"size,omitempty"`
	State                string     `json:"state,omitempty"`
	Timestamp            int32      `json:"timestamp"`
	Type                 string     `json:"type,omitempty"`
	URI                  string     `json:"uri,omitempty"`
	ValidProof           *bool      `json:"validProof,omitempty"`
}

func (r MetaResponse) String() string {
	v := reflect.ValueOf(r)
	t := v.Type()
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < t.NumField(); i++ {
		fv := v.Field(i)
		if fv.IsZero() {
			// omit zero values
			continue
		}
		if b.Len() > 1 {
			b.WriteString(" ")
		}
		ft := t.Field(i)
		b.WriteString(ft.Name)
		b.WriteString(":")
		if ft.Type.Kind() == reflect.Slice && ft.Type.Elem().Kind() == reflect.Uint8 {
			// print []byte as hexadecimal
			fmt.Fprintf(&b, "%x", fv.Bytes())
		} else {
			fv = reflect.Indirect(fv) // print *T as T
			fmt.Fprintf(&b, "%v", fv.Interface())
		}
	}
	b.WriteString("}")
	return b.String()
}

// SetError sets the MetaResponse's Ok field to false, and Message to a string
// representation of v. Usually, v's type will be error or string.
func (r *MetaResponse) SetError(v interface{}) {
	r.Ok = false
	r.Message = fmt.Sprintf("%s", v)
}

type CensusDump struct {
	RootHash   []byte   `json:"rootHash"`
	ClaimsData [][]byte `json:"claimsData"`
}

// VotePackage represents the payload of a vote (usually base64 encoded)
type VotePackage struct {
	Nonce string `json:"nonce,omitempty"`
	Votes []int  `json:"votes"`
}

type Key struct {
	Idx int    `json:"idx"`
	Key string `json:"key"`
}
