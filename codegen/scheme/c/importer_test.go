package c

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
	"modernc.org/cc/v4"
)

// mustParseSnippet parses a small C snippet and returns the AST for type inspection.
func mustParseSnippet(t *testing.T, src string) *cc.AST {
	t.Helper()
	abi, err := cc.NewABI(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	ast, err := cc.Translate(
		&cc.Config{ABI: abi},
		[]cc.Source{
			{Name: "<predefined>", Value: predefinedPreamble},
			{Name: "test.h", Value: src},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ast
}

func TestMapCType_Int(t *testing.T) {
	ast := mustParseSnippet(t, "int x;")
	got := mapCType(ast.Int, ast, nil)
	if got == nil || got.Kind != ir.TypeInt {
		t.Errorf("int → %v, want TypeInt", got)
	}
}

func TestMapCType_Float(t *testing.T) {
	ast := mustParseSnippet(t, "double x;")
	got := mapCType(ast.Double, ast, nil)
	if got == nil || got.Kind != ir.TypeFloat {
		t.Errorf("double → %v, want TypeFloat", got)
	}
}

func TestMapCType_CharPtr(t *testing.T) {
	ast := mustParseSnippet(t, "char *s;")
	// char* maps to TypeString
	for _, nodes := range ast.Scope.Nodes {
		for _, node := range nodes {
			d, ok := node.(*cc.Declarator)
			if !ok || d.Name() != "s" {
				continue
			}
			got := mapCType(d.Type(), ast, nil)
			if got == nil || got.Kind != ir.TypeString {
				t.Errorf("char* → %v, want TypeString", got)
			}
			return
		}
	}
	t.Fatal("no 's' declarator found")
}

func TestMapCType_VoidPtr(t *testing.T) {
	ast := mustParseSnippet(t, "void *p;")
	for _, nodes := range ast.Scope.Nodes {
		for _, node := range nodes {
			d, ok := node.(*cc.Declarator)
			if !ok || d.Name() != "p" {
				continue
			}
			got := mapCType(d.Type(), ast, nil)
			// void* → TypeDyn with Meta "unsafe.Pointer"
			if got == nil || got.Kind != ir.TypeDyn {
				t.Errorf("void* → %v, want TypeDyn(unsafe.Pointer)", got)
				return
			}
			meta, _ := got.Meta.(string)
			if meta != "unsafe.Pointer" {
				t.Errorf("void* Meta = %q, want %q", meta, "unsafe.Pointer")
			}
			return
		}
	}
	t.Fatal("no 'p' declarator found")
}

func TestMapCType_StructPtr(t *testing.T) {
	ast := mustParseSnippet(t, "struct Foo { int x; }; struct Foo *p;")
	structs := map[string]*ir.StructDef{}
	for _, nodes := range ast.Scope.Nodes {
		for _, node := range nodes {
			d, ok := node.(*cc.Declarator)
			if !ok || d.Name() != "p" {
				continue
			}
			got := mapCType(d.Type(), ast, structs)
			// T* → optional<ref<T>>
			if got == nil || got.Kind != ir.TypeOption {
				t.Errorf("Foo* → %v, want TypeOption", got)
				return
			}
			inner := got.Elems[0] // ref<Foo>
			if inner.Kind != ir.TypeRef {
				t.Errorf("Foo* inner → %v, want TypeRef", inner)
				return
			}
			return
		}
	}
	t.Fatal("no 'p' declarator found")
}

func TestMapCType_Union(t *testing.T) {
	ast := mustParseSnippet(t, "union U { int a; float b; }; union U *p;")
	structs := map[string]*ir.StructDef{}
	for _, nodes := range ast.Scope.Nodes {
		for _, node := range nodes {
			d, ok := node.(*cc.Declarator)
			if !ok || d.Name() != "p" {
				continue
			}
			got := mapCType(d.Type(), ast, structs)
			// union* → no panic, returns TypeDyn or nil
			_ = got
			return
		}
	}
	t.Fatal("no 'p' declarator found")
}

func TestCImporter_ResolveFile(t *testing.T) {
	dir := t.TempDir()
	headerPath := filepath.Join(dir, "test.h")
	if err := os.WriteFile(headerPath, []byte(`
int add(int a, int b);
struct Point { int x; int y; };
`), 0600); err != nil {
		t.Fatal(err)
	}

	imp := &CImporter{}
	ni, err := imp.Resolve("c://"+headerPath, dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should have one function: add
	var addFn *ir.Func
	for _, fn := range ni.Funcs {
		if fn.Name == "add" {
			addFn = fn
			break
		}
	}
	if addFn == nil {
		t.Fatalf("func 'add' not found; got funcs: %v", funcNames(ni.Funcs))
	}
	if addFn.NativePkg != "C" {
		t.Errorf("NativePkg = %q, want %q", addFn.NativePkg, "C")
	}
	if addFn.NativeName != "C.add" {
		t.Errorf("NativeName = %q, want %q", addFn.NativeName, "C.add")
	}

	// Should have the Point struct
	var pointSd *ir.StructDef
	for _, sd := range ni.Structs {
		if sd.Name == "Point" {
			pointSd = sd
			break
		}
	}
	if pointSd == nil {
		t.Fatal("Point struct not found in NativeImport")
	}
	if pointSd.Native != "C.Point" {
		t.Errorf("Point.Native = %q, want %q", pointSd.Native, "C.Point")
	}
}

func TestCImporter_PkgconfigMissing(t *testing.T) {
	imp := &CImporter{}
	_, err := imp.Resolve("c://pkg:nonexistent-lib-xyz", ".")
	if err == nil {
		t.Fatal("expected error for missing pkg-config lib, got nil")
	}
	if !strings.Contains(err.Error(), "pkg-config") {
		t.Errorf("error = %q; want it to mention pkg-config", err.Error())
	}
}

func funcNames(fns []*ir.Func) []string {
	names := make([]string, len(fns))
	for i, f := range fns {
		names[i] = f.Name
	}
	return names
}
