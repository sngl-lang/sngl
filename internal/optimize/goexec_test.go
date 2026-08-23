//go:build !js

package optimize

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

const purepkgPath = "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"

func projectDir() string {
	// Walk up from the test file to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func purepkgFunc(name string, params []*ir.Param, ret *ir.Type) *ir.Func {
	return &ir.Func{
		Name:       name,
		NativeName: "purepkg." + name,
		NativePkg:  "purepkg",
		Purity:     ir.PurityPure,
		Params:     params,
		Return:     ret,
	}
}

var (
	fnDouble   = purepkgFunc("Double", []*ir.Param{{Name: "x", Type: ir.TypInt}}, ir.TypInt)
	fnGreet    = purepkgFunc("Greet", []*ir.Param{{Name: "name", Type: ir.TypString}}, ir.TypString)
	fnGetItems = purepkgFunc("GetItems", nil, &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypDyn}})
	fnBoom     = purepkgFunc("Boom", nil, ir.TypString)
	fnJoin     = purepkgFunc("Join", []*ir.Param{
		{Name: "parts", Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}}},
		{Name: "sep", Type: ir.TypString},
	}, ir.TypString)
)

// purepkgCtx is an evalCtx whose go:// import is the purepkg test package,
// with a fresh request set.
func purepkgCtx(dir string) *evalCtx {
	return &evalCtx{
		dir:    dir,
		native: &nativeEval{},
		nativeImports: map[string]*ir.NativeImport{"purepkg": {
			ImportPath: purepkgPath,
			Funcs:      []*ir.Func{fnDouble, fnGreet, fnGetItems, fnBoom, fnJoin, purepkgFunc("Nothing", nil, nil)},
		}},
		nativeSchemes: map[string]string{"purepkg": "go"},
	}
}

// evalNow requests every call, runs the one batch they produce, and returns
// what each request resolved to — the round loop in miniature.
func evalNow(t *testing.T, ctx *evalCtx, calls ...struct {
	fn   *ir.Func
	args []any
}) []constResult {
	t.Helper()
	for _, c := range calls {
		if _, state, err := requestPureGoFunc(ctx, "go", purepkgPath, c.fn, c.args); state == nativeReady || err != nil {
			t.Logf("%s resolved before the batch ran: %v", c.fn.NativeName, err)
		}
	}
	if len(ctx.native.order) > 0 {
		runNativeRequests(ctx.dir, ctx.native.order)
	}
	out := make([]constResult, len(calls))
	for i, c := range calls {
		v, state, err := requestPureGoFunc(ctx, "go", purepkgPath, c.fn, c.args)
		if state == nativePending {
			t.Fatalf("%s still pending after its batch ran", c.fn.NativeName)
		}
		out[i] = constResult{val: v, err: err}
	}
	return out
}

type call = struct {
	fn   *ir.Func
	args []any
}

// One batch answers every pending call, and a panicking one costs only its own
// value.
func TestBatchEvaluation(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	ctx := purepkgCtx(dir)
	got := evalNow(t, ctx,
		call{fnDouble, []any{5}},
		call{fnGreet, []any{"world"}},
		call{fnGetItems, nil},
		call{fnJoin, []any{[]any{"a", "b"}, "-"}},
		call{fnBoom, nil},
	)

	if got[0].err != nil || got[0].val != 10 {
		t.Errorf("Double(5) = %v, %v; want 10", got[0].val, got[0].err)
	}
	if got[1].err != nil || got[1].val != "Hello, world!" {
		t.Errorf("Greet(world) = %v, %v; want %q", got[1].val, got[1].err, "Hello, world!")
	}
	if got[3].err != nil || got[3].val != "a-b" {
		t.Errorf("Join([a b], -) = %v, %v; want %q", got[3].val, got[3].err, "a-b")
	}
	if got[4].err == nil {
		t.Errorf("Boom() = %v; want an error", got[4].val)
	}

	items, ok := got[2].val.([]any)
	if !ok {
		t.Fatalf("GetItems() = %T (%v), want []any", got[2].val, got[2].err)
	}
	if len(items) != 2 {
		t.Fatalf("GetItems() returned %d items, want 2", len(items))
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %T, want map[string]any", items[0])
	}
	if first["name"] != "alpha" {
		t.Errorf("item name = %v, want alpha", first["name"])
	}
	if first["value"] != 1 {
		t.Errorf("item value = %v (%T), want int 1", first["value"], first["value"])
	}
}

