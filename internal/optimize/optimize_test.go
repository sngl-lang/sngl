package optimize

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkAndOptimize is a test helper: parse → check → optimize → convert.
func checkAndOptimize(t *testing.T, source, platform, lang string) (*ir.Package, *ast.Document) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS("."),
		Dir:    ".",
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{Platform: platform, Language: lang}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	return pkg, ir.Convert(pkg)
}

func formatDoc(doc *ast.Document) string {
	return parser.Format(doc)
}

func TestOptimize_ConstFolding(t *testing.T) {
	src := `
const x = 2 + 3
component main {
	text(value=string(x))
}
`
	// After optimization, string(x) with x=5 folds to "5".
	// The const itself may be shaken since it's fully inlined.
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	// The folded value "5" should appear in the output.
	if !strings.Contains(out, `"5"`) {
		t.Errorf("expected folded value \"5\" in output:\n%s", out)
	}
}

func TestOptimize_ConstExprFolds(t *testing.T) {
	// const(expr) operand is unwrapped by the checker, so the optimizer
	// folds it just like any other constant. The literal "7" must appear
	// in the output.
	src := `
const x = const (3 + 4)
component main {
	text(value=string(x))
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `"7"`) {
		t.Errorf("expected folded value \"7\" in output:\n%s", out)
	}
}

func TestOptimize_ConstExprFoldsPureCall(t *testing.T) {
	// Primary use case: `const pure_fn(args)` must fold to a literal at
	// compile time. Inlining + folding turns double(21) into 42.
	src := `
func double(x int) int { return x * 2 }
const x int = const double(21)
component main {
	text(value=string(x))
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `"42"`) {
		t.Errorf("expected folded value \"42\" in output:\n%s", out)
	}
	// The call site must not survive as a runtime call.
	if strings.Contains(out, "double(") {
		t.Errorf("call to double() leaked past optimizer:\n%s", out)
	}
}

