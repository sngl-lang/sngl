package js

import (
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

func resolveOrFail(t *testing.T, uri, dir string) *ir.NativeImport {
	t.Helper()
	imp, err := (&JSImporter{}).Resolve(uri, dir)
	if err != nil {
		t.Fatalf("Resolve(%q, %q): %v", uri, dir, err)
	}
	return imp
}

func findFunc(imp *ir.NativeImport, name string) *ir.Func {
	for _, f := range imp.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func findStruct(imp *ir.NativeImport, name string) *ir.StructDef {
	for _, s := range imp.Structs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func TestSimpleFunctions(t *testing.T) {
	dir := testdataDir(t)
	imp := resolveOrFail(t, "js://./simple", dir)

	add := findFunc(imp, "add")
	if add == nil {
		t.Fatalf("add not found in %#v", imp.Funcs)
	}
	if add.Foreign.Unusable != "" {
		t.Errorf("add unusable: %s", add.Foreign.Unusable)
	}
	if len(add.Params) != 2 {
		t.Errorf("add params: %d, want 2", len(add.Params))
	}
	if add.Return == nil || add.Return.Kind != ir.TypeFloat {
		t.Errorf("add return: %v, want float", add.Return)
	}
	if add.IsAsync {
		t.Errorf("add IsAsync = true, want false")
	}

	greet := findFunc(imp, "greet")
	if greet == nil {
		t.Fatalf("greet not found")
	}
	if greet.Params[0].Type.Kind != ir.TypeString {
		t.Errorf("greet param: %v", greet.Params[0].Type)
	}

	ft := findFunc(imp, "fetchTitle")
	if ft == nil {
		t.Fatalf("fetchTitle not found")
	}
	if !ft.IsAsync {
		t.Errorf("fetchTitle IsAsync = false, want true")
	}
	if ft.Return == nil || ft.Return.Kind != ir.TypeString {
		t.Errorf("fetchTitle return: %v, want string (Promise unwrapped)", ft.Return)
	}
}

func TestInterface(t *testing.T) {
	dir := testdataDir(t)
	imp := resolveOrFail(t, "js://./iface", dir)

	user := findStruct(imp, "User")
	if user == nil {
		t.Fatalf("User struct not found")
	}
	if len(user.Fields) != 4 {
		t.Errorf("User fields: %d, want 4", len(user.Fields))
	}
	emailField := user.Fields[2] // by source order
	if emailField.Name != "email" {
		t.Errorf("Fields[2].Name = %q, want email", emailField.Name)
	}
	if emailField.Type.Kind != ir.TypeOption {
		t.Errorf("email type kind = %v, want Option", emailField.Type.Kind)
	}
	tagsField := user.Fields[3]
	if tagsField.Type.Kind != ir.TypeList {
		t.Errorf("tags type kind = %v, want List", tagsField.Type.Kind)
	}

	mk := findFunc(imp, "makeUser")
	if mk == nil {
		t.Fatalf("makeUser not found")
	}
	if mk.Return == nil || mk.Return.Decl != user {
		t.Errorf("makeUser return wrong: %v", mk.Return)
	}
}

func TestJSON(t *testing.T) {
	dir := testdataDir(t)
	imp := resolveOrFail(t, "js://./json/data.json", dir)
	if len(imp.Vars) != 1 {
		t.Fatalf("Vars: %d, want 1", len(imp.Vars))
	}
	v := imp.Vars[0]
	if v.Name != "data" {
		t.Errorf("Var.Name = %q, want data", v.Name)
	}
	if v.Type == nil || v.Type.Kind != ir.TypeStruct {
		t.Fatalf("Var.Type = %v, want struct", v.Type)
	}
	root := v.Type.Decl.(*ir.StructDef)
	if len(root.Fields) != 4 {
		t.Errorf("root fields: %d, want 4", len(root.Fields))
	}
	fieldByName := func(name string) *ir.StructField {
		for _, f := range root.Fields {
			if f.Name == name {
				return f
			}
		}
		return nil
	}
	if fieldByName("title").Type.Kind != ir.TypeString {
		t.Errorf("title not string")
	}
	if fieldByName("count").Type.Kind != ir.TypeInt {
		t.Errorf("count not int")
	}
	if fieldByName("tags").Type.Kind != ir.TypeList {
		t.Errorf("tags not list")
	}
	if fieldByName("nested").Type.Kind != ir.TypeStruct {
		t.Errorf("nested not struct")
	}
}

func TestNodeModulesResolution(t *testing.T) {
	dir := testdataDir(t)
	imp := resolveOrFail(t, "js://foo", dir)
	pluck := findFunc(imp, "pluck")
	if pluck == nil {
		t.Fatalf("pluck not found")
	}
	if pluck.Params[0].Type.Kind != ir.TypeString {
		t.Errorf("pluck arg type = %v, want string", pluck.Params[0].Type)
	}
}
