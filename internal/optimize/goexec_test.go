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
		Name:    name,
		Foreign: ir.Foreign{Name: "purepkg." + name, Path: "purepkg"},
		Purity:  ir.PurityPure,
		Params:  params,
		Return:  ret,
	}
}

var (
	fnDouble = purepkgFunc("Double", []*ir.Param{{Name: "x", Type: ir.TypInt}}, ir.TypInt)
	fnGreet  = purepkgFunc("Greet", []*ir.Param{{Name: "name", Type: ir.TypString}}, ir.TypString)
	fnBoom   = purepkgFunc("Boom", nil, ir.TypString)
	fnJoin   = purepkgFunc("Join", []*ir.Param{
		{Name: "parts", Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}}},
		{Name: "sep", Type: ir.TypString},
	}, ir.TypString)
)

// purepkgCtx is an evalCtx whose go:// import is the purepkg test package,
// with a fresh request set and a fresh cache — one test's evaluations are not
// another's.
func purepkgCtx(dir string) *evalCtx {
	return &evalCtx{
		dir:    dir,
		cache:  NewEvalCache(),
		native: &nativeEval{},
		nativeImports: map[string]*ir.NativeImport{"purepkg": {
			ImportPath: purepkgPath,
			Funcs:      []*ir.Func{fnDouble, fnGreet, fnBoom, fnJoin, purepkgFunc("Nothing", nil, nil)},
		}},
		nativeSchemes: map[string]string{"purepkg": "go"},
		// pkg carries the real import, whose struct declarations are what a
		// value naming its own type resolves against; the hand-built
		// NativeImport above declares only funcs.
		pkg: purepkgPackage(),
	}
}

// purepkgPackage is a package importing purepkg through the real go://
// importer, or nil where the import does not resolve — the same condition
// importedFunc skips on.
func purepkgPackage() *ir.Package {
	ni, err := purepkgImport()
	if err != nil {
		return nil
	}
	return &ir.Package{Imports: []*ir.Import{{
		Path:   "go://" + purepkgPath,
		Alias:  "purepkg",
		Native: ni,
	}}}
}

// nextRound is the same compilation's next fold pass: a fresh request set over
// the cache the previous pass filled. Two purepkgCtx calls are two
// compilations, and a compilation's evaluated values are its own.
func nextRound(ctx *evalCtx) *evalCtx {
	c := *ctx
	c.native = &nativeEval{}
	return &c
}