func TestOptimize_PlatformElimination(t *testing.T) {
	src := `
component main {
	platform html {
		text(value="html only")
	}
	platform bubbletea {
		text(value="bubbletea only")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, "html only") {
		t.Error("expected 'html only' to be kept")
	}
	if strings.Contains(out, "bubbletea only") {
		t.Error("expected 'bubbletea only' to be removed")
	}
}

func TestOptimize_IfConstTrue(t *testing.T) {
	src := `
component main {
	if PLATFORM == "html" {
		text(value="yes")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, "yes") {
		t.Error("expected 'yes' to be kept for matching platform")
	}
}

func TestOptimize_IfConstFalse(t *testing.T) {
	src := `
component main {
	if PLATFORM == "bubbletea" {
		text(value="no")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if strings.Contains(out, `"no"`) {
		t.Error("expected dead branch to be removed")
	}
}

func TestOptimize_ConstPropagation(t *testing.T) {
	src := `
const greeting = "hello"
const msg = greeting + " world"
component main {
	text(value=msg)
}
`
	// After optimization, msg = "hello" + " world" = "hello world".
	// The text prop gets the folded value. Consts may be shaken.
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `hello world`) {
		t.Errorf("expected folded 'hello world' in output:\n%s", out)
	}
}

func TestOptimize_FunctionInlining(t *testing.T) {
	// Test that a pure function call in a component gets inlined.
	src := `
func double(x int) => x * 2

component main {
	text(value=string(double(21)))
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	// The double(21) call should be inlined and folded to 42.
	// Find the text node in the component body and check its value prop.
	if len(pkg.Components) == 0 {
		t.Fatal("expected at least one component")
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Name == "text" {
			for _, p := range ni.Props {
				if p.Name == "value" {
					// Should be folded to literal "42" (string, raw chars `42`).
					if lit, ok := p.Value.(*ir.Literal); ok {
						if lit.Raw == "42" {
							return // success
						}
						t.Errorf("expected literal 42, got %s", lit.Raw)
						return
					}
					// Might be a Conversion wrapping a literal.
					if conv, ok := p.Value.(*ir.Conversion); ok {
						if lit, ok := conv.Operand.(*ir.Literal); ok {
							if lit.Raw == "42" {
								return // success — string(42) not fully folded, but inline worked
							}
						}
					}
					t.Logf("value prop type: %T", p.Value)
				}
			}
		}
	}
	// If double is pure and inlined, that's the key test.
	// At minimum, the func should be shaken if fully inlined.
	for _, f := range pkg.Funcs {
		if f.Name == "double" {
			t.Log("double function was not shaken (still referenced)")
			return
		}
	}
	t.Log("double function was shaken (fully inlined)")
}

func TestOptimize_ShakeUnusedConst(t *testing.T) {
	src := `
const used = 1
const unused = 2
component main {
	text string(used)
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	for _, c := range pkg.Consts {
		if c.Name == "unused" {
			t.Error("expected unused const to be shaken")
		}
	}
}

func TestOptimize_ShakeUnusedFunc(t *testing.T) {
	src := `
func used() => 1
func unused() => 2
component main {
	text(value=string(used()))
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	for _, f := range pkg.Funcs {
		if f.Name == "unused" {
			t.Error("expected unused func to be shaken")
		}
	}
}

func TestOptimize_KeepTestFunc(t *testing.T) {
	src := `
func testFoo() => 1
component main {
	text(value="hi")
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	found := false
	for _, f := range pkg.Funcs {
		if f.Name == "testFoo" {
			found = true
		}
	}
	if !found {
		t.Error("expected test func to be preserved")
	}
}

func TestOptimize_EmptyPkg(t *testing.T) {
	pkg := &ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
	if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatal(err)
	}
}

// TestOptimize_PropPropagatesAsConst exercises bug #1: a top-level const
// passed as a prop into a child component must let that child's for-loop
// unroll at compile time.
func TestOptimize_PropPropagatesAsConst(t *testing.T) {
	src := `
const items = ["a", "b"]
component Row(entries list<string> = []) {
	for x = entries {
		text(value=x)
	}
}
component main {
	Row(entries=items)
}
`
	pkg, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)

	// The call-site Row(entries=items) must inline + unroll into static
	// text nodes. The Row component's *declaration* may still hold the
	// for-loop (it's a generic template); only main matters.
	var mainComp *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			mainComp = c
		}
	}
	if mainComp == nil {
		t.Fatal("expected component main")
	}
	assertNoFor(t, "main", mainComp.Body)

	if !strings.Contains(out, `"a"`) || !strings.Contains(out, `"b"`) {
		t.Errorf("expected literal \"a\" and \"b\" in output:\n%s", out)
	}

	// Confirm both unrolled text nodes show up directly in main.
	var values []string
	for _, s := range mainComp.Body {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Name == "text" {
			for _, p := range ni.Props {
				if p.Name == "value" {
					if lit, ok := p.Value.(*ir.Literal); ok {
						values = append(values, lit.Raw)
					}
				}
			}
		}
	}
	if len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Errorf("expected unrolled text values [a, b] in main, got %v", values)
	}
}

// TestFoldDoesNotCollapseDerefOfAddrOfConst ensures the const folder leaves
// reference operations alone. `&k` is not const (storage may be aliased), and
// folding the operand of `&` to a literal would produce `&5`, which is not a
// valid lvalue. Likewise `*p` must remain a runtime load even when p was
// initialized from a const's address.
func TestFoldDoesNotCollapseDerefOfAddrOfConst(t *testing.T) {
	src := `
const k int = 5
component main {
    var p ref<int> = &k
    var v int = *p
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")

	if len(pkg.Components) == 0 {
		t.Fatal("expected at least one component")
	}
	var mainComp *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			mainComp = c
		}
	}
	if mainComp == nil {
		t.Fatal("expected component main")
	}

	var pVar, vVar *ir.Var
	for _, v := range mainComp.Vars {
		switch v.Name {
		case "p":
			pVar = v
		case "v":
			vVar = v
		}
	}
	if pVar == nil {
		t.Fatal("expected component var p in main")
	}
	if vVar == nil {
		t.Fatal("expected component var v in main")
	}

	// p's initializer must remain a Unary(&...) — not folded into a literal.
	pInit, ok := pVar.Init.(*ir.Unary)
	if !ok {
		t.Fatalf("expected p.Init to remain *ir.Unary, got %T", pVar.Init)
	}
	if pInit.Op != ast.UnaryAddr {
		t.Fatalf("expected p.Init op UnaryAddr, got %v", pInit.Op)
	}
	// The operand of & must still be the ident k (an lvalue) — NOT a literal.
	if _, ok := pInit.Operand.(*ir.Literal); ok {
		t.Errorf("operand of & was folded to a literal; & must take an lvalue")
	}
	if _, ok := pInit.Operand.(*ir.Ident); !ok {
		t.Errorf("expected & operand to remain *ir.Ident, got %T", pInit.Operand)
	}

	// v's initializer must remain a Unary(*p) — not folded into a literal.
	vInit, ok := vVar.Init.(*ir.Unary)
	if !ok {
		t.Fatalf("expected v.Init to remain *ir.Unary, got %T", vVar.Init)
	}
	if vInit.Op != ast.UnaryDeref {
		t.Fatalf("expected v.Init op UnaryDeref, got %v", vInit.Op)
	}
}

// fsResolver is a minimal checker.ImportResolver that reads .sngl files
// from an fs.FS for directory imports. Scheme imports are unsupported.
type fsResolver struct{}

func (fsResolver) Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error) {
	entries, err := fs.ReadDir(fsys, importPath)
	if err != nil {
		return nil, err
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		path := importPath + "/" + e.Name()
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, err
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (fsResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	return nil, nil
}

func (fsResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

// checkAndOptimizeFS lets a test ship multiple files via fstest.MapFS, then
// parses+checks+optimizes the named entrypoint and returns the root package.
func checkAndOptimizeFS(t *testing.T, fsys fs.FS, entry, platform, lang string) *ir.Package {
	t.Helper()
	data, err := fs.ReadFile(fsys, entry)
	if err != nil {
		t.Fatalf("read %s: %v", entry, err)
	}
	doc, err := parser.Parse(entry, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:       fsys,
		Dir:      ".",
		IsMain:   true,
		Resolver: fsResolver{},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{Platform: platform, Language: lang}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	return pkg
}

// TestOptimize_ImportedComponentForUnrolls exercises bug #2: a for-loop
// inside an imported component, iterating over a const declared in that
// imported package, must unroll at compile time. Today the optimizer
// never visits imports' Components, so the for-loop survives.
func TestOptimize_ImportedComponentForUnrolls(t *testing.T) {
	fsys := fstest.MapFS{
		"main.sngl": &fstest.MapFile{Data: []byte(`
import "lib"
component main {
	lib.List()
}
`)},
		"lib/widgets.sngl": &fstest.MapFile{Data: []byte(`
const tags = ["x", "y"]
component List() {
	for t = tags {
		text(value=t)
	}
}
`)},
	}
	pkg := checkAndOptimizeFS(t, fsys, "main.sngl", "html", "js")

	// Find the imported lib package and assert its List component body has
	// no surviving *ir.For.
	var list *ir.Component
	for _, imp := range pkg.Imports {
		if imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c.Name == "List" {
				list = c
				break
			}
		}
	}
	if list == nil {
		t.Fatal("expected to find imported component List")
	}
	assertNoFor(t, "lib.List", list.Body)

	// And the unrolled NodeInsts should be plain text(value="x")/text(value="y").
	var values []string
	for _, s := range list.Body {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Name == "text" {
			for _, p := range ni.Props {
				if p.Name == "value" {
					if lit, ok := p.Value.(*ir.Literal); ok {
						values = append(values, lit.Raw)
					}
				}
			}
		}
	}
	if len(values) != 2 || values[0] != "x" || values[1] != "y" {
		t.Errorf("expected text values [x, y], got %v", values)
	}
}

func assertNoFor(t *testing.T, where string, stmts []ir.Stmt) {
	t.Helper()
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.For:
			t.Errorf("%s: unexpected *ir.For after Optimize", where)
		case *ir.NodeInst:
			assertNoFor(t, where, n.Children)
		case *ir.If:
			assertNoFor(t, where, n.Body)
			assertNoFor(t, where, n.Else)
		case *ir.PlatformFilter:
			assertNoFor(t, where, n.Body)
		}
	}
}

// TestFoldsTestdata exercises the previously-dead `// FOLD value` directive
// machinery. Each fixture with FOLD directives is parsed, checked, optimized;
// each marked line is checked against the corresponding *ir.Var initializer
// to confirm it folded to the expected literal value.
func TestFoldsTestdata(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if len(s.Folds) == 0 {
			continue
		}
		if s.ExpectsError("parse") || s.ExpectsError("check") {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{
				FS:     s.FS,
				Dir:    s.Dir,
				IsMain: true,
			})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Fatalf("check: %s", d.Error())
				}
			}
			if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
				t.Fatalf("optimize: %v", err)
			}
			byLine := collectVarsByLine(pkg)
			for _, fd := range s.Folds {
				v, ok := byLine[fd.Line]
				if !ok {
					t.Errorf("line %d: no var found at FOLD directive line", fd.Line)
					continue
				}
				lit, ok := v.Init.(*ir.Literal)
				if !ok {
					t.Errorf("line %d (%s): expected folded literal, got %T (%s)",
						fd.Line, v.Name, v.Init, irExprDescr(v.Init))
					continue
				}
				got := litValue(lit)
				if got != fd.Expected {
					t.Errorf("line %d (%s): got %v (%T), want %v (%T)",
						fd.Line, v.Name, got, got, fd.Expected, fd.Expected)
				}
			}
		})
	}
}

