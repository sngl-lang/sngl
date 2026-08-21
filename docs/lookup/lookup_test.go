package lookup_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/docs/lookup"
)

func TestLookupStdlibIndex(t *testing.T) {
	res, err := lookup.Lookup("sngl")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindIndex {
		t.Fatalf("kind: got %v, want KindIndex", res.Kind)
	}
	if !res.Index.Library {
		t.Error("Library should be true")
	}
	if res.Index.Title != "sngl" {
		t.Errorf("Title: got %q", res.Index.Title)
	}

	// color should appear as a TypeEntry with methods folded in.
	var color *lookup.TypeEntry
	for i := range res.Index.Types {
		if res.Index.Types[i].Name == "color" {
			color = &res.Index.Types[i]
			break
		}
	}
	if color == nil {
		t.Fatal("color type missing from index")
	}
	if len(color.Methods) < 6 {
		t.Errorf("color: want ≥6 methods, got %d", len(color.Methods))
	}
	for _, m := range color.Methods {
		if m.FullName != "color."+m.ShortName {
			t.Errorf("method FullName mismatch: %q vs short %q", m.FullName, m.ShortName)
		}
	}

	// Primitive receivers (int, float, string, list) should appear as synthesized TypeEntry.
	primitives := map[string]bool{"int": false, "float": false, "string": false, "list": false}
	for _, tp := range res.Index.Types {
		if _, ok := primitives[tp.Name]; ok {
			primitives[tp.Name] = true
			if len(tp.Methods) == 0 {
				t.Errorf("primitive %q has no methods", tp.Name)
			}
		}
	}
	for name, seen := range primitives {
		if !seen {
			t.Errorf("primitive %q missing from Types", name)
		}
	}

	// Free functions bucket must stay separate — today stdlib has none, but the
	// slice shouldn't carry receiver-dotted names.
	for _, f := range res.Index.Functions {
		if filepath.Base(f.Name) != f.Name {
			t.Errorf("free function carries a dot: %q", f.Name)
		}
	}
}

func TestLookupStdlibComponent(t *testing.T) {
	res, err := lookup.Lookup("sngl", "button")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindComponent {
		t.Fatalf("kind: got %v, want KindComponent", res.Kind)
	}
	if res.Component.Name != "button" {
		t.Errorf("Name: got %q", res.Component.Name)
	}
	if res.Component.Schema == nil {
		t.Error("Schema should be populated for stdlib components")
	}
}

func TestLookupStdlibType(t *testing.T) {
	res, err := lookup.Lookup("sngl", "color")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindType {
		t.Fatalf("kind: got %v, want KindType", res.Kind)
	}
	if res.Type.Name != "color" || res.Type.Struct == nil {
		t.Errorf("Type detail incomplete: %+v", res.Type)
	}
	if len(res.Type.Methods) == 0 {
		t.Error("color should carry methods")
	}
}

func TestLookupStdlibMethod(t *testing.T) {
	res, err := lookup.Lookup("sngl", "color", "darken")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindFunc {
		t.Fatalf("kind: got %v, want KindFunc", res.Kind)
	}
	if res.Func.Name != "color.darken" {
		t.Errorf("Name: got %q, want color.darken", res.Func.Name)
	}
}

func TestLookupStdlibDottedLeaf(t *testing.T) {
	// Single dotted ident should resolve the same way as a two-ident chain.
	res, err := lookup.Lookup("sngl", "color.darken")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindFunc || res.Func.Name != "color.darken" {
		t.Fatalf("got %+v", res)
	}
}

func TestLookupStdlibField(t *testing.T) {
	res, err := lookup.Lookup("sngl", "Style", "padding")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindField {
		t.Fatalf("kind: got %v, want KindField", res.Kind)
	}
	if res.Field.Type != "Style" || res.Field.Name != "padding" {
		t.Errorf("Field: %+v", res.Field)
	}
}

func TestLookupStdlibEnumMember(t *testing.T) {
	res, err := lookup.Lookup("sngl", "FlexDirection", "row")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindMember {
		t.Fatalf("kind: got %v, want KindMember", res.Kind)
	}
	if res.Member.Enum != "FlexDirection" || res.Member.Name != "row" {
		t.Errorf("Member: %+v", res.Member)
	}
}

func TestLookupStdlibEvent(t *testing.T) {
	res, err := lookup.Lookup("sngl", "button", "click")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindProp {
		t.Fatalf("kind: got %v, want KindProp", res.Kind)
	}
	if res.Prop.Event == "" {
		t.Error("Prop.Event should carry payload type for stdlib events")
	}
}

func TestLookupPrimitiveReceiver(t *testing.T) {
	// string / int / float / list each have an empty struct declaration in
	// the stdlib (so doc comments have a home), and method funcs hang off
	// the same receiver.
	res, err := lookup.Lookup("sngl", "string")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindType {
		t.Fatalf("kind: got %v, want KindType", res.Kind)
	}
	if res.Type.Name != "string" {
		t.Errorf("Name: got %q", res.Type.Name)
	}
	if res.Type.Doc == "" {
		t.Error("string should carry doc from its empty struct decl")
	}
	if len(res.Type.Methods) == 0 {
		t.Error("string should carry methods")
	}

	// Walk into a method.
	res, err = lookup.Lookup("sngl", "string", "upper")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindFunc || res.Func.Name != "string.upper" {
		t.Errorf("expected string.upper func, got %+v", res)
	}
}

func TestLookupNotFound(t *testing.T) {
	_, err := lookup.Lookup("./nonexistent-dir")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, lookup.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	_, err = lookup.Lookup("sngl", "NotAThing")
	if !errors.Is(err, lookup.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestIndexInTempDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "widgets")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "w.sngl"), []byte(`component Counter(count int) { text { value = "x" } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.sngl"), []byte(`import w "./widgets"
component main { w.Counter(count = 1) }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	refs := lookup.IndexIn(dir)
	if len(refs) < 3 {
		t.Fatalf("want at least Current+Stdlib+local, got %v", refs)
	}
	if refs[0].Kind != lookup.PackageCurrent {
		t.Errorf("first entry should be current, got %+v", refs[0])
	}
	if refs[1].Kind != lookup.PackageLibrary || refs[1].Path != "sngl" {
		t.Errorf("second entry should be stdlib, got %+v", refs[1])
	}
	var widgets *lookup.PackageRef
	for i := range refs {
		if refs[i].Alias == "w" || refs[i].Title == "w" {
			widgets = &refs[i]
			break
		}
	}
	if widgets == nil || widgets.Kind != lookup.PackageLocal {
		t.Errorf("widgets alias not found: %+v", refs)
	}
}

func TestLookupInDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sngl"), []byte(`struct Point {
    x int
    y int
}
component main {
    text { value = "x" }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := lookup.LookupIn(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindIndex {
		t.Fatalf("kind: got %v", res.Kind)
	}
	var foundPoint bool
	for _, tp := range res.Index.Types {
		if tp.Name == "Point" {
			foundPoint = true
		}
	}
	if !foundPoint {
		t.Error("Point struct missing from index")
	}

	// Walking into the struct field.
	res, err = lookup.LookupIn(dir, ".", "Point", "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindField || res.Field.Name != "x" {
		t.Errorf("expected field x, got %+v", res)
	}
}
