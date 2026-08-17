package checker_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	_ "git.duckfam.us/jonathan/sngl/internal/macros/canvas"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// mockResolver provides fake packages for import tests.
type mockResolver struct {
	pkgs map[string]string // import path → SNGL source
}

func (m *mockResolver) Resolve(_ fs.FS, path string) ([]*ast.Document, error) {
	src, ok := m.pkgs[path]
	if !ok {
		return nil, fmt.Errorf("package %q not found", path)
	}
	doc, err := parser.Parse(path+".sngl", []byte(src))
	if err != nil {
		return nil, err
	}
	return []*ast.Document{doc}, nil
}

func (m *mockResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	return nil, fmt.Errorf("scheme imports not supported in tests")
}

func (m *mockResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

func newTestResolver() *mockResolver {
	return &mockResolver{pkgs: map[string]string{
		"widgets": `
component Counter(label = "") {
    var count = 0
    text(value=label)
}

component _helper() {
    text(value="private")
}
`,
		"badlib": `
component main {
    text(value="oops")
}
`,
	}}
}

func parse(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Logf("diagnostic: %s", d.Error())
		}
	}
	return pkg
}

func TestTernaryNumericPromotion(t *testing.T) {
	// A ternary mixing an int and a float constant branch types as float: the
	// untyped int constant `0` adopts the float type of the other branch. This
	// is constant representability, not a value-level int→float conversion —
	// the branch becomes a float literal rather than being wrapped in a cast.
	pkg := parse(t, `
component main {
	func pick(b bool) => b ? 0 : 1.5
	text(value=string(pick(true)))
}`)
	var fn *ir.Func
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			if f.Name == "pick" {
				fn = f
			}
		}
	}
	if fn == nil {
		t.Fatal("func pick not found")
	}
	if fn.Return == nil || fn.Return.Kind != ir.TypeFloat {
		t.Fatalf("pick return type = %v, want float", fn.Return)
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("body[0] = %T, want *ir.Return", fn.Block[0])
	}
	tern, ok := ret.Value.(*ir.Ternary)
	if !ok {
		t.Fatalf("return value = %T, want *ir.Ternary", ret.Value)
	}
	if tern.Type == nil || tern.Type.Kind != ir.TypeFloat {
		t.Errorf("ternary type = %v, want float", tern.Type)
	}
	then := tern.Then.ExprType()
	if then == nil || then.Kind != ir.TypeFloat {
		t.Errorf("then-branch type = %v, want float (int 0 adopted the float branch's type)", then)
	}
}

func TestVarTypes(t *testing.T) {
	pkg := parse(t, `
var count int
var name string
var active bool
`)
	if len(pkg.Vars) != 3 {
		t.Fatalf("got %d vars, want 3", len(pkg.Vars))
	}
	tests := []struct {
		name string
		want string
	}{
		{"count", "int"},
		{"name", "string"},
		{"active", "bool"},
	}
	for i, tt := range tests {
		if pkg.Vars[i].Name != tt.name {
			t.Errorf("var %d: name = %q, want %q", i, pkg.Vars[i].Name, tt.name)
		}
		if pkg.Vars[i].Type.String() != tt.want {
			t.Errorf("var %d: type = %s, want %s", i, pkg.Vars[i].Type, tt.want)
		}
	}
}

func TestConstTypes(t *testing.T) {
	pkg := parse(t, `
const pi float = 3.14
const name = "hello"
`)
	if len(pkg.Consts) != 2 {
		t.Fatalf("got %d consts, want 2", len(pkg.Consts))
	}
	if pkg.Consts[0].Type.String() != "float" {
		t.Errorf("pi type = %s, want float", pkg.Consts[0].Type)
	}
	if !pkg.Consts[0].IsConst {
		t.Error("pi should be const")
	}
}

func TestStructDef(t *testing.T) {
	pkg := parse(t, `
struct Todo {
	title string
	done bool
}
`)
	if len(pkg.Structs) != 1 {
		t.Fatalf("got %d structs, want 1", len(pkg.Structs))
	}
	s := pkg.Structs[0]
	if s.Name != "Todo" {
		t.Errorf("name = %q, want Todo", s.Name)
	}
	if len(s.Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(s.Fields))
	}
	if s.Fields[0].Type.String() != "string" {
		t.Errorf("title type = %s, want string", s.Fields[0].Type)
	}
	if s.Fields[1].Type.String() != "bool" {
		t.Errorf("done type = %s, want bool", s.Fields[1].Type)
	}
}

func TestEnumDef(t *testing.T) {
	pkg := parse(t, `
enum Color { red, green, blue }
`)
	if len(pkg.Enums) != 1 {
		t.Fatalf("got %d enums, want 1", len(pkg.Enums))
	}
	e := pkg.Enums[0]
	if e.Name != "Color" {
		t.Errorf("name = %q, want Color", e.Name)
	}
	if len(e.Members) != 3 {
		t.Fatalf("got %d members, want 3", len(e.Members))
	}
}

