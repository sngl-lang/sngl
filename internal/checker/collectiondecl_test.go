package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A collection type carries the #[builtin]-marked declaration it was resolved
// from, whichever way it was spelled. Every one of these is built through
// ir.ListOf/MapOf, so attaching it anywhere short of there -- the annotation
// path only, say -- leaves the rest nil, which is worse than none of them
// having it.
func TestCollectionTypesCarryTheirDeclaration(t *testing.T) {
	const src = `component main ui {
    var annotated list<int> = [1]
    var inferred = [1, 2]
    var nested list<list<string>> = [["a"]]
    var m map<string, int> = {}
    var mapped = inferred.map(func(x int) => x * 2)
    text(value=string(annotated.length))
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %v", d.Error())
		}
	}
	if len(pkg.Components) != 1 {
		t.Fatalf("got %d components, want 1", len(pkg.Components))
	}

	wantKind := map[string]ir.BuiltinKind{
		"annotated": ir.BuiltinList,
		"inferred":  ir.BuiltinList,
		"nested":    ir.BuiltinList,
		"m":         ir.BuiltinMap,
		"mapped":    ir.BuiltinList,
	}
	seen := map[string]bool{}
	for _, v := range pkg.Components[0].Vars {
		want, ok := wantKind[v.Name]
		if !ok {
			continue
		}
		seen[v.Name] = true
		sd, isStruct := v.Type.Decl.(*ir.StructDef)
		if !isStruct {
			t.Errorf("%s (%s) carries no declaration", v.Name, v.Type)
			continue
		}
		if sd.Builtin != want {
			t.Errorf("%s (%s) declaration is marked %q, want %q", v.Name, v.Type, sd.Builtin, want)
		}
		if len(sd.TypeParams) == 0 {
			t.Errorf("%s (%s) declaration has no type parameters", v.Name, v.Type)
		}
	}
	for name := range wantKind {
		if !seen[name] {
			t.Errorf("no var %q in the checked component", name)
		}
	}

	// A type is not more declared at the top level.
	nested := pkg.Components[0].Vars
	for _, v := range nested {
		if v.Name != "nested" {
			continue
		}
		inner := v.Type.Elems[0]
		if _, ok := inner.Decl.(*ir.StructDef); !ok {
			t.Errorf("the inner list of %s carries no declaration", v.Type)
		}
	}
}
