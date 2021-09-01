package statedb

import (
	"fmt"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/vocdoni/arbo"
	"go.vocdoni.io/dvote/db"
	"go.vocdoni.io/dvote/db/badgerdb"
	"go.vocdoni.io/dvote/tree"
)

var emptyHash = make([]byte, 32)

func TestVersion(t *testing.T) {
	sdb := newTestStateDB(t)
	version, err := sdb.Version()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, version, qt.Equals, uint32(0))

	root := make([]byte, 32)
	root[0] = 0x41
	wTx := sdb.db.WriteTx()
	qt.Assert(t, setVersionRoot(wTx, 1, root), qt.IsNil)
	qt.Assert(t, wTx.Commit(), qt.IsNil)

	version, err = sdb.Version()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, version, qt.Equals, uint32(1))

	root1, err := sdb.VersionRoot(1)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, root1, qt.DeepEquals, root)

	root1, err = sdb.Root()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, root1, qt.DeepEquals, root)

	// dumpPrint(sdb.db)
}

func TestStateDB(t *testing.T) {
	sdb := newTestStateDB(t)
	version, err := sdb.Version()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, version, qt.Equals, uint32(0))

	// Can't view because the tree doesn't exist yet
	{
		_, err := sdb.TreeView()
		qt.Assert(t, err, qt.Equals, ErrEmptyTree)
	}

	mainTree, err := sdb.BeginTx()
	qt.Assert(t, err, qt.IsNil)

	// Initial root is emptyHash
	root, err := mainTree.Root()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, root, qt.DeepEquals, emptyHash)

	keys := [][]byte{[]byte("key0"), []byte("key1"), []byte("key2"), []byte("key3")}
	vals := [][]byte{[]byte("val0"), []byte("val1"), []byte("val2"), []byte("val3")}

	qt.Assert(t, mainTree.Add(keys[0], vals[0]), qt.IsNil)
	qt.Assert(t, mainTree.Add(keys[1], vals[1]), qt.IsNil)

	// Current root is != emptyHash
	root1, err := mainTree.Root()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, root1, qt.Not(qt.DeepEquals), emptyHash)

	qt.Assert(t, mainTree.Commit(), qt.IsNil)

	// statedb.Root == mainTree.Root
	rootStateDB, err := sdb.Root()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, rootStateDB, qt.DeepEquals, root1)

	// mainTreeView.Root = statedb.Root
	{
		mainTreeView, err := sdb.TreeView()
		qt.Assert(t, err, qt.IsNil)
		rootView, err := mainTreeView.Root()
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, rootView, qt.DeepEquals, root1)

		value, err := mainTreeView.Get(keys[0])
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, value, qt.DeepEquals, vals[0])
	}

	// dumpPrint(sdb.db)

	// fmt.Printf("DBG --- NewTx\n")

	// Begin another Tx
	mainTree, err = sdb.BeginTx()
	qt.Assert(t, err, qt.IsNil)

	root2, err := mainTree.Root()
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, root2, qt.DeepEquals, root1)

	// Insert a new key-value
	qt.Assert(t, mainTree.Add(keys[2], vals[2]), qt.IsNil)

	// Uncommited changes are not available in the View
	{
		mainTreeView, err := sdb.TreeView()
		qt.Assert(t, err, qt.IsNil)

		_, err = mainTreeView.Get(keys[2])
		qt.Assert(t, err, qt.Equals, arbo.ErrKeyNotFound)
	}

	mainTree.Discard()
}

var singleCfg = NewSubTreeSingleConfig(
	arbo.HashFunctionSha256,
	[]byte("single"),
	256,
	func(value []byte) ([]byte, error) {
		if len(value) != 32 {
			return nil, fmt.Errorf("len(value) = %v != 32", len(value))
		}
		return value, nil
	},
	func(value []byte, root []byte) ([]byte, error) {
		if len(value) != 32 {
			return nil, fmt.Errorf("len(value) = %v != 32", len(value))
		}
		return root, nil
	},
)

var multiACfg = NewSubTreeConfig(
	arbo.HashFunctionSha256,
	[]byte("multia"),
	256,
	func(value []byte) ([]byte, error) {
		if len(value) != 64 {
			return nil, fmt.Errorf("len(value) = %v != 64", len(value))
		}
		return value[:32], nil
	},
	func(value []byte, root []byte) ([]byte, error) {
		if len(value) != 64 {
			return nil, fmt.Errorf("len(value) = %v != 64", len(value))
		}
		copy(value[:32], root)
		return value, nil
	},
)

var multiBCfg = NewSubTreeConfig(
	arbo.HashFunctionPoseidon,
	[]byte("multib"),
	256,
	func(value []byte) ([]byte, error) {
		if len(value) != 64 {
			return nil, fmt.Errorf("len(value) = %v != 64", len(value))
		}
		return value[32:], nil
	},
	func(value []byte, root []byte) ([]byte, error) {
		if len(value) != 64 {
			return nil, fmt.Errorf("len(value) = %v != 64", len(value))
		}
		copy(value[32:], root)
		return value, nil
	},
)

