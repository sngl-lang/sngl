package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/v2/checker"
	"git.duckfam.us/jonathan/sngl/internal/v2/parser"
)

func parse(t *testing.T, src string) *checker.Package {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == checker.Error {
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

func TestExprTypeMap(t *testing.T) {
	pkg := parse(t, `
func add(a int, b int) => a + b
`)
	// The function body expression should be in the TypeMap.
	if len(pkg.TypeMap) == 0 {
		t.Error("TypeMap is empty")
	}
}

func TestPurityAnalysis(t *testing.T) {
	pkg := parse(t, `
var count int
func pure(a int, b int) => a + b
`)
	for _, f := range pkg.Funcs {
		if f.Name == "pure" && f.Purity != checker.PurityPure {
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
		if d.Severity == checker.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error diagnostic for unknown type")
	}
}
