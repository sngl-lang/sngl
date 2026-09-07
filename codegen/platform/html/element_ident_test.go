package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkedPkgForTest checks src with the html platform as its target.
func checkedPkgForTest(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("w.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{&Generator{}},
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %v", d)
		}
	}
	return pkg
}

// firstElementDecl is the declaration the first raw element of a checked
// package resolved to. It is how codegen reaches one as well: the node carries
// it, and nothing holds one for the package.
func firstElementDecl(t *testing.T, pkg *ir.Package) *ir.Component {
	t.Helper()
	var found *ir.Component
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if n, ok := s.(*ir.NodeInst); ok && isElement(n.Component) {
			found = n.Component
			return ir.SkipAll
		}
		return nil
	})
	if found == nil {
		t.Fatal("no raw element in the checked package")
	}
	return found
}

// This package names html's own vocabulary in Go — the tag prop, the attrs
// prop, and the shape of a DOM event name — rather than reading the marks back
// off the declaration. Something has to hold the two spellings together, and
// this is it.
func TestElementDeclarationMatchesWhatHTMLAssumes(t *testing.T) {
	const src = "import . \"sngl:ui\"\nimport . \"sngl:app\"\nimport html \"sngl:platform/html\"\n\nwindow(\"t\") {\n    html.div {}\n}\n"
	decl := firstElementDecl(t, checkedPkgForTest(t, src))

	if got := decl.WildcardInto; got != tagProp {
		t.Errorf("element binds its tag into %q, but this package reads %q", got, tagProp)
	}
	if elementProp(decl, tagProp) == nil {
		t.Errorf("element declares no %q prop", tagProp)
	}

	// attrs is the collector, and the only prop that is one: elementProp
	// excludes it by name rather than by asking which props collect.
	var collectors []string
	for _, p := range decl.Props {
		if p != nil && p.Wildcard != "" {
			collectors = append(collectors, p.Name)
		}
	}
	if len(collectors) != 1 || collectors[0] != attrsProp {
		t.Errorf("element's collecting props are %v, want [%s]", collectors, attrsProp)
	}

	// isDOMEventName stands in for the event wildcard's pattern.
	for _, e := range decl.Events {
		if e == nil || e.Wildcard == "" {
			continue
		}
		for _, name := range []string{"click", "pointerdown", "x9", "scrollEnd", "Click", ""} {
			if ir.MatchesWildcard(e.Wildcard, name) != isDOMEventName(name) {
				t.Errorf("event wildcard %q and isDOMEventName disagree about %q", e.Wildcard, name)
			}
		}
	}
}

// A component of the program's own is not a raw element, whatever it declares
// —- isElement takes both of html's props, so one of them alone is not enough.
func TestIsElementRejectsAUserComponent(t *testing.T) {
	if isElement(&ir.Component{Name: "mine", Props: []*ir.Prop{{Name: tagProp}}}) {
		t.Error("a component declaring only a tag prop is not html's element")
	}
	if isElement(&ir.Component{Name: "mine", Props: []*ir.Prop{{Name: attrsProp}}}) {
		t.Error("a component declaring only an attrs prop is not html's element")
	}
	if isElement(nil) {
		t.Error("nil is not html's element")
	}
}
