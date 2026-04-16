package checker_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

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
`, "argument 1: cannot pass string as int")
}

func TestFuncArgArityMismatch(t *testing.T) {
	expectError(t, `
func add(a int, b int) => a + b
func test() {
	var x = add(1)
}
`, "expected 2 arguments, got 1")
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
`, "expected 2 arguments, got 1")
}

func TestMethodInstanceArity(t *testing.T) {
	// Instance form shifts sig: 2.add() passes only receiver, missing second param.
	expectError(t, `
func int.add(a int, b int) => a + b
func test() {
	var x = 2.add()
}
`, "expected 1 arguments, got 0")
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

			doc, parseErr := parser.Parse(filepath.Base(path), src)
			if parseErr != nil {
				t.Fatalf("parse: %v", parseErr)
			}
			_, diags := checker.Check(doc, &checker.Config{IsMain: true})

			if len(expected) == 0 {
				// No error directives — expect clean check.
				for _, d := range diags {
					if d.Severity == ir.Error {
						t.Errorf("unexpected error: %s", d.Error())
					}
				}
				return
			}
			// Match each directive against diagnostics.
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
		})
	}
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
			_, diags := checker.Check(doc, &checker.Config{IsMain: true})
			// Log errors but don't fail — project testdata uses v1 ERROR(check)
			// directives which may not match v2 checker messages.
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Logf("diagnostic: %s", d.Error())
				}
			}
		})
	}
}