// A cached value is answered without a batch: the second request must resolve
// with no pending work at all.
func TestCachedCallNeedsNoBatch(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	evalNow(t, purepkgCtx(dir), call{fnGreet, []any{"cache"}})

	ctx := purepkgCtx(dir)
	v, state, err := requestPureGoFunc(ctx, "go", purepkgPath, fnGreet, []any{"cache"})
	if state != nativeReady || err != nil {
		t.Fatalf("cached call not ready: state=%v err=%v", state, err)
	}
	if v != "Hello, cache!" {
		t.Errorf("got %v", v)
	}
	if len(ctx.native.order) != 0 {
		t.Errorf("a cached call still requested a batch")
	}
}

// Argument lists that differ only in where the split falls must not share a
// key: the old "%v of []any" key rendered both of these as "[a b]".
func TestRequestKeyDoesNotCollide(t *testing.T) {
	one, _, err := renderGoArgs(purepkgFunc("F", []*ir.Param{
		{Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}}},
	}, ir.TypString), []any{[]any{"a b"}})
	if err != nil {
		t.Fatal(err)
	}
	two, _, err := renderGoArgs(purepkgFunc("F", []*ir.Param{
		{Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}}},
	}, ir.TypString), []any{[]any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if k1, k2 := requestKey("p", "p.F", one), requestKey("p", "p.F", two); k1 == k2 {
		t.Errorf("[\"a b\"] and [\"a\",\"b\"] share key %s", k1)
	}
}

// Arguments are rendered by the Go language codegen, so a list argument is a
// typed Go slice literal rather than a []any.
func TestRenderGoArgs(t *testing.T) {
	args, _, err := renderGoArgs(fnJoin, []any{[]any{"a", "b"}, "-"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`[]string{"a", "b"}`, `"-"`}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg %d = %s, want %s", i, args[i], want[i])
		}
	}
}

// A scheme the generated program cannot link fails immediately instead of
// poisoning the batch with a call site that will not compile.
func TestNonGoSchemeFails(t *testing.T) {
	ctx := purepkgCtx(projectDir())
	_, state, err := requestPureGoFunc(ctx, "js", purepkgPath, fnGreet, []any{"x"})
	if state != nativeFailed || err == nil {
		t.Fatalf("js:// call accepted: state=%v err=%v", state, err)
	}
	if len(ctx.native.order) != 0 {
		t.Error("js:// call was added to the batch")
	}
}

// A call whose arguments come from another compile-time call cannot be known
// in the first round: the folder has to come back for it.
func TestNestedCallTakesASecondRound(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	mkConst := func(name string, arg ir.Expr) *ir.Var {
		return &ir.Var{
			Name:    name,
			Type:    ir.TypInt,
			IsConst: true,
			Init: &ir.Call{
				AST: &ast.CallExpr{Func: &ast.SelectExpr{
					Operand: &ast.IdentExpr{Name: "purepkg"},
					Field:   "Double",
				}},
				Type: ir.TypInt,
				Args: []ir.CallArg{{Value: arg}},
			},
		}
	}
	inner := mkConst("inner", &ir.Literal{Type: ir.TypInt, Raw: "13"})
	outer := mkConst("outer", &ir.Ident{Name: "inner", Sym: inner, Type: ir.TypInt})
	pkg := &ir.Package{
		Consts: []*ir.Var{inner, outer},
		Imports: []*ir.Import{{
			Path:  "go://" + purepkgPath,
			Alias: "purepkg",
			Native: &ir.NativeImport{
				ImportPath: purepkgPath,
				Funcs:      []*ir.Func{fnDouble},
			},
		}},
	}

	if err := Optimize(pkg, &Config{Platform: "html", Language: "none", Dir: dir}); err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	lit, ok := outer.Init.(*ir.Literal)
	if !ok {
		t.Fatalf("nested const did not fold: %T", outer.Init)
	}
	if lit.Raw != "52" {
		t.Errorf("Double(Double(13)) folded to %q, want 52", lit.Raw)
	}
}

