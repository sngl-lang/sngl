package optimize

import (
	"io/fs"
	"os"
	"path"
	"sync"

	"git.duckfam.us/jonathan/sngl/internal/asset"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/ir"
)

// constResult is a finished compile-time evaluation: the checked IR of the
// value, or the reason there will never be one.
type constResult struct {
	expr ir.Expr
	// value is a producer's answer (produce.go), held as the interpreter's
	// value rather than IR: it is what the fold that asked goes on with.
	value any
	err   error
}

// EvalCache memoizes compile-time evaluation across the rounds and the Optimize
// calls of one compilation — the pipeline optimizes twice per target, and a
// build with several targets evaluates the same call for each. The key is
// derived from the function and its rendered arguments, so two calls collide
// only when they really are the same call.
//
// It is scoped to a compilation and not to the process because the key says
// nothing about the *body* of the evaluated function: a server that recompiles
// (sngl preview) must re-evaluate after the Go source is edited. Config.Cache
// carries it; a Config that names none gets one of its own.
type EvalCache struct {
	m     sync.Map // string → constResult
	files sync.Map // string → fileResult
	// gen is the store a go: value is kept in between compilations, keyed on
	// what the value was built from rather than on the call alone. Nil means
	// the process's default store.
	gen *gencache.Store
}

// fileResult is one file: import's read: the bytes, and the content-hashed
// name they produce. Both are a function of the file alone, so a second call
// naming it repeats the read and the SHA of every byte -- which the docs site
// did eight times over a 96MB playground wasm, once per fold, the two probe
// rounds and the second Optimize included.
type fileResult struct {
	data   []byte
	hashed string
	err    error
}

// NewEvalCache returns a cache to share across the targets of one compilation.
func NewEvalCache() *EvalCache { return &EvalCache{} }

func (c *EvalCache) load(key string) (constResult, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return constResult{}, false
	}
	return v.(constResult), true
}

func (c *EvalCache) store(key string, res constResult) { c.m.Store(key, res) }

// file reads one file: asset, once per compilation. It shares the cache's
// lifetime rather than the process's for the reason stated above: a server
// that recompiles must see an edited file, and it gets a new cache to do it.
//
// The bytes are shared, not copied, so the FileAsset every call site appends
// names one buffer instead of one per call.
func (c *EvalCache) file(dirPath, filename string) fileResult {
	key := "file\x00" + dirPath + "\x00" + filename
	if v, ok := c.files.Load(key); ok {
		return v.(fileResult)
	}
	var res fileResult
	// Through an os.Root, so a symlink in the asset directory cannot hand a
	// build a file from outside it.
	if root, err := os.OpenRoot(dirPath); err != nil {
		res.err = err
	} else {
		res.data, res.err = fs.ReadFile(root.FS(), filename)
		root.Close()
	}
	if res.err == nil {
		res.hashed = asset.HashedName(path.Base(filename), res.data)
	}
	c.files.Store(key, res)
	return res
}

// genStore is the store go: values are kept in between compilations.
func (c *EvalCache) genStore() *gencache.Store {
	if c.gen == nil {
		return gencache.Default()
	}
	return c.gen
}

// evalCache is the cache this pass folds against. Optimize always supplies
// one; a context built directly (a scan, a unit test) gets its own, so that a
// missing cache costs a repeat rather than a nil dereference.
func (c *evalCtx) evalCache() *EvalCache {
	if c.cache == nil {
		c.cache = NewEvalCache()
	}
	return c.cache
}
