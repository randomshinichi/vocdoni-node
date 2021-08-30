/*
Package statedb contains the implementation of StateDB, a database backed
structure that holds the state of the blockchain indexed by version (each
version corresponding to the state at each block).  The StateDB holds a dynamic
hierarcy of linked merkle trees starting with the mainTree on top, with the
property that the keys and values of all merkle trees can be cryptographically
represented by a single hash, the StateDB.Root (which corresponds to the
mainTree.Root).

Internally all subTrees of the StateDB use the same database (for views) and
the same transaction (for a block update).  Database prefixes are used to split
the storage of each subTree while avoiding collisions.  The structure of
prefixes is detailed here:
- subTree: `{KindID}{id}/`
	- arbo.Tree: `t/`
	- subTrees: `s/`
		- contains any number of subTree
	- nostate: `n/`
	- metadata: `m/`
		- versions: `v/` (only in mainTree)
			- currentVersion: `current` -> {currentVersion}
			- version to root: `{version}` -> `{root}`

Since the mainTree is unique and doesn't have a parent, the prefixes used in
the mainTree skip the first element of the path (`{KindID}{id}/`).
- Example:
	- mainTree arbo.Tree: `t/`
	- processTree arbo.Tree (a singleton under mainTree): `s/process/t/`
	- censusTree arbo.Tree (a non-singleton under processTree):
	`s/process/s/census{pID}/t/` (we are using pID, the processID as the id
	of the subTree)
	- voteTree arbo.Tree (a non-singleton under processTree):
	`s/process/s/vote{pID}/t/` (we are using pID, the processID as the id
	of the subTree)

Each tree has an associated database that can be accessed via the NoState
method.  These databases are auxiliary key-values that don't belong to the
blockchain state, and thus any value in the NoState databases won't be
reflected in the StateDB.Root.  One of the usecases for the NoState database is
to store auxiliary mappings used to optimize the capacity usage of merkletrees
used for zkSNARKS, where the number of levels is of critical matter.  For
example, we may want to build a census tree of babyjubjub public keys that will
be used to prove ownership of the public key via a SNARK in order to vote.  If
we build the merkle tree using the public key as path, we will have an
unbalanced tree which requires more levels than strictly necessary.  On the
other hand, if we use a sequential index as path and set the value to the
public key, we achieve maximum balancing reducing the number of tree levels.
But then we can't easily query for the existence of a public key in the tree to
generate a proof.  In such a case, we can store the mapping of public key to
index in the NoState database.
*/
package statedb

import (
	"encoding/binary"
	"errors"
	"path"

	"github.com/vocdoni/arbo"
	"go.vocdoni.io/dvote/db"
	"go.vocdoni.io/dvote/db/prefixeddb"
	"go.vocdoni.io/dvote/tree"
)

const (
	subKeyTree    = "t"
	subKeyMeta    = "m"
	subKeyNoState = "n"
	subKeySubTree = "s"
)

const pathVersion = "v"

var keyCurVersion = []byte("current")

