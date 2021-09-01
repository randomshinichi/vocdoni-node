package statedb

import (
	"encoding/binary"
	"errors"

	"github.com/vocdoni/arbo"
	"go.vocdoni.io/dvote/db"
	"go.vocdoni.io/dvote/db/prefixeddb"
	"go.vocdoni.io/dvote/tree"
)

// type TreeType string
//
// const (
// 	TreeTypeSha256   TreeType = "sha256"
// 	TreeTypePoseidon TreeType = "poseidon"
// 	TreeTypeBlake2b  TreeType = "blake2b"
// )
//
// func hashLen(typ TreeType) int {
// 	switch typ {
// 	case TreeTypeSha256:
// 		return 32
// 	case TreeTypePoseidon:
// 		return 32
// 	case TreeTypeBlake2b:
// 		return 32
// 	default:
// 		panic(fmt.Sprintf("Unsupported TreeType %v", typ))
// 	}
// }

func appendCopy(s []byte, v ...byte) []byte {
	// Copy s to ensure we don't reuse the slice's backing array.
	appended := make([]byte, 0, len(s)+len(v))
	appended = append(appended, s...)
	appended = append(appended, v...)
	// Make sure the appended's capacity is fixed, to prevent future appends from sharing memory.
	return appended[:len(appended):len(appended)]
}

var separator byte = '/'

var (
	subKeyTree    []byte = []byte("t")
	subKeyMeta    []byte = []byte("m")
	subKeyNoState []byte = []byte("n")
	subKeySubTree []byte = []byte("s")
)

var (
	pathVersion   []byte = []byte("v")
	keyCurVersion []byte = []byte("current")
)

func uint32ToBytes(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func bytesToUint32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}

func join(pathA, pathB []byte) []byte {
	return appendCopy(appendCopy(pathA, separator), pathB...)
}

func subDB(db db.Database, path []byte) db.Database {
	return prefixeddb.NewPrefixedDatabase(db, appendCopy(path, separator))
}

func subWriteTx(tx db.WriteTx, path []byte) db.WriteTx {
	return prefixeddb.NewPrefixedWriteTx(tx, appendCopy(path, separator))
}

func subReadTx(tx db.ReadTx, path []byte) db.ReadTx {
	return prefixeddb.NewPrefixedReadTx(tx, appendCopy(path, separator))
}

type Viewer interface {
	Get(key []byte) ([]byte, error)
}

type databaseViewer struct {
	db db.Database
}

func (v *databaseViewer) Get(key []byte) ([]byte, error) {
	tx := v.db.ReadTx()
	defer tx.Discard()
	return tx.Get(key)
}

type Updater interface {
	Viewer
	Set(key, value []byte) error
}

type txUpdater struct {
	tx db.WriteTx
}

func (u *txUpdater) Get(key []byte) ([]byte, error) {
	return u.tx.Get(key)
}

func (u *txUpdater) Set(key []byte, value []byte) error {
	return u.tx.Set(key, value)
}

type treeConfig struct {
	parentLeafKey []byte
	prefix        []byte
	*SubTreeConfig
}

type GetRootFn func(value []byte) ([]byte, error)
type SetRootFn func(value []byte, root []byte) ([]byte, error)

type SubTreeConfig struct {
	hashFunc          arbo.HashFunction
	kindID            []byte
	parentLeafGetRoot GetRootFn
	parentLeafSetRoot SetRootFn
	maxLevels         int
}

func NewSubTreeConfig(hashFunc arbo.HashFunction, kindID []byte, maxLevels int,
	parentLeafGetRoot GetRootFn, parentLeafSetRoot SetRootFn) *SubTreeConfig {
	return &SubTreeConfig{
		hashFunc:          hashFunc,
		kindID:            kindID,
		parentLeafGetRoot: parentLeafGetRoot,
		parentLeafSetRoot: parentLeafSetRoot,
		maxLevels:         maxLevels,
	}
}

func (c *SubTreeConfig) treeConfig(id []byte) *treeConfig {
	return &treeConfig{
		parentLeafKey: id,
		prefix:        appendCopy(c.kindID, id...),
		SubTreeConfig: c,
	}
}

