package fyne

import (
	"sort"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// stdlibComponentsUnderFyne is every stdlib component this platform is
// responsible for, one per line of the source below.
//
// The list is written out rather than derived, so it catches an override that
// went missing and NOT a component added to sngl://std -- adding one there with
// no override anywhere leaves every test here green. Making that a failure
// wants the bodyless-declaration syntax in #123, which turns "a target must
// implement this" into something the compiler can see rather than something
// each platform's test restates.
var stdlibComponentsUnderFyne = []string{
	"vbox", "hbox", "stack", "scroll", "spacer", "card", "splitview",
	"modal", "drawer", "popover", "tooltip",
	"tabs", "toolbar", "menubar", "menu",
	"table", "tree", "radio",
	"text", "badge", "chip", "divider", "avatar", "spinner", "progress", "datepicker",
	"button", "link", "image", "checkbox", "toggle", "select",
	"input", "textarea",
}

// allComponentsSrc instantiates every name in stdlibComponentsUnderFyne so the
// checked IR carries a NodeInst pointing at each stdlib *ir.Component.
const allComponentsSrc = `
import . "sngl://std"
import "sngl://platforms/fyne"
output { go { fyne() } }
component main {
    vbox {
        hbox {}
        stack {}
        scroll { text(value="in scroll") }
        spacer()
        card {}
        splitview {}
        modal {}
        drawer {}
        popover {}
        tooltip { text(value="tipped") }
        tabs {}
        toolbar {}
        menubar()
        menu()
        table()
        tree()
        radio()
        text(value="t")
        badge(value="b")
        chip(label="c")
        divider()
        avatar(initials="AB")
        spinner(label="s")
        progress(value=0.5)
        datepicker(placeholder="pick")
        button(text="go")
        link(href="/x", text="l")
        image(src="/i.png")
        checkbox(label="c")
        toggle(label="t")
        select(options=["a"])
        input(placeholder="p")
        textarea(placeholder="p", rows=3)
    }
}
`

// TestEveryStdlibComponentHasAFyneBody is bar one of the redesign: each stdlib
// component must carry a `component sngl.X { ... }` body in fyne.sngl,
// and that body must implement something.
//
// The emptiness check is the point. mergePlatformExtensions reserves
// Overrides[platform] before checking the block, so an override written
// with an empty body records a *present* entry that renders nothing — a
// node whose stdlib name reaches codegen, matches no widget, and is dropped
// while its AppendChild is still emitted (issue #120's failure mode). Presence
// alone is therefore not the assertion; a non-empty statement list is.
func TestEveryStdlibComponentHasAFyneBody(t *testing.T) {
	pkg := checkAllComponents(t)

	seen := map[string]*ir.Component{}
	codegen.WalkVisualTree(pkg.Components[0].Body, func(n *ir.NodeInst, _ int) bool {
		if n.Component != nil {
			seen[n.Name] = n.Component
		}
		return false
	})

	var missingNode, missingBody, emptyBody []string
	for _, name := range stdlibComponentsUnderFyne {
		comp, ok := seen[name]
		if !ok {
			missingNode = append(missingNode, name)
			continue
		}
		body, ok := comp.Overrides["fyne"]
		if !ok {
			missingBody = append(missingBody, name)
			continue
		}
		if len(body.Stmts) == 0 {
			emptyBody = append(emptyBody, name)
		}
	}
	if len(missingNode) > 0 {
		t.Fatalf("fixture did not instantiate %v — the source, not the platform, is wrong", missingNode)
	}
	if len(missingBody) > 0 {
		t.Errorf("no Overrides[\"fyne\"] entry for %v", missingBody)
	}
	if len(emptyBody) > 0 {
		t.Errorf("Overrides[\"fyne\"] is present but empty for %v", emptyBody)
	}
	if n := len(stdlibComponentsUnderFyne); n != 34 {
		t.Errorf("stdlibComponentsUnderFyne has %d entries, want the 34 stdlib components", n)
	}
}

// TestEveryFyneBodyLowersToADeclaredWidget closes the other half: an override
// body is only worth having if what it instantiates carries a Spec the Go
// emitter can build a widget from.
//
// The check runs over the *checked* tree, before inlining, so an override's
// body names a widget component (Label, VBox …) rather than the primitive that
// widget's own body instantiates. Following the chain to a primitive and
// decoding the Spec there is what proves the override reaches something
// buildable — and it is the same walk a user's own widget declaration would be
// followed by, since nothing here knows which of the two it is looking at.
func TestEveryFyneBodyLowersToADeclaredWidget(t *testing.T) {
	pkg := checkAllComponents(t)

	seen := map[string]*ir.Component{}
	codegen.WalkVisualTree(pkg.Components[0].Body, func(n *ir.NodeInst, _ int) bool {
		if n.Component != nil {
			seen[n.Name] = n.Component
		}
		return false
	})

	used := map[string]bool{}
	for _, name := range stdlibComponentsUnderFyne {
		comp := seen[name]
		if comp == nil {
			t.Fatalf("fixture did not instantiate %q", name)
		}
		prims := fynePrimitiveNodes(comp.Overrides["fyne"].Stmts)
		if len(prims) == 0 {
			t.Errorf("%s: fyne body reaches no fyne primitive", name)
			continue
		}
		for _, n := range prims {
			used[fynePrimitive(n.Component)] = true
			if _, err := specFromProps(n.Name, nodeProps(n)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}

	// The reverse: a primitive no override reaches is one nothing tests.
	var unused []string
	for _, id := range []string{"Widget", "Container", "Wrapper"} {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("fyne primitives no override reaches: %v", unused)
	}
}

// fynePrimitiveNodes collects every fyne primitive instantiation reachable
// from stmts, following a non-primitive component into its own body — which is
// how a widget declaration (`component Label { Widget(spec=...) }`) is reached
// from an override that names it.
func fynePrimitiveNodes(stmts []ir.Stmt) []*ir.NodeInst {
	var out []*ir.NodeInst
	seen := map[*ir.Component]bool{}
	var walk func([]ir.Stmt)
	walk = func(s []ir.Stmt) {
		codegen.WalkVisualTree(s, func(n *ir.NodeInst, _ int) bool {
			if n.Component == nil {
				return false
			}
			if fynePrimitive(n.Component) != "" {
				out = append(out, n)
				return false
			}
			if !seen[n.Component] {
				seen[n.Component] = true
				walk(n.Component.Body)
			}
			return false
		})
	}
	walk(stmts)
	return out
}

// nodeProps is the node's props by name, the shape specFromProps decodes. In a
// real build these arrive as the assignments lowering emits after CreateNode;
// here they are read straight off the checked node.
func nodeProps(n *ir.NodeInst) map[string]ir.Expr {
	out := map[string]ir.Expr{}
	for _, p := range n.Props {
		out[p.Name] = p.Value
	}
	return out
}

func checkAllComponents(t *testing.T) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(allComponentsSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("fyne"); p != nil {
		plats = append(plats, p)
	} else {
		t.Fatal("fyne platform not registered")
	}
	var langs []ir.Language
	if l := codegen.LookupLang("go"); l != nil {
		langs = append(langs, l)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: plats, Languages: langs, Targets: []ir.StaticTarget{{Platform: "fyne", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("%s: %s", d.Pos, d.Msg)
		}
	}
	if pkg == nil || len(pkg.Components) == 0 {
		t.Fatal("checker returned no components")
	}
	return pkg
}