// uint32ToBytes encodes an uint32 as a little endian in []byte
func uint32ToBytes(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// subDB returns a db.Database prefixed with `path | '/'`
func subDB(db db.Database, path string) db.Database {
	return prefixeddb.NewPrefixedDatabase(db, []byte(path+"/"))
}

// subWriteTx returns a db.WriteTx prefixed with `path | '/'`
func subWriteTx(tx db.WriteTx, path string) db.WriteTx {
	return prefixeddb.NewPrefixedWriteTx(tx, []byte(path+"/"))
}

// subReadTx returns a db.ReadTx prefixed with `path | '/'`
func subReadTx(tx db.ReadTx, path string) db.ReadTx {
	return prefixeddb.NewPrefixedReadTx(tx, []byte(path+"/"))
}

// Viewer is an interface for a read-only key-value database
type Viewer interface {
	Get(key []byte) ([]byte, error)
}

// databaseViewer is a wrapper over db.Database that implements Viewer.
type databaseViewer struct {
	db db.Database
}

// Get the key from the database.  Internally uses a new read transaction.
func (v *databaseViewer) Get(key []byte) ([]byte, error) {
	tx := v.db.ReadTx()
	defer tx.Discard()
	return tx.Get(key)
}

// Updater is an interface for a read-write key-value database.
type Updater interface {
	Viewer
	Set(key, value []byte) error
}

// treeConfig is a unified configuration for a subTree.
type treeConfig struct {
	parentLeafKey []byte
	prefix        string
	*SubTreeConfig
}

// GetRootFn is a function type that takes a leaf value and returns the contained root.
type GetRootFn func(value []byte) ([]byte, error)

// SetRootFn is a function type that takes a leaf value and a root, updates the
// leaf value with the new root and returns it.
type SetRootFn func(value []byte, root []byte) ([]byte, error)

// SubTreeConfig
type SubTreeConfig struct {
	hashFunc          arbo.HashFunction
	kindID            string
	parentLeafGetRoot GetRootFn
	parentLeafSetRoot SetRootFn
	maxLevels         int
}

// SubTreeSingleConfig contains the configuration used for a non-singleton subTree.
func NewSubTreeConfig(hashFunc arbo.HashFunction, kindID string, maxLevels int,
	parentLeafGetRoot GetRootFn, parentLeafSetRoot SetRootFn) *SubTreeConfig {
	return &SubTreeConfig{
		hashFunc:          hashFunc,
		kindID:            kindID,
		parentLeafGetRoot: parentLeafGetRoot,
		parentLeafSetRoot: parentLeafSetRoot,
		maxLevels:         maxLevels,
	}
}

// treeConfig returns a unified configuration type for opening a singleton
// subTree that is identified by `id`.  `id` is the path in the parent tree to
// the leaf that contains the subTree root.
func (c *SubTreeConfig) treeConfig(id []byte) *treeConfig {
	return &treeConfig{
		parentLeafKey: id,
		prefix:        c.kindID + string(id),
		SubTreeConfig: c,
	}
}

// SubTreeSingleConfig contains the configuration used for a singleton subTree.
type SubTreeSingleConfig struct {
	*SubTreeConfig
}

// NewSubTreeSingleConfig creates a new SubTreeSingleConfig.
func NewSubTreeSingleConfig(hashFunc arbo.HashFunction, kindID string, maxLevels int,
	parentLeafGetRoot GetRootFn, parentLeafSetRoot SetRootFn) *SubTreeSingleConfig {
	return &SubTreeSingleConfig{
		NewSubTreeConfig(hashFunc, kindID, maxLevels, parentLeafGetRoot, parentLeafSetRoot),
	}
}

// Key returns the key used in the parent tree in which the value that contains
// the subTree root is stored.  The key is the path of the parent leaf with the root.
func (c *SubTreeSingleConfig) Key() []byte {
	return []byte(c.kindID)
}

// treeConfig returns a unified configuration type for opening a subTree.
func (c *SubTreeSingleConfig) treeConfig() *treeConfig {
	return &treeConfig{
		parentLeafKey: []byte(c.kindID),
		prefix:        c.kindID,
		SubTreeConfig: c.SubTreeConfig,
	}
}

// mainTreeCfg is the subTree configuration of the mainTree.  It doesn't have a
// kindID because it's the top level tree.  For the same reason, it doesn't
// contain functions to work with the parent leaf: it doesn't have a parent.
var mainTreeCfg = NewSubTreeSingleConfig(arbo.HashFunctionSha256, "", 256, nil, nil).treeConfig()

// StateDB is a database backed structure that holds a dynamic hierarchy of
// linked merkle trees with the property that the keys and values of all merkle
// trees can be cryptographically represented by a single hash, the
// StateDB.Root (which corresponds to the mainTree.Root).
type StateDB struct {
	hashLen int
	db      db.Database
}

// NewStateDB returns an instance of the StateDB.
func NewStateDB(db db.Database) *StateDB {
	return &StateDB{
		hashLen: mainTreeCfg.hashFunc.Len(),
		db:      db,
	}
}

// setVersionRoot is a helper function used to set the last version and its
// corresponding root from the top level tx.
func setVersionRoot(tx db.WriteTx, version uint32, root []byte) error {
	txMetaVer := subWriteTx(tx, path.Join(subKeyMeta, pathVersion))
	if err := txMetaVer.Set(keyCurVersion, uint32ToBytes(version)); err != nil {
		return err
	}
	return txMetaVer.Set(uint32ToBytes(version), root)
}

// getVersionRoot is a helper function used get the last version from the top
// level tx.
func getVersion(tx db.ReadTx) (uint32, error) {
	versionLE, err := subReadTx(tx, path.Join(subKeyMeta, pathVersion)).Get(keyCurVersion)
	if err == db.ErrKeyNotFound {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(versionLE), nil
}

// getVersionRoot is a helper function used get the root of version from the
// top level tx.
func (s *StateDB) getVersionRoot(tx db.ReadTx, version uint32) ([]byte, error) {
	if version == 0 {
		return make([]byte, s.hashLen), nil
	}
	root, err := subReadTx(tx, path.Join(subKeyMeta, pathVersion)).Get(uint32ToBytes(version))
	if err != nil {
		return nil, err
	}
	return root, nil
}

// getRoot is a helper function used to get the last version's root from the
// top level tx.
func (s *StateDB) getRoot(tx db.ReadTx) ([]byte, error) {
	version, err := getVersion(tx)
	if err != nil {
		return nil, err
	}
	return s.getVersionRoot(tx, version)
}

// The first commited version is 1.  Calling Version on a fresh StateDB will
// return 0.
func (s *StateDB) Version() (uint32, error) {
	tx := s.db.ReadTx()
	defer tx.Discard()
	return getVersion(tx)
}

// VersionRoot returns the StateDB root corresponding to the version v.  A new
// StateDB always has the version 0 with root == emptyHash.
func (s *StateDB) VersionRoot(v uint32) ([]byte, error) {
	tx := s.db.ReadTx()
	defer tx.Discard()
	return s.getVersionRoot(tx, v)
}

// Root returns the root of the StateDB, which corresponds to the root of the
// mainTree.  This root is a hash that cryptographically represents the entire
// StateDB (except for the NoState databases).
func (s *StateDB) Root() ([]byte, error) {
	tx := s.db.ReadTx()
	defer tx.Discard()
	return s.getRoot(tx)
}

// BeginTx creates a new transaction for the StateDB to begin an update, with
// the mainTree opened for update wrapped in the returned TreeTx.  You must
// either call treeTx.Commit or treeTx.Discard if BeginTx doesn't return an
// error.  Calling treeTx.Discard after treeTx.Commit is ok.
func (s *StateDB) BeginTx() (treeTx *TreeTx, err error) {
	cfg := mainTreeCfg
	tx := s.db.WriteTx()
	defer func() {
		if err != nil {
			tx.Discard()
		}
	}()
	txTree := subWriteTx(tx, subKeyTree)
	tree, err := tree.New(txTree,
		tree.Options{DB: nil, MaxLevels: cfg.maxLevels, HashFunc: cfg.hashFunc})
	if err != nil {
		return nil, err
	}
	return &TreeTx{
		sdb: s,
		TreeUpdate: TreeUpdate{
			tx: tx,
			tree: treeWithTx{
				Tree: tree,
				tx:   txTree,
			},
			cfg:      cfg,
			openSubs: make(map[string]*TreeUpdate),
		},
	}, nil
}

// readOnlyWriteTx is a wrapper over a db.ReadTx that implements the db.WriteTx
// methods but forbids them by returning errors.  This is used for functions
// that take a db.WriteTx but only write conditionally, and we want to detect a
// write attempt.
type readOnlyWriteTx struct {
	db.ReadTx
}

// ErrReadOnly is returned when a write operation is attempted on a db.WriteTx
// that we set up for read only.
var ErrReadOnly = errors.New("read only")

// ErrEmptyTree is returned when a tree is opened for read-only but hasn't been
// created yet.
var ErrEmptyTree = errors.New("empty tree")

// Set implements db.WriteTx.Set but returns error always.
func (t *readOnlyWriteTx) Set(key []byte, value []byte) error {
	return ErrReadOnly
}

// Set implements db.WriteTx.Delete but returns error always.
func (t *readOnlyWriteTx) Delete(key []byte) error {
	return ErrReadOnly
}

// Commit implements db.WriteTx.COmmit but returns nil always.
func (t *readOnlyWriteTx) Commit() error {
	return nil
}

// TreeView returns the mainTree opened at root as a TreeView for read-only.
// If root is nil, the last version's root is used.
func (s *StateDB) TreeView(root []byte) (*TreeView, error) {
	cfg := mainTreeCfg

	tx := s.db.ReadTx()
	defer tx.Discard()
	if root == nil {
		var err error
		if root, err = s.getRoot(tx); err != nil {
			return nil, err
		}
	}

	txTree := subReadTx(tx, subKeyTree)
	defer txTree.Discard()
	tree, err := tree.New(&readOnlyWriteTx{txTree},
		tree.Options{DB: subDB(s.db, subKeyTree), MaxLevels: cfg.maxLevels, HashFunc: cfg.hashFunc})
	if err == ErrReadOnly {
		return nil, ErrEmptyTree
	} else if err != nil {
		return nil, err
	}
	tree, err = tree.FromRoot(root)
	if err != nil {
		return nil, err
	}
	return &TreeView{
		db:   s.db,
		tree: tree,
		cfg:  mainTreeCfg,
	}, nil
}

// TreeView is an opened tree that can only be read.
type TreeView struct {
	// db is db.Database where the contents of this TreeView are stored.
	// The db is already prefixed to avoid collision with parent and
	// sibling TreeUpdates.  From this db, the following prefixes are
	// available:
	// - arbo.Tree (used in TreeView.tree): `t/`
	// - subTrees (used in TreeView.subTree): `s/`
	// - nostate (used in TreeView.NoState): `n/`
	// - metadata: `m/`
	db db.Database
	// tree is the Arbo merkle tree in this TreeView.
	tree *tree.Tree
	// cfg points to this TreeView configuration.
	cfg *treeConfig
}

// NoState returns a read-only key-value database associated with this tree
// that doesn't affect the cryptographic integrity of the StateDB.
func (v *TreeView) NoState() Viewer {
	return &databaseViewer{
		db: subDB(v.db, subKeyNoState),
	}
}

// Get returns the value at key in this tree.  `key` is the path of the leaf,
// and the returned value is the leaf's value.
func (v *TreeView) Get(key []byte) ([]byte, error) {
	return v.tree.Get(nil, key)
}

// Iterate iterates over all nodes of this tree.
func (v *TreeView) Iterate(callback func(key, value []byte) bool) error {
	return v.tree.Iterate(nil, callback)
}

// Root returns the root of the tree, which cryptographically summarises the
// state of the tree.
func (v *TreeView) Root() ([]byte, error) {
	return v.tree.Root(nil)
}

// Size returns the number of leafs (key-values) that this tree contains.
func (v *TreeView) Size() (uint64, error) {
	// NOTE: Tree.Size is currently unimplemented
	return v.tree.Size(nil), nil
}

// GenProof generates a proof of existence of the given key for this tree.  The
// returned values are the leaf value and the proof itself.
func (v *TreeView) GenProof(key []byte) ([]byte, []byte, error) {
	return v.tree.GenProof(nil, key)
}

// Dump exports all the tree leafs in a byte array.
func (v *TreeView) Dump() ([]byte, error) {
	return v.tree.Dump()
}

// subTree is an internal function used to open the subTree (singleton and
// non-singleton) as a TreeView.  The treeView.db is created from
// v.db appending the prefix `subKeySubTree | cfg.prefix`.  In turn
// the treeView.db uses the db.Database from treeView.db appending the
// prefix `'/' | subKeyTree`.  The treeView.tree is opened as a snapshot from
// the root found in its parent leaf
func (v *TreeView) subTree(cfg *treeConfig) (treeView *TreeView, err error) {
	parentLeaf, err := v.tree.Get(nil, cfg.parentLeafKey)
	if err != nil {
		return nil, err
	}
	root, err := cfg.parentLeafGetRoot(parentLeaf)
	if err != nil {
		return nil, err
	}

	db := subDB(v.db, path.Join(subKeySubTree, cfg.prefix))
	tx := db.ReadTx()
	defer tx.Discard()
	txTree := subReadTx(tx, subKeyTree)
	defer txTree.Discard()
	tree, err := tree.New(&readOnlyWriteTx{txTree},
		tree.Options{DB: subDB(db, subKeyTree), MaxLevels: cfg.maxLevels, HashFunc: cfg.hashFunc})
	if err == ErrReadOnly {
		return nil, ErrEmptyTree
	} else if err != nil {
		return nil, err
	}
	tree, err = tree.FromRoot(root)
	if err != nil {
		return nil, err
	}
	return &TreeView{
		db:   db,
		tree: tree,
		cfg:  mainTreeCfg,
	}, nil
}

// SubTreeSingle returns a TreeView of a singleton SubTree whose root is stored
// in the leaf with `cfg.Key()`, and is parametrized by `cfg`.
func (v *TreeView) SubTreeSingle(c *SubTreeSingleConfig) (*TreeView, error) {
	return v.subTree(c.treeConfig())
}

// SubTree returns a TreeView of a non-singleton SubTree whose root is stored
// in the leaf with `key`, and is parametrized by `cfg`.
func (v *TreeView) SubTree(key []byte, c *SubTreeConfig) (*TreeView, error) {
	return v.subTree(c.treeConfig(key))
}

// treeWithTx is an Arbo merkle tree with the db.WriteTx used for updating it.
type treeWithTx struct {
	*tree.Tree
	tx db.WriteTx
}

// TreeUpdate is an opened tree that can be updated.  All updates are stored in
// an internal transaction (shared with all the opened subTrees) and will only
// be commited with the TreeTx.Commit function is called.  The TreeUpdate is
// not safe for concurrent use.
type TreeUpdate struct {
	// tx is db.WriteTx where all the changes done to this TreeUpdate will
	// be applied.  The tx is already prefixed to avoid collision with
	// parent and sibling TreeUpdates.  From this tx, the following prefixes are available:
	// - arbo.Tree (used in TreeUpdate.tree): `t/`
	// - subTrees (used in TreeUpdate.subTree): `s/`
	// - nostate (used in TreeUpdate.NoState): `n/`
	// - metadata: `m/`
	tx db.WriteTx
	// dirtyTree is set to true when the TreeUpdate.tree has been modified.
	// If true, during the TreeTx.Commit operation, the root of this tree
	// will be propagated upwards to update the parent trees.
	dirtyTree bool
	// tree is the Arbo merkle tree (with the db.WriteTx) in this
	// TreeUpdate.
	tree treeWithTx
	// openSubs is a map of opened subTrees in a particular TreeTx whose
	// parent is this TreeUpdate.  The key is the subtree prefix.  This map
	// is used in the TreeTx.Commit to traverse all opened subTrees in a
	// TreeTx in order to propagate the roots upwards to update the
	// corresponding parent leafs up to the mainTree.
	openSubs map[string]*TreeUpdate
	// cfg points to this TreeUpdate configuration.
	cfg *treeConfig
}

// Get returns the value at key in this tree.  `key` is the path of the leaf,
// and the returned value is the leaf's value.
func (u *TreeUpdate) Get(key []byte) ([]byte, error) {
	return u.tree.Get(u.tree.tx, key)
}

// Iterate iterates over all nodes of this tree.
func (u *TreeUpdate) Iterate(callback func(key, value []byte) bool) error {
	return u.tree.Iterate(u.tree.tx, callback)
}

// Root returns the root of the tree, which cryptographically summarises the
// state of the tree.
func (u *TreeUpdate) Root() ([]byte, error) {
	return u.tree.Root(u.tree.tx)
}

// Size returns the number of leafs (key-values) that this tree contains.
func (u *TreeUpdate) Size() (uint64, error) {
	// NOTE: Tree.Size is currently unimplemented
	return u.tree.Size(u.tree.tx), nil
}

// GenProof generates a proof of existence of the given key for this tree.  The
// returned values are the leaf value and the proof itself.
func (u *TreeUpdate) GenProof(key []byte) ([]byte, []byte, error) {
	return u.tree.GenProof(u.tree.tx, key)
}

// Unimplemented because arbo.Tree.Dump doesn't take db.ReadTx as input.
// func (u *TreeUpdate) Dump() ([]byte, error) {
// 	panic("TODO")
// }

// NoState returns a key-value database associated with this tree that doesn't
// affect the cryptographic integrity of the StateDB.  Writing to this database
// won't change the StateDB.Root.
func (u *TreeUpdate) NoState() Updater {
	return subWriteTx(u.tx, subKeyNoState)
}

// Add a new key-value to this tree.  `key` is the path of the leaf, and
// `value` is the content of the leaf.
func (u *TreeUpdate) Add(key, value []byte) error {
	u.dirtyTree = true
	return u.tree.Add(u.tree.tx, key, value)
}

// Set adds or updates a key-value in this tree.  `key` is the path of the
// leaf, and `value` is the content of the leaf.
func (u *TreeUpdate) Set(key, value []byte) error {
	u.dirtyTree = true
	return u.tree.Set(u.tree.tx, key, value)
}

// subTree is an internal function used to open the subTree (singleton and
// non-singleton) as a TreeUpdate.  The treeUpdate.tx is created from
// u.tx appending the prefix `subKeySubTree | cfg.prefix`.  In turn
// the treeUpdate.tree uses the db.WriteTx from treeUpdate.tx appending the
// prefix `'/' | subKeyTree`.
func (u *TreeUpdate) subTree(cfg *treeConfig) (treeUpdate *TreeUpdate, err error) {
	if treeUpdate, ok := u.openSubs[string(cfg.prefix)]; ok {
		return treeUpdate, nil
	}
	tx := subWriteTx(u.tx, path.Join(subKeySubTree, cfg.prefix))
	defer func() {
		if err != nil {
			tx.Discard()
		}
	}()
	txTree := subWriteTx(tx, subKeyTree)
	tree, err := tree.New(txTree,
		tree.Options{DB: nil, MaxLevels: cfg.maxLevels, HashFunc: cfg.hashFunc})
	if err != nil {
		return nil, err
	}
	treeUpdate = &TreeUpdate{
		tx: tx,
		tree: treeWithTx{
			Tree: tree,
			tx:   txTree,
		},
		openSubs: make(map[string]*TreeUpdate),
		cfg:      cfg,
	}
	u.openSubs[string(cfg.prefix)] = treeUpdate
	return treeUpdate, nil
}

// SubTreeSingle returns a TreeUpdate of a singleton SubTree whose root is stored
// in the leaf with `cfg.Key()`, and is parametrized by `cfg`.
func (u *TreeUpdate) SubTreeSingle(cfg *SubTreeSingleConfig) (*TreeUpdate, error) {
	return u.subTree(cfg.treeConfig())
}

// SubTree returns a TreeUpdate of a non-singleton SubTree whose root is stored
// in the leaf with `key`, and is parametrized by `cfg`.
func (u *TreeUpdate) SubTree(key []byte, cfg *SubTreeConfig) (*TreeUpdate, error) {
	return u.subTree(cfg.treeConfig(key))
}

// TreeTx is a wrapper over TreeUpdate that includes the Commit and Discard
// methods to control the transaction used to update the StateDB.  It contains
// the mainTree opened in the wrapped TreeUpdate.  The TreeTx is not safe for
// concurent use.
type TreeTx struct {
	sdb *StateDB
	// TreeUpdate contains the mainTree opened for updates.
	TreeUpdate
}

// update is a helper struct used to collect subTree updates that need to
// update the parent's corresponding leaf with the new root.
type update struct {
	// key     []byte
	setRoot SetRootFn
	root    []byte
}

// propagateRoot performs a Depth-First Search on the opened subTrees,
// propagating the roots and updating the parent leaves when the trees are
// dirty.  Only when the treeUpdate.tree is not dirty, and no open subTrees
// (recursively) are dirty we can skip propagating (and thus skip updating the
// treeUpdate.tree, avoiding recalculating hashes unnecessarily).
func propagateRoot(treeUpdate *TreeUpdate) ([]byte, error) {
	// End of recursion
	if len(treeUpdate.openSubs) == 0 {
		// If tree is not dirty, there's nothing to propagate
		if !treeUpdate.dirtyTree {
			return nil, nil
		} else {
			return treeUpdate.tree.Root(treeUpdate.tree.tx)
		}
	}
	// Gather all the updates that need to be applied to the
	// treeUpdate.tree leaves, by leaf key
	updatesByKey := make(map[string][]update)
	for _, sub := range treeUpdate.openSubs {
		root, err := propagateRoot(sub)
		if err != nil {
			return nil, err
		}
		if root == nil {
			continue
		}
		key := sub.cfg.parentLeafKey
		updatesByKey[string(key)] = append(updatesByKey[string(key)], update{
			// key:     key,
			setRoot: sub.cfg.parentLeafSetRoot,
			root:    root,
		})
	}
	// If there are no updates for treeUpdate.tree leaves, and treeUpdate
	// is not dirty, there's nothing to propagate
	if len(updatesByKey) == 0 && !treeUpdate.dirtyTree {
		return nil, nil
	}
	// Apply the updates to treeUpdate.tree leaves.  There can be multiple
	// updates for a single leaf, so we apply them all and then update the
	// leaf once.
	for key, updates := range updatesByKey {
		leaf, err := treeUpdate.tree.Get(treeUpdate.tree.tx, []byte(key))
		if err != nil {
			return nil, err
		}
		for _, update := range updates {
			leaf, err = update.setRoot(leaf, update.root)
			if err != nil {
				return nil, err
			}
		}
		if err := treeUpdate.tree.Set(treeUpdate.tree.tx, []byte(key), leaf); err != nil {
			return nil, err
		}
	}
	// We either updated leaves here, or treeUpdate.tree was dirty, so we
	// propagate the root to trigger updates in the parent tree.
	return treeUpdate.tree.Root(treeUpdate.tree.tx)
}

// Commit will write all the changes made from the TreeTx into the database,
// propagating the roots of dirtiy subTrees up to the mainTree so that a new
// Hash/Root (mainTree.Root == StateDB.Root) is calculated representing the
// state.
func (t *TreeTx) Commit() error {
	root, err := propagateRoot(&t.TreeUpdate)
	if err != nil {
		return err
	}
	t.openSubs = make(map[string]*TreeUpdate)
	version, err := getVersion(t.tx)
	if err != nil {
		return err
	}
	// If root is nil, it means that there were no updates to the StateDB,
	// so the next version root is the current version root.
	if root == nil {
		if root, err = t.sdb.getVersionRoot(t.tx, version); err != nil {
			return err
		}
	}
	if err := setVersionRoot(t.tx, version+1, root); err != nil {
		return err
	}
	return t.tx.Commit()
}

// Discard all the changes that have been made from the TreeTx.  After calling
// Discard, the TreeTx shouldn't no longer be used.
func (t *TreeTx) Discard() {
	t.tx.Discard()
}
