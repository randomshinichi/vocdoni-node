package statedb

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"go.vocdoni.io/dvote/db/badgerdb"
)

func TestStateDB(t *testing.T) {
	sdb := newTestStateDB(t)
	version, err := sdb.Version()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, version, qt.DeepEquals, uint64(0))
}

func newTestStateDB(t *testing.T) *StateDB {
	db, err := badgerdb.New(badgerdb.Options{Path: t.TempDir()})
	qt.Assert(t, err, qt.IsNil)
	sdb := NewStateDB(db)
	return sdb
}
