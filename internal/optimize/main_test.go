//go:build !js

package optimize

import (
	"os"
	"path/filepath"
	"testing"

	"duckfam.us/sngl/internal/gencache"
)

// TestMain moves the evaluator binary cache out of the developer's real cache:
// every test that evaluates a go: call generates its own one-call program,
// so a package run would otherwise leave a few hundred megabytes there, and on
// CI in a cache nothing ever reuses.
//
// The location is fixed rather than per-run, and deliberately not removed
// afterwards: a fresh directory each run means every test relinks its own
// binary. The cache's own size and age bounds keep it in check.
//
// The generated-file store is the opposite: a fresh one per run, so that a
// test asserting a producer ran is not answered by the run before it.
func TestMain(m *testing.M) {
	os.Setenv(binCacheEnv, filepath.Join(os.TempDir(), "sngl-consteval-test"))
	store, err := os.MkdirTemp("", "sngl-gencache-test-*")
	if err != nil {
		panic(err)
	}
	os.Setenv(gencache.DirEnv, store)
	code := m.Run()
	os.RemoveAll(store)
	os.Exit(code)
}