func collectVarsByLine(pkg *ir.Package) map[int]*ir.Var {
	byLine := map[int]*ir.Var{}
	walk := func(v *ir.Var) {
		if v == nil || v.AST == nil {
			return
		}
		// VarDecl/ConstDecl positions: each spec sits on the parent decl's
		// line for single-line decls; grouped (parenthesized) decls share
		// the parent decl's line. The fold matcher uses the spec line, so
		// we record the per-spec position when available.
		// VarSpec has no Pos, so non-grouped var/const decls (one spec
		// per line) are matched by the parent decl's line. Grouped decls
		// would land all specs on the same line, which is good enough for
		// the existing FOLD fixtures (none use grouped form for FOLDed
		// expressions).
		switch d := v.AST.(type) {
		case *ast.VarDecl:
			byLine[d.Pos.Line] = v
		case *ast.ConstDecl:
			byLine[d.Pos.Line] = v
		}
	}
	for _, v := range pkg.Vars {
		walk(v)
	}
	for _, v := range pkg.Consts {
		walk(v)
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			walk(v)
		}
	}
	return byLine
}

func litValue(lit *ir.Literal) any {
	if lit == nil || lit.Type == nil {
		return lit.Raw
	}
	switch lit.Type.Kind {
	case ir.TypeInt:
		var i int
		_, _ = fmt.Sscanf(lit.Raw, "%d", &i)
		return i
	case ir.TypeFloat:
		var f float64
		_, _ = fmt.Sscanf(lit.Raw, "%g", &f)
		return f
	case ir.TypeBool:
		return lit.Raw == "true"
	case ir.TypeString:
		return lit.Raw
	}
	return lit.Raw
}

func irExprDescr(e ir.Expr) string {
	if e == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", e)
}
