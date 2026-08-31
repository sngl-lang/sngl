package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// `list` and `map` are declared in lib/builtin like any other built-in --
// `#[builtin("list")] struct list<T> {}` with generic methods written against
// the type parameters. The mark used to be read for its kind and the
// declaration then dropped, so `list<int>` was {Kind: TypeList, Elems: [int]}
// and nothing downstream could reach the declaration or its parameters. Every
// list type is built through ir.ListOf, which is why every one of these
// spellings has to carry it -- attaching it only where an annotation is
// resolved would be worse than leaving it nil.
func TestCollectionTypesCarryTheirDeclaration(t *testing.T) {
	const src = `component main {
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

	// The element type of a nested list is built the same way, so it carries
	// the declaration too -- a type is not more declared at the top level.
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
