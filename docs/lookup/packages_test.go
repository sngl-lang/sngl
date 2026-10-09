package lookup_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/docs/lookup"

	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
)

func TestLookupPlatformAndroid(t *testing.T) {
	res, err := lookup.Lookup("android")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindIndex || res.Index == nil {
		t.Fatalf("kind: got %v, want KindIndex", res.Kind)
	}
	if res.Index.Title != "android" {
		t.Errorf("Title: got %q", res.Index.Title)
	}
	// Android platform exposes some Components and at minimum an Options struct.
	if len(res.Index.Components) == 0 && len(res.Index.Overrides) == 0 {
		t.Error("android index has no Components or Overrides — expected at least one")
	}
}

func TestLookupPlatformBubbletea(t *testing.T) {
	res, err := lookup.Lookup("bubbletea")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != lookup.KindIndex {
		t.Fatalf("kind: got %v", res.Kind)
	}
}

// One entry per package, each addressed by its import path. A single "sngl"
// entry standing for the whole library could not say which package a
// declaration came from.
func TestLibraryPackages(t *testing.T) {
	pkgs := lookup.LibraryPackages()
	if len(pkgs) == 0 {
		t.Fatal("LibraryPackages returned empty list")
	}
	byPath := map[string]lookup.PackageEntry{}
	for _, p := range pkgs {
		byPath[p.Path] = p
	}

	ui, ok := byPath["sngl:ui"]
	if !ok {
		t.Fatalf("sngl:ui missing; got %v", byPath)
	}
	if ui.Kind != "library" || ui.Title != "sngl:ui" {
		t.Errorf("ui entry: got %+v, want library/sngl:ui", ui)
	}
	if ui.Href != "/docs/sngl/ui/index.html" {
		t.Errorf("ui Href: got %q", ui.Href)
	}
	if ui.Depth != 0 {
		t.Errorf("ui Depth: got %d, want 0", ui.Depth)
	}

	// A nested package is its own entry, one level down.
	draw, ok := byPath["sngl:ui/draw"]
	if !ok {
		t.Fatalf("sngl:ui/draw missing")
	}
	if draw.Href != "/docs/sngl/ui/draw/index.html" || draw.Depth != 1 {
		t.Errorf("ui/draw entry: got %+v", draw)
	}

	if _, ok := byPath["sngl"]; ok {
		t.Error(`a merged "sngl" entry is back; every package is addressed by its own path`)
	}

	android, ok := byPath["android"]
	if !ok {
		t.Fatal("android missing from LibraryPackages")
	}
	if android.Kind != "platform" {
		t.Errorf("android Kind: got %q, want platform", android.Kind)
	}
	// A target keeps its bare name, which is also how a program names it.
	if android.Href != "/docs/android/index.html" {
		t.Errorf("android Href: got %q", android.Href)
	}
}

// The URL is the package path and the declaration's name -- no kind segment,
// since one name means one declaration within a package. The scheme stays as
// the first segment because the library and the targets are two id spaces:
// sngl:html would otherwise land on the html platform's pages.
func TestDeclHrefSeparatesLibraryFromTargets(t *testing.T) {
	if got := lookup.DeclHref("sngl:ui", "components", "button"); got != "/docs/sngl/ui/button.html" {
		t.Errorf("sngl:ui button: got %q", got)
	}
	if got := lookup.DeclHref("sngl:ui/draw", "components", "circle"); got != "/docs/sngl/ui/draw/circle.html" {
		t.Errorf("sngl:ui/draw circle: got %q", got)
	}
	if got := lookup.DeclHref("html", "types", "Options"); got != "/docs/html/Options.html" {
		t.Errorf("html Options: got %q", got)
	}
}

func TestAllDeclPages(t *testing.T) {
	pages := lookup.AllDeclPages()
	if len(pages) == 0 {
		t.Fatal("AllDeclPages returned no pages")
	}
	var (
		sawSnglType       bool
		sawUIDecl         bool
		sawSnglTypeMethod bool
		sawAndroid        bool
	)
	for _, p := range pages {
		if p.Href == "" {
			t.Errorf("DeclPage missing Href: %+v", p)
		}
		// A library package's pages sit under the scheme, a target's under its
		// bare name -- the two id spaces, kept apart.
		wantPrefix := "/docs/" + strings.ReplaceAll(p.Pkg, ":", "/") + "/"
		if !strings.HasPrefix(p.Href, wantPrefix) {
			t.Errorf("Href %q is not under %q", p.Href, wantPrefix)
		}
		if p.Pkg == "sngl" {
			t.Errorf(`a merged "sngl" page is back: %+v`, p)
		}
		// A library component's page is hand-rolled on the website, so the
		// generic loop must not emit a second one at the same href.
		if p.Kind == "components" && strings.HasPrefix(p.Pkg, "sngl:") {
			t.Errorf("library component page leaked into AllDeclPages: %+v", p)
		}
		if p.Pkg == "sngl:ui" && p.Kind == "enums" && p.Name == "Alignment" && p.Ident2 == "" {
			sawUIDecl = true
			if p.Href != "/docs/sngl/ui/Alignment.html" {
				t.Errorf("Alignment href: got %q", p.Href)
			}
		}
		if p.Pkg == "sngl:builtin" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "" {
			sawSnglType = true
			if p.Href != "/docs/sngl/builtin/color.html" {
				t.Errorf("color href: got %q", p.Href)
			}
		}
		if p.Pkg == "sngl:builtin" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "darken" {
			sawSnglTypeMethod = true
			if p.Href != "/docs/sngl/builtin/color/darken.html" {
				t.Errorf("color.darken href: got %q", p.Href)
			}
		}
		if p.Pkg == "android" {
			sawAndroid = true
		}
	}
	if !sawSnglType {
		t.Error("missing sngl:builtin types/color page")
	}
	if !sawSnglTypeMethod {
		t.Error("missing sngl:builtin types/color/darken page")
	}
	if !sawAndroid {
		t.Error("no android pages emitted")
	}
	if !sawUIDecl {
		t.Error("missing sngl:ui enums/Alignment page")
	}
}

