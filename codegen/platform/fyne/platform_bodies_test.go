package fyne

import (
	"sort"
	"strings"
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
// component must carry a `component sngl.X { platform fyne { ... } }` body,
// and that body must implement something.
//
// The emptiness check is the point. mergePlatformExtensions reserves
// PlatformBodies[platform] before checking the block, so an override written
// as `platform fyne {}` records a *present* entry that renders nothing — a
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
		body, ok := comp.PlatformBodies["fyne"]
		if !ok {
			missingBody = append(missingBody, name)
			continue
		}
		if len(body) == 0 {
			emptyBody = append(emptyBody, name)
		}
	}
	if len(missingNode) > 0 {
		t.Fatalf("fixture did not instantiate %v — the source, not the platform, is wrong", missingNode)
	}
	if len(missingBody) > 0 {
		t.Errorf("no PlatformBodies[\"fyne\"] entry for %v", missingBody)
	}
	if len(emptyBody) > 0 {
		t.Errorf("PlatformBodies[\"fyne\"] is present but empty for %v", emptyBody)
	}
	if n := len(stdlibComponentsUnderFyne); n != 34 {
		t.Errorf("stdlibComponentsUnderFyne has %d entries, want the 34 stdlib components", n)
	}
}

// TestEveryFyneBodyLowersToADeclaredWidget closes the other half: an override
// body is only worth having if the intrinsic it instantiates is one the Go
// table can emit. Every component reachable from a fyne override body that
// carries a fyne #[intrinsic] id must resolve through widgetFor.
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
		ids := fyneIntrinsicIDs(comp.PlatformBodies["fyne"])
		if len(ids) == 0 {
			t.Errorf("%s: fyne body instantiates no fyne intrinsic", name)
			continue
		}
		for _, id := range ids {
			used[id] = true
			if _, ok := fyneWidgets[id]; !ok {
				t.Errorf("%s: fyne body uses intrinsic %q, which fyneWidgets does not declare", name, id)
			}
		}
	}

	// The reverse: a table row nothing reaches is a row nothing tests.
	var unused []string
	for id := range fyneWidgets {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("fyneWidgets rows no override reaches: %v", unused)
	}
}

// fyneIntrinsicIDs collects the fyne intrinsic ids of every component
// instantiated anywhere in stmts.
func fyneIntrinsicIDs(stmts []ir.Stmt) []string {
	var out []string
	codegen.WalkVisualTree(stmts, func(n *ir.NodeInst, _ int) bool {
		if n.Component == nil {
			return false
		}
		if id, ok := strings.CutPrefix(n.Component.Intrinsic, intrinsicPrefix); ok {
			out = append(out, id)
		}
		return false
	})
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
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: plats, Languages: langs})
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