func TestSubTree(t *testing.T) {
	sdb := newTestStateDB(t)

	mainTree, err := sdb.BeginTx()
	qt.Assert(t, err, qt.IsNil)

	// Example of a singleton subtree which root is the leaf of the main tree
	qt.Assert(t, mainTree.Add(singleCfg.Key(), emptyHash), qt.IsNil)
	// treePrint(mainTree.tree, mainTree.txTree, "main")
	single, err := mainTree.SubTreeSingle(singleCfg)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, single.Add([]byte("key0"), []byte("value0")), qt.IsNil)
	qt.Assert(t, single.Add([]byte("key1"), []byte("value1")), qt.IsNil)
	// treePrint(single.tree, single.txTree, "single")

	// Example of two (multi) subtrees which roots are in a leaf of the main tree
	id := []byte("01234567")
	qt.Assert(t, mainTree.Add(id, make([]byte, 32*2)), qt.IsNil)
	// treePrint(mainTree.tree, mainTree.txTree, "main")
	multiA, err := mainTree.SubTree(id, multiACfg)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, multiA.Add([]byte("key2"), []byte("value2")), qt.IsNil)
	multiB, err := mainTree.SubTree(id, multiBCfg)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, multiB.Add([]byte("key3"), []byte("value3")), qt.IsNil)
	// treePrint(multiA.tree, multiA.txTree, "multiA")

	qt.Assert(t, mainTree.Commit(), qt.IsNil)

	// dumpPrint(sdb.db)

	// Check that values in each subtree is as expected
	{
		mainTreeView, err := sdb.TreeView()
		qt.Assert(t, err, qt.IsNil)

		single, err := mainTreeView.SubTreeSingle(singleCfg)
		qt.Assert(t, err, qt.IsNil)
		v0, err := single.Get([]byte("key0"))
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, v0, qt.DeepEquals, []byte("value0"))
		v1, err := single.Get([]byte("key1"))
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, v1, qt.DeepEquals, []byte("value1"))

		multiA, err := mainTreeView.SubTree(id, multiACfg)
		qt.Assert(t, err, qt.IsNil)
		// fmt.Printf("DBG %+v\n", multiA.db)
		// dumpPrint(multiA.tree.DB())
		// treePrint(multiA.tree, nil, "multiA")
		v2, err := multiA.Get([]byte("key2"))
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, v2, qt.DeepEquals, []byte("value2"))

		multiB, err := mainTreeView.SubTree(id, multiBCfg)
		qt.Assert(t, err, qt.IsNil)
		v3, err := multiB.Get([]byte("key3"))
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, v3, qt.DeepEquals, []byte("value3"))
	}

	// Check the roots in the leafs are as expected
	{
		mainTreeView, err := sdb.TreeView()
		qt.Assert(t, err, qt.IsNil)
		singleParentLeaf, err := mainTreeView.Get(singleCfg.Key())
		qt.Assert(t, err, qt.IsNil)
		multiParentLeaf, err := mainTreeView.Get(id)
		qt.Assert(t, err, qt.IsNil)

		single, err := mainTreeView.SubTreeSingle(singleCfg)
		qt.Assert(t, err, qt.IsNil)
		singleRoot, err := single.Root()
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, singleParentLeaf, qt.DeepEquals, singleRoot)

		multiA, err := mainTreeView.SubTree(id, multiACfg)
		qt.Assert(t, err, qt.IsNil)
		multiARoot, err := multiA.Root()
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, multiParentLeaf[:32], qt.DeepEquals, multiARoot)

		multiB, err := mainTreeView.SubTree(id, multiBCfg)
		qt.Assert(t, err, qt.IsNil)
		multiBRoot, err := multiB.Root()
		qt.Assert(t, err, qt.IsNil)
		qt.Assert(t, multiParentLeaf[32:], qt.DeepEquals, multiBRoot)
	}
}

func newTestStateDB(t *testing.T) *StateDB {
	db, err := badgerdb.New(badgerdb.Options{Path: t.TempDir()})
	qt.Assert(t, err, qt.IsNil)
	sdb := NewStateDB(db)
	return sdb
}

func toString(v []byte) string {
	elems := strings.Split(string(v), "/")
	elemsClean := make([]string, len(elems))
	for i, elem := range elems {
		asciiPrint := true
		for _, b := range []byte(elem) {
			if b < 0x20 || 0x7e < b {
				asciiPrint = false
				break
			}
		}
		var elemClean string
		if asciiPrint {
			elemClean = string(elem)
		} else {
			elemClean = fmt.Sprintf("\\x%x", elem)
		}
		if len(elemClean) > 10 {
			elemClean = fmt.Sprintf("%v..", elemClean[:10])
		}
		elemsClean[i] = elemClean
	}
	return strings.Join(elemsClean, "/")
}

func dumpPrint(db db.Database) {
	fmt.Printf("--- DB Print ---\n")
	db.Iterate(nil, func(key, value []byte) bool {
		fmt.Printf("%v -> %v\n", toString(key), toString(value))
		return true
	})
}

func treePrint(t *tree.Tree, tx db.ReadTx, name string) {
	fmt.Printf("--- Tree Print (%s)---\n", name)
	if err := t.Iterate(tx, func(key, value []byte) bool {
		if value[0] != arbo.PrefixValueLeaf {
			return true
		}
		leafK, leafV := arbo.ReadLeafValue(value)
		if len(leafK) > 10 {
			fmt.Printf("%x..", leafK[:10])
		} else {
			fmt.Printf("%x", leafK)
		}
		fmt.Printf(" -> ")
		if len(leafV) > 10 {
			fmt.Printf("%x..", leafV[:10])
		} else {
			fmt.Printf("%x", leafV)
		}
		fmt.Printf("\n")
		return true
	}); err != nil {
		panic(err)
	}
}

func TestTree(t *testing.T) {
	db, err := badgerdb.New(badgerdb.Options{Path: t.TempDir()})
	qt.Assert(t, err, qt.IsNil)

	tx := db.WriteTx()
	txTree := subWriteTx(tx, subKeyTree)
	tree, err := tree.New(txTree,
		tree.Options{DB: nil, MaxLevels: 256, HashFunc: arbo.HashFunctionSha256})
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, tree.Add(txTree, []byte("key0"), []byte("value0")), qt.IsNil)
	tx.Commit()
	// dumpPrint(db)
}

// TODO: Test NoState
