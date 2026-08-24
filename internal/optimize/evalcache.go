package optimize

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// constResult is a finished compile-time evaluation: the checked IR of the
// value, or the reason there will never be one.
type constResult struct {
	expr ir.Expr
	err  error
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
	m sync.Map // string → constResult
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

// evalCache is the cache this pass folds against. Optimize always supplies
// one; a context built directly (a scan, a unit test) gets its own, so that a
// missing cache costs a repeat rather than a nil dereference.
func (c *evalCtx) evalCache() *EvalCache {
	if c.cache == nil {
		c.cache = NewEvalCache()
	}
	return c.cache
}