type SubTreeSingleConfig struct {
	*SubTreeConfig
}

func NewSubTreeSingleConfig(hashFunc arbo.HashFunction, kindID []byte, maxLevels int,
	parentLeafGetRoot GetRootFn, parentLeafSetRoot SetRootFn) *SubTreeSingleConfig {
	return &SubTreeSingleConfig{
		NewSubTreeConfig(hashFunc, kindID, maxLevels, parentLeafGetRoot, parentLeafSetRoot),
	}
}

func (c *SubTreeSingleConfig) Key() []byte {
	return c.kindID
}

func (c *SubTreeSingleConfig) treeConfig() *treeConfig {
	return &treeConfig{
		parentLeafKey: c.kindID,
		prefix:        c.kindID,
		SubTreeConfig: c.SubTreeConfig,
	}
}

var mainTreeCfg = NewSubTreeSingleConfig(arbo.HashFunctionSha256, nil, 256, nil, nil).treeConfig()

type StateDB struct {
	hashLen int
	db      db.Database
}

func NewStateDB(db db.Database) *StateDB {
	return &StateDB{
		hashLen: mainTreeCfg.hashFunc.Len(),
		db:      db,
	}
}

func setVersionRoot(tx db.WriteTx, version uint32, root []byte) error {
	txMetaVer := subWriteTx(tx, join(subKeyMeta, pathVersion))
	if err := txMetaVer.Set(keyCurVersion, uint32ToBytes(version)); err != nil {
		return err
	}
	return txMetaVer.Set(uint32ToBytes(version), root)
}

func getVersion(tx db.ReadTx) (uint32, error) {
	versionLE, err := subReadTx(tx, join(subKeyMeta, pathVersion)).Get(keyCurVersion)
	if err == db.ErrKeyNotFound {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return bytesToUint32(versionLE), nil
}

func (s *StateDB) getVersionRoot(tx db.ReadTx, version uint32) ([]byte, error) {
	if version == 0 {
		return make([]byte, s.hashLen), nil
	}
	root, err := subReadTx(tx, join(subKeyMeta, pathVersion)).Get(uint32ToBytes(version))
	if err != nil {
		return nil, err
	}
	return root, nil
}

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

func (s *StateDB) VersionRoot(v uint32) ([]byte, error) {
	tx := s.db.ReadTx()
	defer tx.Discard()
	return s.getVersionRoot(tx, v)
}

func (s *StateDB) Root() ([]byte, error) {
	tx := s.db.ReadTx()
	defer tx.Discard()
	return s.getRoot(tx)
}

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
		TreeUpdate: TreeUpdate{
			tx:       tx,
			txTree:   txTree,
			tree:     tree,
			cfg:      cfg,
			openSubs: make(map[string]*TreeUpdate),
		},
	}, nil
}

type readOnlyWriteTx struct {
	db.ReadTx
}

var ErrReadOnly = errors.New("read only")
var ErrEmptyTree = errors.New("empty tree")

func (t *readOnlyWriteTx) Set(key []byte, value []byte) error {
	return ErrReadOnly
}

func (t *readOnlyWriteTx) Delete(key []byte) error {
	return ErrReadOnly
}

func (t *readOnlyWriteTx) Commit() error {
	return ErrReadOnly
}