// The generated program wraps each call so one panic loses one value, and
// keeps the results out of stdout.
func TestConstEvalSource(t *testing.T) {
	src := constEvalSource([]*nativeRequest{{
		key: "cdead", importPath: purepkgPath, nativeType: "purepkg.Greet",
		funcName: "Greet", args: []string{`"x"`}, hasResult: true,
	}})
	for _, want := range []string{
		`p0 "` + purepkgPath + `"`,
		`const key = "cdead"`,
		"recover()",
		`consteval.Emit(key, p0.Greet("x"))`,
		"consteval.Flush()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated program missing %q:\n%s", want, src)
		}
	}
}

// A pure func with no result is still called; the fold takes null for its
// value rather than failing (which on a target that cannot call go:// at
// runtime would abort the build).
func TestVoidCallFoldsToNull(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	fn := purepkgFunc("Nothing", nil, nil)
	got := evalNow(t, purepkgCtx(dir), call{fn, nil})
	if got[0].err != nil || got[0].val != nil {
		t.Errorf("Nothing() = %v, %v; want nil, nil", got[0].val, got[0].err)
	}
}

// The generated package's path is what Go's build cache keys on, so the same
// program must land on the same path — that is the difference between a 0.1s
// cached link and a 1.0s relink on every round.
func TestConstEvalSrcDirIsStableAndOwned(t *testing.T) {
	dir := t.TempDir()
	const src = "package main\n"

	first, canonical, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	if !canonical {
		t.Error("the first caller did not get the canonical name")
	}
	// While the directory is held, a second caller must not use it: it would
	// be building into someone else's package, and the owner deletes it.
	held, canonical, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	if held == first {
		t.Error("a held directory was handed out twice")
	}
	if canonical {
		t.Error("a fallback directory claimed the canonical name; its binary would be cached and never reused")
	}
	os.RemoveAll(held)
	os.RemoveAll(first) // the owner removes it when the round is done

	again, _, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("the same program got two paths: %s then %s", first, again)
	}
	os.RemoveAll(again)

	other, _, err := constEvalSrcDir(dir, src+"// different\n")
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("two different programs share a path")
	}
}

// A crashed build leaves its directory behind; nothing may own one for longer
// than evalTimeout, so a later compile reclaims it instead of falling back to
// the slow path forever.
func TestConstEvalSrcDirReclaimsStale(t *testing.T) {
	dir := t.TempDir()
	const src = "package main\n"
	stale, _, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * evalTimeout)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	got, _, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != stale {
		t.Errorf("a stale directory was not reclaimed: got %s, want %s", got, stale)
	}
}

// The gate that decides whether to run the round loop at all: true while a
// native call is still in the IR, false once folding has replaced it with a
// literal. The second half is what keeps the post-lower Optimize from cloning
// an expanded IR to discover nothing.
func TestUnresolvedNativeCallGate(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	c := &ir.Var{
		Name:    "x",
		Type:    ir.TypInt,
		IsConst: true,
		Init: &ir.Call{
			AST: &ast.CallExpr{Func: &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: "purepkg"},
				Field:   "Double",
			}},
			Type: ir.TypInt,
			Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypInt, Raw: "21"}}},
		},
	}
	pkg := &ir.Package{
		Consts: []*ir.Var{c},
		Imports: []*ir.Import{{
			Path:  "go://" + purepkgPath,
			Alias: "purepkg",
			Native: &ir.NativeImport{
				ImportPath: purepkgPath,
				Funcs:      []*ir.Func{fnDouble},
			},
		}},
	}
	cfg := &Config{Platform: "html", Language: "none", Dir: dir}
	if !hasUnresolvedNativeCall(pkg, cfg) {
		t.Fatal("a pure go:// call in a const was not seen")
	}
	if err := Optimize(pkg, cfg); err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if hasUnresolvedNativeCall(pkg, cfg) {
		t.Error("the call folded to a literal but the gate still reports work to do")
	}
}