func TestFuncDef(t *testing.T) {
	pkg := parse(t, `
func add(a int, b int) => a + b
`)
	if len(pkg.Funcs) != 1 {
		t.Fatalf("got %d funcs, want 1", len(pkg.Funcs))
	}
	f := pkg.Funcs[0]
	if f.Name != "add" {
		t.Errorf("name = %q, want add", f.Name)
	}
	if len(f.Params) != 2 {
		t.Fatalf("got %d params, want 2", len(f.Params))
	}
}

func TestMethodDef(t *testing.T) {
	pkg := parse(t, `
func int.double(x int) => x + x
`)
	if len(pkg.Funcs) != 1 {
		t.Fatalf("got %d funcs, want 1", len(pkg.Funcs))
	}
	f := pkg.Funcs[0]
	if f.Receiver != "int" {
		t.Errorf("receiver = %q, want int", f.Receiver)
	}
	if f.Name != "double" {
		t.Errorf("name = %q, want double", f.Name)
	}
	// Should be registered as a method.
	if _, ok := pkg.Symbols.LookupMethod("int", "double"); !ok {
		t.Error("method int.double not registered")
	}
}

func TestComponentDef(t *testing.T) {
	pkg := parse(t, `
component Counter(label string, :count int, @click) {
}
`)
	if len(pkg.Components) != 1 {
		t.Fatalf("got %d components, want 1", len(pkg.Components))
	}
	comp := pkg.Components[0]
	if comp.Name != "Counter" {
		t.Errorf("name = %q, want Counter", comp.Name)
	}
	if len(comp.Props) != 2 {
		t.Errorf("got %d props, want 2", len(comp.Props))
	}
	if len(comp.Events) != 1 {
		t.Errorf("got %d events, want 1", len(comp.Events))
	}
	if comp.Props[1].Bidirectional != true {
		t.Error("count should be bidirectional")
	}
}

func TestPurityAnalysis(t *testing.T) {
	pkg := parse(t, `
var count int
func pure(a int, b int) => a + b
`)
	for _, f := range pkg.Funcs {
		if f.Name == "pure" && f.Purity != ir.PurityPure {
			t.Errorf("pure func purity = %d, want PurityPure", f.Purity)
		}
	}
}

func TestDiagnosticUnknownType(t *testing.T) {
	src := `var x Nonexistent`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error diagnostic for unknown type")
	}
}

// expectError parses src, runs Check, and asserts at least one error diagnostic
// contains the given substring.
func expectError(t *testing.T, src, substr string) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error && contains(d.Msg, substr) {
			return
		}
	}
	var msgs strings.Builder
	for _, d := range diags {
		msgs.WriteString("\n  " + d.Error())
	}
	t.Errorf("expected error containing %q, got:%s", substr, msgs.String())
}

// expectNoErrors parses src, runs Check, and asserts no error diagnostics.
func expectNoErrors(t *testing.T, src string) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Binary/unary operator validation ---

func TestBinaryOpTypeMismatch(t *testing.T) {
	expectError(t, `const x = "hello" + true`, "operator + not defined")
	expectError(t, `const x = "hello" - 1`, "operator - not defined")
	expectError(t, `const x = true && 1`, "operator && not defined")
	expectError(t, `const x = 1 || 2`, "operator || not defined")
}

func TestBinaryOpValid(t *testing.T) {
	expectNoErrors(t, `const x = 1 + 2`)
	expectNoErrors(t, `const x = 1.0 + 2`)
	expectNoErrors(t, `const x = "a" + "b"`)
	expectNoErrors(t, `const x = true && false`)
	expectNoErrors(t, `const x = 1 < 2`)
	expectNoErrors(t, `const x = 1 == 2`)
}

func TestUnaryOpTypeMismatch(t *testing.T) {
	expectError(t, `const x = !1`, "operator ! not defined")
	expectError(t, `const x = -true`, "operator - not defined")
}

func TestUnaryOpValid(t *testing.T) {
	expectNoErrors(t, `const x = !true`)
	expectNoErrors(t, `const x = -1`)
	expectNoErrors(t, `const x = -1.5`)
}

func TestCheckRefAndDeref(t *testing.T) {
	expectNoErrors(t, `
component main {
    var x int = 0
    var p ref<int> = &x
    var y int = *p
}`)
}

func TestCheckAddrOfNonLvalue(t *testing.T) {
	expectError(t, `
component main {
    var p ref<int> = &(1 + 2)
}`, "non-lvalue")
}

func TestCheckDerefNonRef(t *testing.T) {
	expectError(t, `
component main {
    var x int = 0
    var y int = *x
}`, "cannot dereference")
}

