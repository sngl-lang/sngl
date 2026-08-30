package golang

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestGoImporter_Resolve(t *testing.T) {
	imp := &GoImporter{}
	ni, err := imp.Resolve("git.duckfam.us/jonathan/sngl/codegen/scheme/golang/testdata/testpkg", ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	structs := map[string]*ir.StructDef{}
	for _, s := range ni.Structs {
		structs[s.Name] = s
	}
	funcs := map[string]*ir.Func{}
	for _, f := range ni.Funcs {
		funcs[f.Name] = f
	}
	vars := map[string]*ir.Var{}
	for _, v := range ni.Vars {
		vars[v.Name] = v
	}

	// Todo struct checks.
	todo, ok := structs["Todo"]
	if !ok {
		t.Fatal("missing struct Todo")
	}
	wantFields := map[string]ir.TypeKind{"id": ir.TypeInt, "title": ir.TypeString, "done": ir.TypeBool}
	if len(todo.Fields) != len(wantFields) {
		t.Errorf("Todo fields = %d, want %d", len(todo.Fields), len(wantFields))
	}
	for _, f := range todo.Fields {
		if want := wantFields[f.Name]; f.Type == nil || f.Type.Kind != want {
			t.Errorf("field %q type = %v, want %v", f.Name, f.Type, want)
		}
		if f.Foreign.Unusable != "" {
			t.Errorf("field %q unexpectedly unusable: %s", f.Name, f.Foreign.Unusable)
		}
	}

	// Bag: Bad field unusable, struct itself usable.
	bag, ok := structs["Bag"]
	if !ok {
		t.Fatal("missing struct Bag")
	}
	bagFields := map[string]*ir.StructField{}
	for _, f := range bag.Fields {
		bagFields[f.Name] = f
	}
	if okF := bagFields["ok"]; okF == nil || okF.Foreign.Unusable != "" {
		t.Errorf("Bag.ok should be usable, got %+v", okF)
	}
	if bad := bagFields["bad"]; bad == nil || bad.Foreign.Unusable == "" {
		t.Error("Bag.bad should be unusable")
	}

	// Unexported should not appear.
	if _, ok := funcs["privateFn"]; ok {
		t.Error("unexported function should not be included")
	}
	if _, ok := vars["hidden"]; ok {
		t.Error("unexported var should not be included")
	}

	// FormatDate: single-return, string/string, usable, param name preserved.
	fd, ok := funcs["FormatDate"]
	if !ok {
		t.Fatal("missing func FormatDate")
	}
	if fd.Foreign.Unusable != "" {
		t.Errorf("FormatDate.Foreign.Unusable = %q, want empty", fd.Foreign.Unusable)
	}
	if len(fd.Params) != 1 || fd.Params[0].Name != "d" || fd.Params[0].Type.Kind != ir.TypeString {
		t.Errorf("FormatDate.Params = %+v, want [{d string}]", fd.Params)
	}
	if fd.Return == nil || fd.Return.Kind != ir.TypeString {
		t.Errorf("FormatDate.Return = %v, want string", fd.Return)
	}
	if fd.Foreign.Name != "testpkg.FormatDate" {
		t.Errorf("FormatDate.Foreign.Name = %q", fd.Foreign.Name)
	}

	// SaveTodo: void return, param is the Todo struct (referential).
	st, ok := funcs["SaveTodo"]
	if !ok {
		t.Fatal("missing func SaveTodo")
	}
	if st.Return != nil {
		t.Errorf("SaveTodo.Return = %v, want nil", st.Return)
	}
	if len(st.Params) != 1 || st.Params[0].Type == nil || st.Params[0].Type.Decl != todo {
		t.Errorf("SaveTodo.Params[0] type should point to Todo struct, got %+v", st.Params[0].Type)
	}

	// FetchAll: list<Todo>.
	fa, ok := funcs["FetchAll"]
	if !ok {
		t.Fatal("missing func FetchAll")
	}
	if fa.Return == nil || fa.Return.Kind != ir.TypeList || len(fa.Return.Elems) != 1 || fa.Return.Elems[0].Decl != todo {
		t.Errorf("FetchAll.Return = %+v, want list of Todo", fa.Return)
	}

	// WithCtx: ctx stripped, HasContextArg set.
	wc, ok := funcs["WithCtx"]
	if !ok {
		t.Fatal("missing func WithCtx")
	}
	if !wc.HasContextArg {
		t.Error("WithCtx.HasContextArg should be true")
	}
	if len(wc.Params) != 1 || wc.Params[0].Name != "msg" || wc.Params[0].Type.Kind != ir.TypeString {
		t.Errorf("WithCtx.Params = %+v", wc.Params)
	}

	// MaybeFail: (T, error) unwrapped.
	mf := funcs["MaybeFail"]
	if mf == nil || !mf.HasErrorReturn {
		t.Fatal("MaybeFail.HasErrorReturn should be true")
	}
	if mf.Return == nil || mf.Return.Kind != ir.TypeString {
		t.Errorf("MaybeFail.Return = %v, want string", mf.Return)
	}

	// WithCtxAndErr: both flags set; return is Todo.
	both := funcs["WithCtxAndErr"]
	if both == nil || !both.HasContextArg || !both.HasErrorReturn {
		t.Fatalf("WithCtxAndErr flags: %+v", both)
	}
	if both.Return == nil || both.Return.Decl != todo {
		t.Errorf("WithCtxAndErr.Return should be Todo, got %+v", both.Return)
	}

	// MultiReturn and ReturnsMap: unusable.
	if mr := funcs["MultiReturn"]; mr == nil || mr.Foreign.Unusable == "" {
		t.Error("MultiReturn should be unusable")
	}
	if rm := funcs["ReturnsMap"]; rm == nil || rm.Foreign.Unusable == "" {
		t.Error("ReturnsMap should be unusable")
	}

	// Count var usable.
	count, ok := vars["Count"]
	if !ok {
		t.Error("missing var Count")
	} else if count.Foreign.Unusable != "" {
		t.Errorf("Count.Foreign.Unusable = %q, want empty", count.Foreign.Unusable)
	}

	// Interface field/param: exposed as dyn, still usable.
	carrier, ok := structs["Carrier"]
	if !ok {
		t.Fatal("missing struct Carrier")
	}
	if len(carrier.Fields) != 1 {
		t.Fatalf("Carrier fields = %d, want 1", len(carrier.Fields))
	}
	h := carrier.Fields[0]
	if h.Type.Kind != ir.TypeDyn || h.Foreign.Unusable != "" {
		t.Errorf("Carrier.handler type=%v unusable=%q, want dyn/empty", h.Type, h.Foreign.Unusable)
	}
	wi, ok := funcs["WithIface"]
	if !ok {
		t.Fatal("missing func WithIface")
	}
	if wi.Foreign.Unusable != "" {
		t.Errorf("WithIface.Foreign.Unusable = %q", wi.Foreign.Unusable)
	}
	if len(wi.Params) != 1 || wi.Params[0].Type.Kind != ir.TypeDyn {
		t.Errorf("WithIface.Params = %+v, want dyn", wi.Params)
	}
}