// mkCacheEntry writes a cache entry of the given size, last used the given
// duration ago.
func mkCacheEntry(t *testing.T, root, name string, size int, usedAgo time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eval"), make([]byte, size), 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-usedAgo)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Each entry links the whole compiler, so the cache is bounded by bytes and
// evicts least recently used. What grows it is argument changes, not time, so
// the age floor alone cannot hold it down.
func TestPruneConstEvalBinsEvictsLRU(t *testing.T) {
	root := t.TempDir()
	// Every entry is out of the grace window, so all are eviction candidates.
	oldest := mkCacheEntry(t, root, "a", 400, 30*binGrace)
	middle := mkCacheEntry(t, root, "b", 400, 20*binGrace)
	newest := mkCacheEntry(t, root, "c", 400, 10*binGrace)
	ancient := mkCacheEntry(t, root, "d", 1, 2*binMaxAge)

	pruneConstEvalBins(root, 900)

	if exists(ancient) {
		t.Error("an entry past binMaxAge survived the age floor")
	}
	if exists(oldest) {
		t.Error("the least recently used entry was not evicted")
	}
	if !exists(newest) {
		t.Error("the most recently used entry was evicted")
	}
	if !exists(middle) {
		t.Error("eviction went past the budget: 800 bytes of 900 is under it")
	}
}

// An entry another compile may be executing right now is not evicted, even
// when that leaves the cache over budget: deleting it would take the binary
// out from under a running build.
func TestPruneConstEvalBinsSparesLiveEntries(t *testing.T) {
	root := t.TempDir()
	live := mkCacheEntry(t, root, "live", 400, binGrace/2)
	cold := mkCacheEntry(t, root, "cold", 400, 30*binGrace)

	pruneConstEvalBins(root, 100)

	if !exists(live) {
		t.Error("an entry inside the grace window was evicted")
	}
	if exists(cold) {
		t.Error("a cold entry survived while the cache was over budget")
	}
}

// Every use touches the entry, a cache hit included — otherwise LRU evicts the
// binary a repeated build keeps reusing, since an unchanged program is never
// relinked and its file mtime never moves.
func TestConstEvalBinPathTouchesOnUse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first, err := constEvalBinPath(".sngl-consteval-touchtest")
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Dir(first)
	old := time.Now().Add(-42 * time.Hour)
	if err := os.Chtimes(binDir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := constEvalBinPath(".sngl-consteval-touchtest"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binDir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("a cache hit did not mark the entry used: mtime is %s old", time.Since(info.ModTime()))
	}
	// The name is the generated package's, minus the dot that hides it inside
	// the project. A fallback (EEXIST) run's directory carries a suffix and so
	// lands in the same root under the same policy.
	if filepath.Base(binDir) != "sngl-consteval-touchtest" {
		t.Errorf("cache entry named %q", filepath.Base(binDir))
	}
}

// The retry that covers the eviction race turns on telling a missing binary
// from a program that ran and failed.
func TestRunConstEvalMissingBinaryIsNotExist(t *testing.T) {
	err := runConstEval(t.TempDir(), filepath.Join(t.TempDir(), "gone"), filepath.Join(t.TempDir(), "out.sngl"))
	if err == nil {
		t.Fatal("executing a missing binary succeeded")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing binary reported %v, which the rebuild-once path cannot recognise", err)
	}
}