func TestCheckAutoDerefSelect(t *testing.T) {
	expectNoErrors(t, `
struct Node {
    value int
}
component main {
    var n Node = Node{value=0}
    var p ref<Node> = &n
    var v int = p.value
    func bump() { p.value = p.value + 1 }
}`)
}

// --- const(expr) assertion ---

func TestConstExprLiteral(t *testing.T) {
	expectNoErrors(t, `const x = const 42`)
	expectNoErrors(t, `const x = const (1 + 2)`)
	expectNoErrors(t, `const x = const ("a" + "b")`)
	expectNoErrors(t, `
const base = 10
const x = const (base * 2)
`)
}

func TestConstExprNonConstVarRejected(t *testing.T) {
	expectError(t, `
var y int = 3
const x = const (y + 1)
`, "not a constant expression")
}

func TestConstExprInsideComponent(t *testing.T) {
	expectError(t, `
component main {
	var count = 0
	text(value=const count)
}
`, "not a constant expression")
}

// TestConstExprErasedFromIR guards the invariant that the checker flattens
// ConstExpr away: the operand is returned directly so downstream phases
// (optimizer, codegen) never encounter a wrapper.
func TestConstExprErasedFromIR(t *testing.T) {
	pkg := parse(t, `const x = const (1 + 2)`)
	if len(pkg.Consts) != 1 {
		t.Fatalf("got %d consts, want 1", len(pkg.Consts))
	}
	init := pkg.Consts[0].Init
	if _, ok := init.(*ir.Binary); !ok {
		t.Fatalf("expected unwrapped *ir.Binary, got %T", init)
	}
}

// --- const(expr) locks function purity ---
//
// The primary motivation for `const` is to force compile-time evaluation of
// pure function calls. If a user refactors a function from pure → impure,
// every `const fn(...)` call site surfaces the regression as a check error.

func TestConstExprUserPureFunc(t *testing.T) {
	// Pure user funcs are eligible for const-folding; refactoring away
	// their purity (e.g. by reading a var) should flip this test.
	expectNoErrors(t, `
func double(x int) int { return x * 2 }
var y int = const double(21)
`)
}

func TestConstExprUserImpureFuncRejected(t *testing.T) {
	// Mutating a module-level var makes the func impure.
	expectError(t, `
var counter int
func bump(x int) int {
	counter = counter + 1
	return x + counter
}
var y int = const bump(1)
`, "not a constant expression")
}

// importResolver is a mock ResolveScheme that hands back a pre-built
// NativeImport so tests can exercise imported-func purity without running
// the real go:// importer.
type importResolver struct {
	mockResolver
	native map[string]*ir.NativeImport
}

func (r *importResolver) ResolveScheme(scheme, uri, _ string) (*ir.NativeImport, error) {
	key := scheme + "://" + uri
	if ni, ok := r.native[key]; ok {
		return ni, nil
	}
	return nil, fmt.Errorf("unknown scheme import %q", key)
}

// checkWithImports runs the checker with a resolver that supplies native
// imports keyed by "scheme://uri".
func checkWithImports(src string, native map[string]*ir.NativeImport) []ir.Diagnostic {
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		return []ir.Diagnostic{{Severity: ir.Error, Msg: err.Error()}}
	}
	r := &importResolver{native: native}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	return diags
}

func intType() *ir.Type { return &ir.Type{Kind: ir.TypeInt} }

func nativeMath(purity ir.Purity) map[string]*ir.NativeImport {
	fn := &ir.Func{
		Name:       "Square",
		Params:     []*ir.Param{{Name: "x", Type: intType()}},
		Return:     intType(),
		Purity:     purity,
		NativePkg:  "math",
		NativeName: "math.Square",
	}
	return map[string]*ir.NativeImport{
		"go://math": {
			ImportPath: "math",
			Funcs:      []*ir.Func{fn},
		},
	}
}

func TestConstExprImportedPureFunc(t *testing.T) {
	src := `
import math "go://math"
var y = const math.Square(4)
`
	diags := checkWithImports(src, nativeMath(ir.PurityPure))
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestConstExprImportedImpureFuncRejected(t *testing.T) {
	src := `
import math "go://math"
var y = const math.Square(4)
`
	diags := checkWithImports(src, nativeMath(ir.PurityUnknown))
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error && contains(d.Msg, "not a constant expression") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'not a constant expression' error for impure import")
	}
}

// --- Import aliases / replaces ---

