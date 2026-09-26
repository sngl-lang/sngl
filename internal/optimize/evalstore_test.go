//go:build !js

package optimize

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/gencache"
	_ "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/storeprobe"
	"git.duckfam.us/jonathan/sngl/ir"
)

var (
	fnRunStamp = purepkgFunc("RunStamp", []*ir.Param{{Name: "tag", Type: ir.TypString}}, ir.TypString)
	fnViaStore = func() *ir.Func {
		f := purepkgFunc("ViaStore", []*ir.Param{{Name: "path", Type: ir.TypString}}, ir.TypString)
		f.HasErrorReturn = true
		return f
	}()
)

// evalIn is one compilation evaluating one call against store: a fresh
// in-memory cache, so whatever answers it came from outside the compilation.
func evalIn(t *testing.T, store *gencache.Store, dir, importPath string, fn *ir.Func, args ...any) string {
	t.Helper()
	ctx := purepkgCtx(dir)
	ctx.cache.gen = store
	if _, _, err := requestPureNativeFunc(ctx, "go", importPath, fn, args); err != nil {
		t.Fatal(err)
	}
	if errs := runNativeRequests(ctx.evalCache(), dir, ir.IndexNativeDecls(ctx.pkg), ctx.native.order); len(errs) > 0 {
		t.Fatal(errs)
	}
	v, _, err := requestPureNativeFunc(nextRound(ctx), "go", importPath, fn, args)
	if err != nil {
		t.Fatal(err)
	}
	return litRaw(v)
}

// A call whose inputs have not changed is not evaluated again: a later
// compilation takes the stored value. RunStamp returns the moment it ran, so
// two equal answers mean the second came from the store.
func TestUnchangedCallIsNotEvaluatedAgain(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	store := t.TempDir()
	tag := t.Name() + time.Now().Format(time.RFC3339Nano)
	first := evalIn(t, gencache.Open(store), dir, purepkgPath, fnRunStamp, tag)
	if second := evalIn(t, gencache.Open(store), dir, purepkgPath, fnRunStamp, tag); second != first {
		t.Errorf("an unchanged call was evaluated again:\nfirst  %s\nsecond %s", first, second)
	}
}

// A store that is off keeps nothing, so every compilation evaluates.
func TestOffStoreEvaluatesEveryTime(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	tag := t.Name() + time.Now().Format(time.RFC3339Nano)
	first := evalIn(t, gencache.Open(""), dir, purepkgPath, fnRunStamp, tag)
	if second := evalIn(t, gencache.Open(""), dir, purepkgPath, fnRunStamp, tag); second == first {
		t.Errorf("with the store off, the second compilation got the first's value: %s", first)
	}
}

// Editing the evaluated function's source invalidates the stored value. The
// package is written for the test, under testdata so the module resolves it
// and `./...` does not, and backdated so the edit is not too recent to store.
func TestSourceEditInvalidates(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	pkgDir, err := os.MkdirTemp("testdata", "srcedit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(pkgDir) })
	write := func(v string) {
		t.Helper()
		path := filepath.Join(pkgDir, "v.go")
		src := "package srcedit\n\n//sngl:pure\nfunc Version() string { return \"" + v + "\" }\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		os.Chtimes(path, old, old)
	}
	importPath := "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/" + filepath.Base(pkgDir)
	fn := &ir.Func{
		Name:    "Version",
		Foreign: ir.Foreign{Name: "srcedit.Version", Path: "srcedit"},
		Purity:  ir.PurityPure,
		Return:  ir.TypString,
	}
	store := t.TempDir()

	write("one")
	if got := evalIn(t, gencache.Open(store), dir, importPath, fn); got != "one" {
		t.Fatalf("Version() = %q, want \"one\"", got)
	}
	write("two, edited")
	if got := evalIn(t, gencache.Open(store), dir, importPath, fn); got != "two, edited" {
		t.Errorf("after editing the source, Version() = %q: the stored value survived the edit", got)
	}
}

// A value computed from a generated file the evaluator read through the store
// -- as the docs site reads gtk4's widget declarations -- records that file,
// so regenerating it invalidates the value. Nothing in the Go source changes,
// so without the record the value would be replayed.
func TestValueFollowsGeneratedFilesTheEvaluatorRead(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	path := filepath.Join(t.TempDir(), "input")
	os.WriteFile(path, []byte("one"), 0o644)
	store := t.TempDir()
	if got := evalIn(t, gencache.Open(store), dir, purepkgPath, fnViaStore, path); got != `"one"` {
		t.Fatalf("ViaStore = %s, want \"one\"", got)
	}
	os.WriteFile(path, []byte("two"), 0o644)
	if got := evalIn(t, gencache.Open(store), dir, purepkgPath, fnViaStore, path); got != `"two"` {
		t.Errorf("after the file the evaluator read changed, ViaStore = %s: the stored value survived", got)
	}
}
