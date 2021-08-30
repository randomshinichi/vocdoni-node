package statedb

type TreeType string

const (
	TreeTypeSha256   TreeType = "sha256"
	TreeTypePoseidon TreeType = "poseidon"
	TreeTypeBlake2b  TreeType = "blake2b"
)

type Viewer interface {
	Get(key []byte) ([]byte, error)
}

type Updater interface {
	Viewer
	Add(key, value []byte) error
	Set(key, value []byte) error
}

type GetRootFn func(value []byte) []byte
type SetRootFn func(value []byte, root []byte) []byte

type SubTreeConfig struct {
	typ               TreeType
	kindID            []byte
	parentLeafGetRoot GetRootFn
	parentLeafSetRoot SetRootFn
	maxLevels         int
}

func NewSubTreeConfig(typ TreeType, kindID []byte,
	getRoot GetRootFn, setRoot SetRootFn, maxLevels int) *SubTreeConfig {
	return &SubTreeConfig{
		typ:               typ,
		kindID:            kindID,
		parentLeafGetRoot: getRoot,
		parentLeafSetRoot: setRoot,
		maxLevels:         maxLevels,
	}
}

type SubTreeSingleConfig struct {
	key []byte
	cfg SubTreeConfig
}

func NewSubTreeSingleConfig(typ TreeType, kindID []byte,
	getRoot GetRootFn, setRoot SetRootFn, key []byte) *SubTreeSingleConfig {
	cfg := NewSubTreeConfig(typ, kindID, getRoot, setRoot, maxLevels)
	return &SubTreeSingleConfig{
		key: key,
		cfg: *cfg,
	}
}

func (c *SubTreeSingleConfig) Key() []byte {
	return c.key
}

var mainTree = NewSubTreeSingleConfig(TreeTypeSha256, nil, nil, nil)

type StateDB struct {
	db Database
}

func NewStateDB(db Database) *StateDB {
	return &StateDB{
		db: db,
	}
}

// func NewStateDBBadger(opts badgerdb.Options) (*StateDB, error) {
// 	db, err := badgerdb.New(opts)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return NewStateDB(db), nil
// }

func (s *StateDB) Version() uint64 {
	panic("TODO")
}

func (s *StateDB) VersionRoot(v uint64) ([]byte, error) {
	panic("TODO")
}

func (s *StateDB) BeginTx() (*TreeTx, error) {
	panic("TODO")
}

func (s *StateDB) TreeView() (*TreeView, error) {
	panic("TODO")
}

type TreeView struct {
}

func (v *TreeView) NoState() Viewer {
	panic("TODO")
}

func (v *TreeView) Get(key []byte) ([]byte, error) {
	panic("TODO")
}

func (v *TreeView) Iterate(callback func(key, value []byte) bool) error {
	panic("TODO")
}

func (v *TreeView) Root() ([]byte, error) {
	panic("TODO")
}

func (v *TreeView) Size() (uint64, error) {
	panic("TODO")
}

func (v *TreeView) GenProof(key []byte) ([]byte, error) {
	panic("TODO")
}

func (v *TreeView) Dump() ([]byte, error) {
	panic("TODO")
}

func (v *TreeView) SubTreeSingle(c *SubTreeSingleConfig) (*TreeView, error) {
	panic("TODO")
}

func (v *TreeView) SubTree(key []byte, c *SubTreeConfig) (*TreeView, error) {
	panic("TODO")
}

type TreeUpdate struct {
}

func (v *TreeUpdate) Get(key []byte) ([]byte, error) {
	panic("TODO")
}

func (v *TreeUpdate) Iterate(callback func(key, value []byte) bool) error {
	panic("TODO")
}

func (v *TreeUpdate) Root() ([]byte, error) {
	panic("TODO")
}

func (v *TreeUpdate) Size() (uint64, error) {
	panic("TODO")
}

func (v *TreeUpdate) GenProof(key []byte) ([]byte, error) {
	panic("TODO")
}

func (v *TreeUpdate) Dump() ([]byte, error) {
	panic("TODO")
}

func (u *TreeUpdate) NoState() Updater {
	panic("TODO")
}

func (u *TreeUpdate) Add(key, value []byte) error {
	panic("TODO")
}

func (u *TreeUpdate) Set(key, value []byte) error {
	panic("TODO")
}

func (u *TreeUpdate) SubTreeSingle(c *SubTreeSingleConfig) (*TreeUpdate, error) {
	panic("TODO")
}

func (v *TreeUpdate) SubTree(key []byte, c *SubTreeConfig) (*TreeView, error) {
	panic("TODO")
}

type TreeTx struct {
	TreeUpdate
}

func (t *TreeTx) Commit() error {
	panic("TODO")
}

func (t *TreeTx) Discard() error {
	panic("TODO")
}
