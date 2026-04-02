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

	// Check data (functions + vars)
	funcNames := map[string]bool{}
	varNames := map[string]bool{}
	for _, d := range decls.Data {
		if d.IsFunc {
			funcNames[d.Name] = true
		} else {
			varNames[d.Name] = true
		}
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
	for _, d := range decls.Data {
		if d.Name == "FormatDate" {
			if len(d.ParamTypes) != 1 || d.ParamTypes[0] != "string" {
				t.Errorf("FormatDate params = %v, want [string]", d.ParamTypes)
			}
			if d.ReturnType != "string" {
				t.Errorf("FormatDate return = %q, want string", d.ReturnType)
			}
		}
		if d.Name == "SaveTodo" {
			if len(d.ParamTypes) != 1 || d.ParamTypes[0] != "testpkg.Todo" {
				t.Errorf("SaveTodo params = %v, want [testpkg.Todo]", d.ParamTypes)
			}
			if d.ReturnType != "" {
				t.Errorf("SaveTodo return = %q, want empty (void)", d.ReturnType)
			}
		}
		if d.Name == "FetchAll" {
			if d.ReturnType != "list:testpkg.Todo" {
				t.Errorf("FetchAll return = %q, want list:testpkg.Todo", d.ReturnType)
			}
		}
	}
}