// The compiler's own tier is reachable by name but is not part of the
// language a program is written in, so the merged library index must not
// advertise it — a reader looking up `sngl` should not meet ColorHex or
// CanvasApplyStyle beside `button`.
func TestLookupSnglOmitsInternalTier(t *testing.T) {
	res, err := lookup.Lookup("sngl")
	if err != nil {
		t.Fatal(err)
	}
	if res.Index == nil {
		t.Fatal("no index for sngl")
	}
	if strings.Contains(res.Index.Description, "internal/") {
		t.Errorf("description lists the internal tier: %q", res.Index.Description)
	}
	// Over Packages, which is what this index carries: `sngl` names the tree
	// rather than a package with members, so DeclIndex.Packages is set instead
	// of the declaration sections.
	//
	// It used to walk Index.Functions, which for this target is always empty —
	// so the loop asserted nothing at all, for any name. Two of the three it
	// named were gone or had never been declared, which is how that stayed
	// invisible: a leak would have had to be of a name nothing could emit.
	if len(res.Index.Packages) == 0 {
		t.Fatal("the sngl index lists no packages; this test would pass vacuously")
	}
	for _, p := range res.Index.Packages {
		if strings.HasPrefix(p.Path, "internal/") || strings.HasPrefix(p.Path, "sngl:internal/") {
			t.Errorf("the compiler's own tier is in the sngl index: %q", p.Path)
		}
	}
}

// Naming one still resolves, as an internal Go package does.
func TestLookupInternalPackageByName(t *testing.T) {
	// sngl:internal/ir rather than the draw package this used to name, which
	// is gone: its last two intrinsics went with the lowering that emitted
	// them. Asserting on Functions keeps the check real -- ir declares macros
	// and the flag enums, and an index with none of them resolved to nothing.
	res, err := lookup.Lookup("sngl:internal/ir")
	if err != nil {
		t.Fatal(err)
	}
	if res.Index == nil || len(res.Index.Macros)+len(res.Index.Enums) == 0 {
		t.Fatal("sngl:internal/ir resolved to nothing")
	}
}

// A bare name is not a lookup path: everything outside sngl:builtin needs
// an import before a program can write it, so resolving one has to report
// the package it came from.
func TestFindInLibrary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pkg     string
		ambient bool
		kind    string
		imp     string
	}{
		{"circle", "sngl:ui/draw", false, "component", `import draw "sngl:ui/draw"`},
		{"button", "sngl:ui", false, "component", `import ui "sngl:ui"`},
		{"Style", "sngl:ui", false, "type", `import ui "sngl:ui"`},
		{"color", "sngl:builtin", true, "type", ""},
	} {
		got := lookup.FindInLibrary(tc.name)
		if len(got) != 1 {
			t.Errorf("%s: got %d origins, want 1 (%v)", tc.name, len(got), got)
			continue
		}
		if got[0].Pkg != tc.pkg || got[0].Ambient != tc.ambient || got[0].Kind != tc.kind {
			t.Errorf("%s: got %+v; want {%s %v %s}", tc.name, got[0], tc.pkg, tc.ambient, tc.kind)
		}
		if got[0].ImportLine() != tc.imp {
			t.Errorf("%s: ImportLine = %q; want %q", tc.name, got[0].ImportLine(), tc.imp)
		}
	}
	if got := lookup.FindInLibrary("StrUpper"); len(got) != 0 {
		t.Errorf("the compiler's own tier is reachable by bare name: %v", got)
	}
	if got := lookup.FindInLibrary("nosuchdeclaration"); len(got) != 0 {
		t.Errorf("unknown name resolved to %v", got)
	}
}

// Every page the site generates is written to the href it carries, so two
// pages sharing one href are one page overwriting the other -- which docsgen
// reports as an output path collision rather than silently emitting the last.
// A stdlib component overridden by five platforms is the shape that produced
// it: five declarations of one `sngl.<name>`, one page.
func TestAllDeclPageHrefsAreUnique(t *testing.T) {
	seen := map[string]lookup.DeclPage{}
	for _, p := range lookup.AllDeclPages() {
		if prev, dup := seen[p.Href]; dup {
			t.Errorf("href %q emitted twice: %s/%s and %s/%s",
				p.Href, prev.Pkg, prev.Name, p.Pkg, p.Name)
			continue
		}
		seen[p.Href] = p
	}
}