func TestImportIdentAlias(t *testing.T) {
	r := &mockResolver{pkgs: map[string]string{
		"widgets": `component Counter(label = "") { text(value=label) }`,
	}}
	doc, err := parser.Parse("test.sngl", []byte(`
import w "widgets"

component main {
    w.Counter(label="Clicks")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestImportReplaceRoutesToReplacementURL(t *testing.T) {
	// Replace routes resolution to the replacement path. Original path "widgets"
	// resolves via "widgets_v2" (no scheme so the directory resolver is used).
	r := &mockResolver{pkgs: map[string]string{
		"widgets_v2": `component Counter(label = "") { text(value=label) }`,
	}}
	doc, err := parser.Parse("test.sngl", []byte(`
import "widgets" => "widgets_v2"

component main {
    widgets.Counter(label="x")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestImportReplacePropagatesToLibrary(t *testing.T) {
	// Main declares a replace; a library it imports has a bare `import "widgets"`
	// which must be redirected via the main's replace map.
	r := &mockResolver{pkgs: map[string]string{
		"shim": `
import "widgets"

component Wrapped(label = "") {
    widgets.Counter(label=label)
}
`,
		"widgets_v2": `component Counter(label = "") { text(value=label) }`,
	}}
	doc, err := parser.Parse("test.sngl", []byte(`
import "shim"
import "widgets" => "widgets_v2"

component main {
    shim.Wrapped(label="x")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestImportSchemeFSDispatch(t *testing.T) {
	// Verify registerImport tries ResolveSchemeFS first for scheme imports
	// (git://, http://, …) and falls back to ResolveScheme only when FS says
	// "not my scheme" (nil docs).
	r := &schemeFSResolver{
		fsPkgs: map[string]string{
			"git://example.com/widgets@v1#-": `component Counter(label = "") { text(value=label) }`,
		},
	}
	doc, err := parser.Parse("test.sngl", []byte(`
import "widgets" => "git://example.com/widgets@v1#-"

component main {
    widgets.Counter(label="x")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// schemeFSResolver returns canned SNGL source for specific scheme URIs via
// ResolveSchemeFS. ResolveScheme never fires for these; falling back to it
// is a bug the test would catch.
type schemeFSResolver struct {
	mockResolver
	fsPkgs map[string]string // full "scheme://uri" → SNGL source
}

func (r *schemeFSResolver) ResolveSchemeFS(scheme, uri, _ string) ([]*ast.Document, fs.FS, error) {
	key := scheme + "://" + uri
	src, ok := r.fsPkgs[key]
	if !ok {
		return nil, nil, nil
	}
	doc, err := parser.Parse(key, []byte(src))
	if err != nil {
		return nil, nil, err
	}
	return []*ast.Document{doc}, nil, nil
}

func TestImportReplaceDuplicate(t *testing.T) {
	r := &mockResolver{pkgs: map[string]string{
		"widgets_v2": `component Counter(label = "") { text(value=label) }`,
		"widgets_v3": `component Counter(label = "") { text(value=label) }`,
	}}
	doc, err := parser.Parse("test.sngl", []byte(`
import "widgets" => "widgets_v2"
import "widgets" => "widgets_v3"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error && contains(d.Msg, "duplicate import replace") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate import replace error; diags: %v", diags)
	}
}

// --- If condition / for iterator ---

func TestIfConditionMustBeBool(t *testing.T) {
	expectError(t, `
func foo() {
	if 1 {
	}
}`, "if condition must be bool")
}

func TestIfConditionBoolOk(t *testing.T) {
	expectNoErrors(t, `
func foo() {
	if true {
	}
}`)
}

func TestForIteratorMustBeList(t *testing.T) {
	expectError(t, `
func foo() {
	var x int
	for item = x {
	}
}`, "for iterator must be list")
}

func TestForIteratorListOk(t *testing.T) {
	expectNoErrors(t, `
func foo() {
	var items list<int>
	for item = items {
	}
}`)
}

// --- Assignment type checking ---

func TestAssignTypeMismatch(t *testing.T) {
	expectError(t, `
func foo() {
	var x int
	x = "hello"
}`, "cannot assign string to int")
}

func TestAssignValid(t *testing.T) {
	expectNoErrors(t, `
func foo() {
	var x int
	x = 42
}`)
}

func TestAssignIntToFloat(t *testing.T) {
	expectNoErrors(t, `
func foo() {
	var x float
	x = 42
}`)
}

// --- Const reassignment ---

func TestConstReassignment(t *testing.T) {
	expectError(t, `
const x = 1
func foo() {
	x = 2
}`, "cannot assign to const")
}

// --- Return type checking ---

func TestReturnTypeMismatch(t *testing.T) {
	expectError(t, `
func foo() int {
	return "hello"
}`, "cannot return string as int")
}

func TestReturnTypeValid(t *testing.T) {
	expectNoErrors(t, `
func foo() int {
	return 42
}`)
}

func TestExprBodyReturnMismatch(t *testing.T) {
	// Expression body funcs infer return type from body — no explicit
	// return type annotation, so no mismatch possible for => form.
	// Test block form instead.
	expectError(t, `
func foo() int {
	return "hello"
}`, "cannot return string as int")
}

func TestExprBodyReturnValid(t *testing.T) {
	expectNoErrors(t, `
func foo() int {
	return 42
}`)
}

// --- Const expression validation ---

func TestConstNonConstRef(t *testing.T) {
	expectError(t, `
var mutable = 5
const BAD = mutable + 1
`, "non-const")
}

func TestConstConstRefOk(t *testing.T) {
	expectNoErrors(t, `
const a = 1
const b = a + 2
`)
}

func TestConstFuncCallNotAllowed(t *testing.T) {
	expectError(t, `
func compute() => 42
const x = compute()
`, "non-const")
}

// --- Var init type checking ---

func TestVarInitTypeMismatch(t *testing.T) {
	expectError(t, `var x int = "hello"`, "cannot initialize int with string")
}

func TestVarInitValid(t *testing.T) {
	expectNoErrors(t, `var x int = 42`)
}

func TestVarInitIntToFloat(t *testing.T) {
	expectNoErrors(t, `var x float = 42`)
}

// --- Function argument type checking ---

func TestFuncArgTypeMismatch(t *testing.T) {
	expectError(t, `
func add(a int, b int) => a + b
func test() {
	var x = add("hello", 1)
}
`, "cannot pass string as int")
}

func TestFuncArgArityMismatch(t *testing.T) {
	expectError(t, `
func add(a int, b int) => a + b
func test() {
	var x = add(1)
}
`, "missing required argument")
}

func TestFuncArgValid(t *testing.T) {
	expectNoErrors(t, `
func add(a int, b int) => a + b
func test() {
	var x = add(1, 2)
}
`)
}

func TestGenericTypeInference(t *testing.T) {
	pkg := parse(t, `
func identity<T>(x T) => x
func first<T>(items list<T>) T {
	return items[0]
}
func test() {
	var a = identity(42)
	var b = identity("hello")
	var items list<int>
	var c = first(items)
}
`)
	// Verify inferred return types via function IR.
	for _, fn := range pkg.Funcs {
		switch fn.Name {
		case "identity":
			if fn.Return == nil || fn.Return.String() == "dyn" {
				t.Errorf("identity return type not inferred: %s", fn.Return)
			}
		case "first":
			if fn.Return == nil || fn.Return.String() != "T" {
				t.Errorf("first return type: got %s, want T", fn.Return)
			}
		}
	}
}

// --- Method call semantics ---

func TestMethodInstanceCall(t *testing.T) {
	// 2.add(3) — receiver is implicit first arg.
	expectNoErrors(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = 2.add(3)
}
`)
}

func TestMethodStaticCall(t *testing.T) {
	// int.add(2, 3) — all args explicit.
	expectNoErrors(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = int.add(2, 3)
}
`)
}

func TestMethodInstanceWrongArgType(t *testing.T) {
	expectError(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = 2.add("hello")
}
`, "cannot pass string as int")
}

func TestMethodStaticWrongArgType(t *testing.T) {
	expectError(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = int.add(2, "hello")
}
`, "cannot pass string as int")
}

func TestMethodStaticArity(t *testing.T) {
	expectError(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = int.add(2)
}
`, "missing required argument")
}

func TestMethodInstanceArity(t *testing.T) {
	// Instance form shifts sig: 2.add() passes only receiver, missing second param.
	expectError(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = 2.add()
}
`, "missing required argument")
}

func TestMethodStringReceiver(t *testing.T) {
	expectNoErrors(t, `
func string.upper(s string) string {
	return s
}
func test() {
	var x = "hello".upper()
	var y = string.upper("hello")
}
`)
}

// --- Stdlib tests ---

func TestStdlibComponentResolution(t *testing.T) {
	// text() should resolve as a stdlib component, not an error.
	expectNoErrors(t, `
component main {
	text(value="hello")
}
`)
}

func TestStdlibComponentWithChildren(t *testing.T) {
	expectNoErrors(t, `
component main {
	vbox {
		text(value="a")
		text(value="b")
	}
}
`)
}

func TestStdlibComponentUnknownProp(t *testing.T) {
	expectError(t, `
component main {
	text(bogus="bad")
}
`, `unknown prop "bogus" on component text`)
}

func TestStdlibMethodCall(t *testing.T) {
	expectNoErrors(t, `
func test() {
	var x = "hello".upper()
	var y = "hello".length()
}
`)
}

func TestStdlibListMethodCall(t *testing.T) {
	expectNoErrors(t, `
func test() {
	var items = [1, 2, 3]
	var n = items.length()
}
`)
}

func TestStdlibComponentCallStmt(t *testing.T) {
	expectNoErrors(t, `
component main {
	text(value="hello")
	button(text="click me")
}
`)
}

func TestStdlibQualifiedAccess(t *testing.T) {
	// sngl.text resolves to stdlib text even when user shadows it.
	expectNoErrors(t, `
component text() {}
component main {
	sngl.text(value="stdlib text")
	text()
}
`)
}

func TestBodyDisambiguation(t *testing.T) {
	pkg := parse(t, `
component greeting() {}

func sideEffect() {}

component main {
	greeting()
	sideEffect()
	if true {
		greeting()
	}
}
`)
	// Find main component.
	var main *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			main = c
		}
	}
	if main == nil {
		t.Fatal("main component not found")
	}
	if len(main.Body) == 0 {
		t.Fatal("Body is empty")
	}

	// First statement: greeting() → NodeInst (component instantiation).
	ni, ok := main.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("Body[0]: want *NodeInst, got %T", main.Body[0])
	}
	if ni.Name != "greeting" {
		t.Errorf("NodeInst.Name = %q, want %q", ni.Name, "greeting")
	}
	if ni.Component == nil {
		t.Error("NodeInst.Component is nil, want resolved component")
	}

	// Second statement: sideEffect() → CallStmt (function call).
	cs, ok := main.Body[1].(*ir.CallStmt)
	if !ok {
		t.Fatalf("Body[1]: want *CallStmt, got %T", main.Body[1])
	}
	if cs.Call == nil {
		t.Error("CallStmt.Call is nil, want resolved call")
	} else if cs.Call.Func == nil {
		t.Error("CallStmt.Call.Func is nil, want resolved function")
	}

	// Third statement: if → If with nested NodeInst.
	ifStmt, ok := main.Body[2].(*ir.If)
	if !ok {
		t.Fatalf("Body[2]: want *If, got %T", main.Body[2])
	}
	if len(ifStmt.Body) != 1 {
		t.Fatalf("If.Body length = %d, want 1", len(ifStmt.Body))
	}
	if _, ok := ifStmt.Body[0].(*ir.NodeInst); !ok {
		t.Errorf("If.Body[0]: want *NodeInst, got %T", ifStmt.Body[0])
	}
}

func TestBodyForLoop(t *testing.T) {
	pkg := parse(t, `
component main {
	var items = [1, 2, 3]
	for item = items {
		text(value="hi")
	}
}
`)
	var main *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			main = c
		}
	}
	if main == nil {
		t.Fatal("main component not found")
	}

	// Find the For statement (skip any non-For stmts).
	var forStmt *ir.For
	for _, s := range main.Body {
		if f, ok := s.(*ir.For); ok {
			forStmt = f
			break
		}
	}
	if forStmt == nil {
		t.Fatal("For statement not found in Body")
	}
	if forStmt.ElemType == nil {
		t.Fatal("For.ElemType is nil")
	}
	if len(forStmt.Body) != 1 {
		t.Fatalf("For.Body length = %d, want 1", len(forStmt.Body))
	}
	if _, ok := forStmt.Body[0].(*ir.NodeInst); !ok {
		t.Errorf("For.Body[0]: want *NodeInst, got %T", forStmt.Body[0])
	}
}

func TestBodyBareIdentFunction(t *testing.T) {
	// Bare identifier that resolves to a function → CallStmt.
	pkg := parse(t, `
func doStuff() {}

component main {
	doStuff
}
`)
	var main *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			main = c
		}
	}
	if main == nil {
		t.Fatal("main component not found")
	}
	if len(main.Body) == 0 {
		t.Fatal("Body is empty")
	}
	cs, ok := main.Body[0].(*ir.CallStmt)
	if !ok {
		t.Fatalf("Body[0]: want *CallStmt, got %T", main.Body[0])
	}
	if cs.Call == nil {
		t.Error("CallStmt.Call is nil, want resolved call")
	} else if cs.Call.Func == nil {
		t.Error("CallStmt.Call.Func is nil, want resolved function")
	}
}

// --- Testdata-driven tests ---

func testdataDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata")
}

func TestCheckTestdata(t *testing.T) {
	dir := testdataDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *.sngl files in %s", dir)
	}
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			dirs, err := testutil.ParseDirectives(path)
			if err != nil {
				t.Fatalf("directives: %v", err)
			}
			expected := testutil.Filter(dirs, "check")
			lintExpected := testutil.Filter(dirs, "lint")

			if len(testutil.Filter(dirs, "parse")) > 0 && len(expected) == 0 && len(lintExpected) == 0 {
				t.Skip("has ERROR(parse) directive, no ERROR(check) or ERROR(lint)")
				return
			}

			doc, parseErr := parser.Parse(filepath.Base(path), src)
			if parseErr != nil {
				t.Fatalf("parse: %v", parseErr)
			}
			cfg := &checker.Config{IsMain: true}
			for _, s := range doc.Stmts {
				if _, ok := s.(*ast.Import); ok {
					cfg.Resolver = newTestResolver()
					break
				}
			}
			_, diags := checker.Check(doc, cfg)

			if len(expected) == 0 && len(lintExpected) == 0 {
				// No error directives — expect clean check.
				for _, d := range diags {
					if d.Severity == ir.Error {
						t.Errorf("unexpected error: %s", d.Error())
					}
				}
				return
			}
			// Match each ERROR(check) directive against error-severity diagnostics.
			for _, exp := range expected {
				found := false
				for _, d := range diags {
					if d.Severity != ir.Error {
						continue
					}
					// Match by line number and substring.
					if d.Pos.Line == exp.Line && strings.Contains(d.Msg, exp.Substring) {
						found = true
						break
					}
				}
				if !found {
					var got strings.Builder
					for _, d := range diags {
						fmt.Fprintf(&got, "\n  %s", d.Error())
					}
					t.Errorf("line %d: expected error containing %q, got:%s",
						exp.Line, exp.Substring, got.String())
				}
			}
			// Match each ERROR(lint) directive against warning-severity diagnostics.
			testutil.AssertDiagnostics(t, diags, dirs, "lint")
		})
	}
}

// TestNoSilentDynInferred guards the "dyn only when explicit" invariant: for
// fixtures whose source never mentions `dyn`, the checked IR must not contain
// any TypeDyn. A regression in the checker that silently infers dyn for
// unannotated params, struct fields, returns, etc. will show up here.
func TestNoSilentDynInferred(t *testing.T) {
	dir := testdataDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	dynKeyword := regexp.MustCompile(`\bdyn\b`)
	// Calls through these stdlib receivers produce TypDyn today because the
	// stdlib's `=>` funcs omit explicit return annotations (see
	// registerStdlibFunc). Fixtures that use these methods inherit inferred
	// dyn through no fault of the user-code checker paths, so the backstop
	// skips them. Tightening this list should come together with adding
	// return-type annotations to the stdlib.
	stdlibDynMethod := regexp.MustCompile(`\b(?:int|float|string|list|option|color|Alert|File|stdlib|alert|file)\.[a-zA-Z]|\.(?:length|upper|lower|trim|contains|startsWith|endsWith|indexOf|substring|replace|split|join|push|pop|slice|parse|hex|rgb|rgba|opacity|lighten|darken|abs|min|max|clamp|floor|ceil|round|sqrt|pow|sin|cos|tan|asin|acos|atan|atan2)\(`)
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			// Strip comments before scanning so directive mentions like
			// `// ERROR(check) "dyn..."` don't count as explicit dyn use.
			stripped := stripLineComments(src)
			if dynKeyword.Match(stripped) {
				t.Skip("source contains explicit `dyn` — backstop not applicable")
			}
			if stdlibDynMethod.Match(stripped) {
				t.Skip("source uses stdlib methods whose return types are implicitly dyn")
			}
			dirs, err := testutil.ParseDirectives(path)
			if err != nil {
				t.Fatalf("directives: %v", err)
			}
			if len(testutil.Filter(dirs, "parse")) > 0 {
				t.Skip("parse-error fixture")
			}
			if len(testutil.Filter(dirs, "check")) > 0 {
				t.Skip("check-error fixture — TypDyn during error recovery is expected")
			}
			doc, parseErr := parser.Parse(filepath.Base(path), src)
			if parseErr != nil {
				t.Skipf("parse: %v", parseErr)
			}
			cfg := &checker.Config{IsMain: true}
			for _, s := range doc.Stmts {
				if _, ok := s.(*ast.Import); ok {
					cfg.Resolver = newTestResolver()
					break
				}
			}
			pkg, _ := checker.Check(doc, cfg)
			if pkg == nil {
				return
			}
			report := func(where string, pos ast.Pos) {
				t.Errorf("inferred TypeDyn at %s (%s:%d:%d) — expected explicit annotation or an error",
					where, filepath.Base(path), pos.Line, pos.Column)
			}
			visit := func(label string, pos ast.Pos, typ *ir.Type) {
				if typ != nil && typ.Kind == ir.TypeDyn {
					report(label, pos)
				}
			}
			for _, v := range pkg.Vars {
				visit("package var "+v.Name, stmtPos(v.AST), v.Type)
			}
			for _, v := range pkg.Consts {
				visit("package const "+v.Name, stmtPos(v.AST), v.Type)
			}
			for _, fn := range pkg.Funcs {
				pos := ast.Pos{}
				if fn.AST != nil {
					pos = fn.AST.Pos
				}
				for _, p := range fn.Params {
					visit("func "+fn.Name+" param "+p.Name, pos, p.Type)
				}
				visit("func "+fn.Name+" return", pos, fn.Return)
			}
			for _, comp := range pkg.Components {
				pos := ast.Pos{}
				if comp.AST != nil {
					pos = comp.AST.Pos
				}
				for _, p := range comp.Props {
					visit("component "+comp.Name+" prop "+p.Name, pos, p.Type)
				}
				for _, v := range comp.Vars {
					vpos := pos
					if p := stmtPos(v.AST); p.Line != 0 {
						vpos = p
					}
					visit("component "+comp.Name+" var "+v.Name, vpos, v.Type)
				}
				for _, fn := range comp.Funcs {
					for _, p := range fn.Params {
						visit("component "+comp.Name+" func "+fn.Name+" param "+p.Name, pos, p.Type)
					}
					visit("component "+comp.Name+" func "+fn.Name+" return", pos, fn.Return)
				}
			}
			for _, sd := range pkg.Structs {
				pos := ast.Pos{}
				if sd.AST != nil {
					pos = sd.AST.Pos
				}
				for _, f := range sd.Fields {
					visit("struct "+sd.Name+" field "+f.Name, pos, f.Type)
				}
			}
		})
	}
}

// stmtPos extracts the Pos from an ast.Stmt that wraps a decl with a Pos field.
func stmtPos(s ast.Stmt) ast.Pos {
	switch x := s.(type) {
	case *ast.VarDecl:
		return x.Pos
	case *ast.ConstDecl:
		return x.Pos
	}
	return ast.Pos{}
}

// stripLineComments removes // line comments from source so they don't pollute
// the backstop's `dyn` keyword scan.
func stripLineComments(src []byte) []byte {
	var out []byte
	for len(src) > 0 {
		i := bytes.Index(src, []byte("//"))
		if i < 0 {
			out = append(out, src...)
			break
		}
		out = append(out, src[:i]...)
		eol := bytes.IndexByte(src[i:], '\n')
		if eol < 0 {
			break
		}
		src = src[i+eol:]
	}
	return out
}

// TestCheckProjectTestdata runs the v2 checker over all project-level testdata
// samples (testdata/*.sngl) that don't have parse errors.
func TestCheckProjectTestdata(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Skipf("v2 parse failed: %v", err)
			}
			// Run pre-check macro expansion and assert ERROR(expand) directives.
			expandDiags := expand.ExpandPre([]*ast.Document{doc})
			testutil.AssertDiagnostics(t, expandDiags, s.Errors, "expand")
			if s.ExpectsError("expand") {
				return // expansion errors; skip type-check
			}
			_, diags := checker.Check(doc, &checker.Config{IsMain: true})
			// Log errors but don't fail — project testdata uses v1 ERROR(check)
			// directives which may not match v2 checker messages.
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Logf("diagnostic: %s", d.Error())
				}
			}
			// Assert ERROR(lint) directives against warning-severity diagnostics.
			testutil.AssertDiagnostics(t, diags, s.Errors, "lint")
		})
	}
}

