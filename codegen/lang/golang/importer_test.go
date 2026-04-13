package golang

import (
	"testing"
)

func TestGoImporter_Resolve(t *testing.T) {
	imp := &GoImporter{}
	decls, err := imp.Resolve("go://git.duckfam.us/jonathan/sngl/codegen/lang/golang/testdata/testpkg", ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Check structs
	if len(decls.Structs) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(decls.Structs))
	}
	s := decls.Structs[0]
	if s.Name != "Todo" {
		t.Errorf("struct name = %q, want %q", s.Name, "Todo")
	}
	if len(s.Fields) != 3 {
		t.Errorf("struct fields = %d, want 3", len(s.Fields))
	} else {
		checks := map[string]string{"id": "int", "title": "string", "done": "bool"}
		for _, f := range s.Fields {
			want, ok := checks[f.Name]
			if !ok {
				t.Errorf("unexpected field %q", f.Name)
				continue
			}
			if f.Type != want {
				t.Errorf("field %q type = %q, want %q", f.Name, f.Type, want)
			}
		}
	}

	// Check funcs and vars
	funcNames := map[string]bool{}
	for _, f := range decls.Funcs {
		funcNames[f.Name] = true
	}
	varNames := map[string]bool{}
	for _, v := range decls.Vars {
		varNames[v.Name] = true
	}

	// Should have SaveTodo, FormatDate, FetchAll
	for _, fn := range []string{"SaveTodo", "FormatDate", "FetchAll"} {
		if !funcNames[fn] {
			t.Errorf("missing extern func %q", fn)
		}
	}

	// Should have Count var
	if !varNames["Count"] {
		t.Error("missing extern var Count")
	}

	// Should NOT have unexported names
	if funcNames["privateFn"] {
		t.Error("unexported function should not be included")
	}
	if varNames["hidden"] {
		t.Error("unexported var should not be included")
	}

	// Check FormatDate has correct param/return types
	for _, f := range decls.Funcs {
		if f.Name == "FormatDate" {
			if len(f.ParamTypes) != 1 || f.ParamTypes[0] != "string" {
				t.Errorf("FormatDate params = %v, want [string]", f.ParamTypes)
			}
			if f.ReturnType != "string" {
				t.Errorf("FormatDate return = %q, want string", f.ReturnType)
			}
		}
		if f.Name == "SaveTodo" {
			if len(f.ParamTypes) != 1 || f.ParamTypes[0] != "testpkg.Todo" {
				t.Errorf("SaveTodo params = %v, want [testpkg.Todo]", f.ParamTypes)
			}
			if f.ReturnType != "" {
				t.Errorf("SaveTodo return = %q, want empty (void)", f.ReturnType)
			}
		}
		if f.Name == "FetchAll" {
			if f.ReturnType != "list:testpkg.Todo" {
				t.Errorf("FetchAll return = %q, want list:testpkg.Todo", f.ReturnType)
			}
		}
	}
}