func (s *StateDB) TreeView() (*TreeView, error) {
	cfg := mainTreeCfg

	tx := s.db.ReadTx()
	defer tx.Discard()
	root, err := s.getRoot(tx)
	if err != nil {
		return nil, err
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

type TreeView struct {
	db   db.Database
	tree *tree.Tree
	cfg  *treeConfig
}

func (v *TreeView) NoState() Viewer {
	return &databaseViewer{
		db: subDB(v.db, subKeyNoState),
	}
}

func (v *TreeView) Get(key []byte) ([]byte, error) {
	return v.tree.Get(nil, key)
}

func (v *TreeView) Iterate(callback func(key, value []byte) bool) error {
	return v.tree.Iterate(nil, callback)
}

func (v *TreeView) Root() ([]byte, error) {
	return v.tree.Root(nil)
}

func (v *TreeView) Size() (uint64, error) {
	// NOTE: Tree.Size is currently unimplemented
	return v.tree.Size(nil), nil
}

func (v *TreeView) GenProof(key []byte) ([]byte, []byte, error) {
	return v.tree.GenProof(nil, key)
}

func (v *TreeView) Dump() ([]byte, error) {
	return v.tree.Dump()
}

func (v *TreeView) subTree(cfg *treeConfig) (*TreeView, error) {
	parentLeaf, err := v.tree.Get(nil, cfg.parentLeafKey)
	if err != nil {
		return nil, err
	}
	root, err := cfg.parentLeafGetRoot(parentLeaf)
	if err != nil {
		return nil, err
	}

	db := subDB(v.db, cfg.prefix)
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

func (v *TreeView) SubTreeSingle(c *SubTreeSingleConfig) (*TreeView, error) {
	return v.subTree(c.treeConfig())
}

func (v *TreeView) SubTree(key []byte, c *SubTreeConfig) (*TreeView, error) {
	return v.subTree(c.treeConfig(key))
}

type TreeUpdate struct {
	tx        db.WriteTx
	txTree    db.WriteTx
	dirtyTree bool
	tree      *tree.Tree
	openSubs  map[string]*TreeUpdate
	cfg       *treeConfig
}

func (u *TreeUpdate) Get(key []byte) ([]byte, error) {
	return u.tree.Get(u.txTree, key)
}

func (u *TreeUpdate) Iterate(callback func(key, value []byte) bool) error {
	return u.tree.Iterate(u.txTree, callback)
}

func (u *TreeUpdate) Root() ([]byte, error) {
	return u.tree.Root(u.txTree)
}

func (u *TreeUpdate) Size() (uint64, error) {
	// NOTE: Tree.Size is currently unimplemented
	return u.tree.Size(u.txTree), nil
}

func (u *TreeUpdate) GenProof(key []byte) ([]byte, []byte, error) {
	return u.tree.GenProof(u.txTree, key)
}

// Unimplemented because arbo.Tree.Dump doesn't take db.ReadTx as input.
// func (u *TreeUpdate) Dump() ([]byte, error) {
// 	panic("TODO")
// }

func (u *TreeUpdate) NoState() Updater {
	return &txUpdater{
		tx: subWriteTx(u.tx, subKeyNoState),
	}
}

func (u *TreeUpdate) Add(key, value []byte) error {
	u.dirtyTree = true
	return u.tree.Add(u.txTree, key, value)
}

func (u *TreeUpdate) Set(key, value []byte) error {
	u.dirtyTree = true
	return u.tree.Set(u.txTree, key, value)
}

func (u *TreeUpdate) subTree(cfg *treeConfig) (treeUpdate *TreeUpdate, err error) {
	if treeUpdate, ok := u.openSubs[string(cfg.prefix)]; ok {
		return treeUpdate, nil
	}
	tx := subWriteTx(u.tx, join(subKeySubTree, cfg.prefix))
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
		tx:       tx,
		txTree:   txTree,
		tree:     tree,
		openSubs: make(map[string]*TreeUpdate),
		cfg:      cfg,
	}
	u.openSubs[string(cfg.prefix)] = treeUpdate
	return treeUpdate, nil
}

func (u *TreeUpdate) SubTreeSingle(c *SubTreeSingleConfig) (*TreeUpdate, error) {
	return u.subTree(c.treeConfig())
}

func (u *TreeUpdate) SubTree(key []byte, c *SubTreeConfig) (*TreeUpdate, error) {
	return u.subTree(c.treeConfig(key))
}

type TreeTx struct {
	TreeUpdate
}

func (t *TreeTx) Commit() error {
	// TODO: Propagate roots of subtrees to parents leaves
	t.openSubs = make(map[string]*TreeUpdate)
	version, err := getVersion(t.tx)
	if err != nil {
		return err
	}
	root, err := t.tree.Root(t.txTree)
	if err != nil {
		return err
	}
	if err := setVersionRoot(t.tx, version+1, root); err != nil {
		return err
	}
	return t.tx.Commit()
}

func (t *TreeTx) Discard() {
	t.tx.Discard()
}
