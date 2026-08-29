package javascript

import (
	"testing"
	"testing/fstest"

	"git.duckfam.us/jonathan/sngl/codegen"
	jsscheme "git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	"git.duckfam.us/jonathan/sngl/ir"
)

// jsStructDef loads one interface out of a TypeScript module the way the
// checker does, so the test reasons about a real importer record rather than a
// hand-built one.
func jsStructDef(t *testing.T, src, name string) *ir.StructDef {
	t.Helper()
	fsys := fstest.MapFS{"lib/index.ts": &fstest.MapFile{Data: []byte(src)}}
	ni, err := (&jsscheme.JSImporter{}).ResolveFS("./lib", fsys, "")
	if err != nil {
		t.Fatalf("resolving js:./lib: %v", err)
	}
	for _, sd := range ni.Structs {
		if sd.Name == name {
			return sd
		}
	}
	t.Fatalf("no struct %q in %v", name, ni.Structs)
	return nil
}

// A struct read out of a JavaScript module is written with the property names
// that module declared: the importer lowered the leading capital to reach a
// SNGL field name, and the module still reads the capitalised one.
func TestStructLitUsesTypeScriptPropertyNames(t *testing.T) {
	sd := jsStructDef(t, "export interface Point { X: number; Y: number }\n", "Point")
	jc := NewIRContext(codegen.NewExprCtx(nil))
	lit := &ir.StructLit{
		Def:    sd,
		Fields: []ir.FieldInit{{Name: "x"}, {Name: "y"}},
	}
	if got, want := jc.StructLit(lit, []string{"1", "2"}), "{X: 1, Y: 2}"; got != want {
		t.Errorf("StructLit = %s, want %s", got, want)
	}
}

// A go: struct records native names too, and they are Go's. Nothing in a
// generated page reads them — no Go runs in a browser — so the page has to keep
// spelling the fields the SNGL way, which is what the rest of it reads.
func TestStructLitKeepsSnglNamesForOtherSchemes(t *testing.T) {
	type goTypeID struct{ Path, Name string }
	sd := &ir.StructDef{
		Name:    "Entry",
		Foreign: ir.Foreign{Origin: goTypeID{Path: "example.com/api", Name: "Entry"}},
		Fields: []*ir.StructField{
			{Name: "title", Foreign: ir.Foreign{Name: "Title"}},
			{Name: "href", Foreign: ir.Foreign{Name: "Href"}},
		},
	}
	jc := NewIRContext(codegen.NewExprCtx(nil))
	lit := &ir.StructLit{
		Def:    sd,
		Fields: []ir.FieldInit{{Name: "title"}, {Name: "href"}},
	}
	if got, want := jc.StructLit(lit, []string{`"b"`, `"/b"`}), `{title: "b", href: "/b"}`; got != want {
		t.Errorf("StructLit = %s, want %s", got, want)
	}
}

// A SNGL enum member is its own name in generated JS. The checker leaves a
// placeholder in Value today, so once that placeholder is filled in the only
// thing keeping this true is the scheme test: a member reached through an enum
// no JavaScript importer declared is never erased to its value.
func TestEnumMemberKeepsSnglNameForOtherSchemes(t *testing.T) {
	ed := &ir.EnumDef{
		Name: "Color",
		Members: []*ir.EnumMember{
			{Name: "Red", Value: &ir.Literal{Value: "0", Type: &ir.Type{Kind: ir.TypeInt}}},
			{Name: "Green", Value: &ir.Literal{Value: "1", Type: &ir.Type{Kind: ir.TypeInt}}},
		},
	}
	if src, ok := nativeEnumMemberJS(&ir.Type{Kind: ir.TypeEnum, Decl: ed}, "Green"); ok {
		t.Errorf("nativeEnumMemberJS = %s, want no erasure", src)
	}
}