// evalNow requests every call, runs the one batch they produce, and returns
// what each request resolved to — the round loop in miniature.
func evalNow(t *testing.T, ctx *evalCtx, calls ...struct {
	fn   *ir.Func
	args []any
}) []constResult {
	t.Helper()
	for _, c := range calls {
		if _, state, err := requestPureNativeFunc(ctx, "go", purepkgPath, c.fn, c.args); state == nativeReady || err != nil {
			t.Logf("%s resolved before the batch ran: %v", c.fn.Foreign.Name, err)
		}
	}
	if len(ctx.native.order) > 0 {
		runNativeRequests(ctx.evalCache(), ctx.dir, ir.IndexNativeDecls(ctx.pkg), ctx.native.order)
	}
	out := make([]constResult, len(calls))
	for i, c := range calls {
		v, state, err := requestPureNativeFunc(ctx, "go", purepkgPath, c.fn, c.args)
		if state == nativePending {
			t.Fatalf("%s still pending after its batch ran", c.fn.Foreign.Name)
		}
		out[i] = constResult{expr: v, err: err}
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
	// GetItems' return type comes from the real importer: the elements are a
	// declared struct, and that declaration is what names their fields.
	getItems := importedFunc(t, "GetItems")
	ctx := purepkgCtx(dir)
	got := evalNow(t, ctx,
		call{fnDouble, []any{5}},
		call{fnGreet, []any{"world"}},
		call{getItems, nil},
		call{fnJoin, []any{[]any{"a", "b"}, "-"}},
		call{fnBoom, nil},
	)

	if got[0].err != nil || litRaw(got[0].expr) != "10" {
		t.Errorf("Double(5) = %v, %v; want 10", got[0].expr, got[0].err)
	}
	if got[1].err != nil || litRaw(got[1].expr) != "Hello, world!" {
		t.Errorf("Greet(world) = %v, %v; want %q", got[1].expr, got[1].err, "Hello, world!")
	}
	if got[3].err != nil || litRaw(got[3].expr) != "a-b" {
		t.Errorf("Join([a b], -) = %v, %v; want %q", got[3].expr, got[3].err, "a-b")
	}
	if got[4].err == nil {
		t.Errorf("Boom() = %v; want an error", got[4].expr)
	}

	items, ok := got[2].expr.(*ir.ListLit)
	if !ok {
		t.Fatalf("GetItems() = %T (%v), want a list literal", got[2].expr, got[2].err)
	}
	if len(items.Elems) != 2 {
		t.Fatalf("GetItems() returned %d items, want 2", len(items.Elems))
	}
	first, ok := items.Elems[0].(*ir.StructLit)
	if !ok {
		t.Fatalf("item = %T, want a struct literal", items.Elems[0])
	}
	if got := fieldRaw(first, "name"); got != "alpha" {
		t.Errorf("item name = %q, want alpha", got)
	}
	if got := fieldRaw(first, "value"); got != "1" {
		t.Errorf("item value = %q, want 1", got)
	}
}

// litRaw is the raw text of a literal expression, or "" for anything else.
func litRaw(e ir.Expr) string {
	if lit, ok := e.(*ir.Literal); ok {
		return lit.Raw
	}
	return ""
}

// fieldRaw is the raw text of a named field's literal value.
func fieldRaw(s *ir.StructLit, name string) string {
	for _, f := range s.Fields {
		if f.Name == name {
			return litRaw(f.Value)
		}
	}
	return ""
}

// A cached value is answered without a batch: the second request must resolve
// with no pending work at all.
func TestCachedCallNeedsNoBatch(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	first := purepkgCtx(dir)
	evalNow(t, first, call{fnGreet, []any{"cache"}})

	ctx := nextRound(first)
	v, state, err := requestPureNativeFunc(ctx, "go", purepkgPath, fnGreet, []any{"cache"})
	if state != nativeReady || err != nil {
		t.Fatalf("cached call not ready: state=%v err=%v", state, err)
	}
	if litRaw(v) != "Hello, cache!" {
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
	if k1, k2 := requestKey("go", "p", "p.F", one), requestKey("go", "p", "p.F", two); k1 == k2 {
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

// A scheme no runner claims fails immediately instead of poisoning the batch
// with a call site that will not compile.
func TestUnrunnableSchemeFails(t *testing.T) {
	ctx := purepkgCtx(projectDir())
	_, state, err := requestPureNativeFunc(ctx, "c", purepkgPath, fnGreet, []any{"x"})
	if state != nativeFailed || err == nil {
		t.Fatalf("c:// call accepted: state=%v err=%v", state, err)
	}
	if len(ctx.native.order) != 0 {
		t.Error("c:// call was added to the batch")
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
		funcName: "Greet", args: []string{`"x"`}, ret: ir.TypString,
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
	// The encoder writes `null`, which checks to the same thing source `null`
	// does: a reference to the predeclared const, whose type is null.
	if got[0].err != nil || got[0].expr == nil || got[0].expr.ExprType().Kind != ir.TypeNull {
		t.Errorf("Nothing() = %v, %v; want a null-typed expression", got[0].expr, got[0].err)
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
// than ownerLifetime, so a later compile reclaims it instead of falling back to
// the slow path forever.
func TestConstEvalSrcDirReclaimsStale(t *testing.T) {
	dir := t.TempDir()
	const src = "package main\n"
	stale, _, err := constEvalSrcDir(dir, src)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * ownerLifetime)
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

	// The other half of the bound: an owner that has spent one evalTimeout in
	// its build and is now in its run still holds this directory, and deleting
	// it takes the source out from under a live compile.
	live, _, err := constEvalSrcDir(dir, src+"// live\n")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-2 * evalTimeout)
	if err := os.Chtimes(live, when, when); err != nil {
		t.Fatal(err)
	}
	if got, _, err := constEvalSrcDir(dir, src+"// live\n"); err != nil {
		t.Fatal(err)
	} else if got == live {
		t.Error("a directory a live owner may still hold was reclaimed")
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

// One batch, and the values in it stand or fall one at a time. Two of these
// calls cannot produce a value at all — a self-referential pointer and a
// uint64 that does not fit the int the importer declared — and the fold of the
// third must not notice.
func TestOneBadValueDoesNotBlankTheBatch(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	cycle, huge := importedFunc(t, "Cycle"), importedFunc(t, "Huge")
	got := evalNow(t, purepkgCtx(dir),
		call{cycle, nil},
		call{huge, nil},
		call{importedFunc(t, "Bytes"), nil},
		call{importedFunc(t, "Ratio"), nil},
		call{fnGreet, []any{"batch"}},
	)

	if got[0].err == nil {
		t.Errorf("Cycle() = %v; want an error, since a value containing itself has no SNGL form", got[0].expr)
	}
	if got[1].err == nil {
		t.Errorf("Huge() = %v; want an error: %v does not fit %v", got[1].expr, "math.MaxUint64", huge.Return)
	}
	// []byte is list<int> on the import side, so the elements are what checks.
	list, ok := got[2].expr.(*ir.ListLit)
	if !ok {
		t.Fatalf("Bytes() = %T (%v), want a list literal", got[2].expr, got[2].err)
	}
	if len(list.Elems) != 2 || litRaw(list.Elems[0]) != "104" || litRaw(list.Elems[1]) != "105" {
		t.Errorf("Bytes() folded to %v, want [104, 105]", list.Elems)
	}
	// A float32 encoded at 64-bit precision reads back as 0.10000000149011612.
	if got[3].err != nil || litRaw(got[3].expr) != "0.1" {
		t.Errorf("Ratio() = %v, %v; want 0.1", litRaw(got[3].expr), got[3].err)
	}
	if got[4].err != nil || litRaw(got[4].expr) != "Hello, batch!" {
		t.Errorf("Greet(batch) = %v, %v; want the greeting — a failed key in the batch took it down",
			got[4].expr, got[4].err)
	}
}

// A struct-typed argument is refused before the batch is written. The Go
// translator spells the literal `Item{...}`, which names nothing in a program
// that reaches purepkg under an alias, so the generated program would not
// compile and every other value in the batch would go with it.
func TestStructArgIsRefused(t *testing.T) {
	f := importedFunc(t, "Describe")
	_, _, err := renderGoArgs(f, []any{map[string]any{"Name": "a", "Value": 1}})
	if err == nil {
		t.Fatal("a struct argument was rendered")
	}
	for _, want := range []string{"Describe", "Item"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

// The cache hands out a copy. Later phases mutate IR in place, so two call
// sites folding to one value must not be handed the same node.
func TestCachedValueIsNotShared(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	ctx := purepkgCtx(dir)
	got := evalNow(t, ctx, call{getItemsFn(t), nil})
	if got[0].err != nil {
		t.Fatalf("GetItems(): %v", got[0].err)
	}
	second, state, err := requestPureNativeFunc(nextRound(ctx), "go", purepkgPath, getItemsFn(t), nil)
	if state != nativeReady || err != nil {
		t.Fatalf("second request: state=%v err=%v", state, err)
	}
	if second == got[0].expr {
		t.Fatal("two call sites share one expression node")
	}
	first := got[0].expr.(*ir.ListLit)
	if second.(*ir.ListLit).Elems[0] == first.Elems[0] {
		t.Error("the copy shares its elements with the original")
	}
	first.Elems = nil // what a later phase's in-place edit looks like
	if len(second.(*ir.ListLit).Elems) != 2 {
		t.Error("editing one call site's value changed the other's")
	}
}

func getItemsFn(t *testing.T) *ir.Func {
	t.Helper()
	return importedFunc(t, "GetItems")
}

// A whole-batch failure — here a project directory the evaluator cannot be
// built in — is not an answer about any one call, so it must not be cached as
// one: the process goes on to optimize other targets, and one of them may
// succeed where this one could not.
func TestBatchFailureIsNotCached(t *testing.T) {
	req := &nativeRequest{
		key:        requestKey("go", "infra", "purepkg.Greet", []string{`"x"`}),
		scheme:     "go",
		importPath: purepkgPath,
		nativeType: "purepkg.Greet",
		funcName:   "Greet",
		args:       []string{`"x"`},
		ret:        ir.TypString,
	}
	cache := NewEvalCache()
	if err := runNativeRequests(cache, t.TempDir(), nil, []*nativeRequest{req}); err == nil {
		t.Fatal("the evaluator built in a directory with no module")
	}
	if _, cached := cache.load(req.key); cached {
		t.Error("a build failure was cached as this call's final answer")
	}
}

// A fatal fold error inside a nested scope — an inlined component body, a
// for-loop iteration — is the child context's, and the child is what folds the
// call that fails. Reporting it is the parent's job: this target cannot call
// go:// at runtime, so a dropped error is a page rendered with the value
// missing.
func TestNestedFoldErrorIsReported(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	fnBoomWith := purepkgFunc("BoomWith", []*ir.Param{{Name: "s", Type: ir.TypString}}, ir.TypString)
	boomWith := func(arg ir.Expr) *ir.Call {
		return &ir.Call{
			AST: &ast.CallExpr{Func: &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: "purepkg"},
				Field:   "BoomWith",
			}},
			Type: ir.TypString,
			Args: []ir.CallArg{{Value: arg}},
		}
	}
	mkPkg := func(inlined bool) *ir.Package {
		var body []ir.Stmt
		var comps []*ir.Component
		if inlined {
			// component wrapped(name string) { text(value = purepkg.BoomWith(name)) }
			// main { wrapped(name = "x") }
			sym := &ir.Param{Name: "name", Type: ir.TypString}
			comp := &ir.Component{
				Name:  "wrapped",
				Props: []*ir.Prop{{Name: "name", Type: ir.TypString, Sym: sym}},
				Body: []ir.Stmt{&ir.NodeInst{Name: "text", Props: []ir.Arg{{
					Name:  "value",
					Value: boomWith(&ir.Ident{Name: "name", Sym: sym, Type: ir.TypString}),
				}}}},
			}
			comps = append(comps, comp)
			body = []ir.Stmt{&ir.NodeInst{
				Name:      "wrapped",
				Component: comp,
				Props:     []ir.Arg{{Name: "name", Value: &ir.Literal{Type: ir.TypString, Raw: "x"}}},
			}}
		} else {
			// main { for name = ["x"] { text(value = purepkg.BoomWith(name)) } }
			loop := &ir.LoopVar{Name: "name", Type: ir.TypString}
			body = []ir.Stmt{&ir.For{
				Key:      "name",
				ElemType: ir.TypString,
				KeySym:   loop,
				Iter: &ir.ListLit{
					Type:  &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}},
					Elems: []ir.Expr{&ir.Literal{Type: ir.TypString, Raw: "x"}},
				},
				Body: []ir.Stmt{&ir.NodeInst{Name: "text", Props: []ir.Arg{{
					Name:  "value",
					Value: boomWith(&ir.Ident{Name: "name", Sym: loop, Type: ir.TypString}),
				}}}},
			}}
		}
		comps = append(comps, &ir.Component{Name: "main", Body: body})
		return &ir.Package{
			Components: comps,
			Imports: []*ir.Import{{
				Path:  "go://" + purepkgPath,
				Alias: "purepkg",
				Native: &ir.NativeImport{
					ImportPath: purepkgPath,
					Funcs:      []*ir.Func{fnBoomWith},
				},
			}},
		}
	}

	for _, tc := range []struct {
		name    string
		inlined bool
	}{{"inlined component body", true}, {"for-loop iteration", false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := Optimize(mkPkg(tc.inlined), &Config{Platform: "html", Language: "none", Dir: dir})
			if err == nil {
				t.Fatal("a nested fold's fatal error was dropped")
			}
			if !strings.Contains(err.Error(), "BoomWith") {
				t.Errorf("error does not name the call that failed: %v", err)
			}
		})
	}
}

// A compilation's evaluated values are its own. The key records the function
// and its rendered arguments but nothing about the Go body behind it, so a
// value folded by one compilation must not answer the next — that is what lets
// `sngl preview` see a pure func whose body was edited between reloads.
func TestCachedValueDoesNotCrossCompilations(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	evalNow(t, purepkgCtx(dir), call{fnGreet, []any{"scope"}})

	next := purepkgCtx(dir)
	if _, state, err := requestPureNativeFunc(next, "go", purepkgPath, fnGreet, []any{"scope"}); state != nativePending || err != nil {
		t.Fatalf("a later compilation answered from the earlier one's cache: state=%v err=%v", state, err)
	}
}