func TestAnonymousWindowHasEmptyName(t *testing.T) {
	src, err := os.ReadFile("../../testdata/window_anonymous_no_synthetic.sngl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := parser.Parse("window_anonymous_no_synthetic.sngl", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Error())
		}
	}
	if len(pkg.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(pkg.Windows))
	}
	if got := pkg.Windows[0].Name; got != "" {
		t.Fatalf("want empty Name, got %q", got)
	}
}

func TestCheckShapeType(t *testing.T) {
	// canvas and rect both have list<shape> ChildrenType — rect used inside canvas.
	expectNoErrors(t, `
import "internal://canvas"
component canvas(width float, height float) list<shape> {}
component rect(x float, y float, w float, h float) list<shape> {}
component myWidget() {
    canvas(width=400, height=300) {
        rect(x=10, y=10, w=100, h=50) {}
    }
}
`)
}

func TestCheckShapeTypeRejectsNonShape(t *testing.T) {
	expectError(t, `
import "internal://canvas"
component canvas(width float, height float) list<shape> {}
component notAShape() {}
component myWidget() {
    canvas(width=400, height=300) {
        notAShape() {}
    }
}
`, "expected shape component")
}

func TestCheckShape_StandaloneRejected(t *testing.T) {
	// shape must not be usable as a standalone type (field, param, var).
	expectError(t, `
component myWidget() {
    var bad shape = 0
}
`, "shape is only valid as a children type")
}

func TestCheckCanvasStdlib(t *testing.T) {
	expectNoErrors(t, `
component myWidget() {
    canvas(width=400px, height=300px) {
        rect(x=10.0, y=10.0, w=100.0, h=50.0, style=CanvasStyle{}) {}
        circle(cx=50.0, cy=50.0, r=30.0, style=CanvasStyle{}) {}
        canvasText(x=10.0, y=10.0, content="hello", style=CanvasStyle{}) {}
    }
}
`)
}
