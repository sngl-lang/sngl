package lookup_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/docs/lookup"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
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
	if len(res.Index.Components) == 0 && len(res.Index.PlatformTypes) == 0 && len(res.Index.Overrides) == 0 {
		t.Error("android index has no Components, Overrides, or PlatformTypes — expected at least one")
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

// The library and the targets are two id spaces, and the URL keeps them apart:
// sngl:html would otherwise land on the html platform's pages.
func TestDeclHrefSeparatesLibraryFromTargets(t *testing.T) {
	if got := lookup.DeclHref("sngl:ui", "components", "button"); got != "/docs/sngl/ui/components/button.html" {
		t.Errorf("sngl:ui button: got %q", got)
	}
	if got := lookup.DeclHref("sngl:ui/draw", "components", "circle"); got != "/docs/sngl/ui/draw/components/circle.html" {
		t.Errorf("sngl:ui/draw circle: got %q", got)
	}
	if got := lookup.DeclHref("html", "types", "Options"); got != "/docs/html/types/Options.html" {
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
			if p.Href != "/docs/sngl/ui/enums/Alignment.html" {
				t.Errorf("Alignment href: got %q", p.Href)
			}
		}
		if p.Pkg == "sngl:builtin" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "" {
			sawSnglType = true
			if p.Href != "/docs/sngl/builtin/types/color.html" {
				t.Errorf("color href: got %q", p.Href)
			}
		}
		if p.Pkg == "sngl:builtin" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "darken" {
			sawSnglTypeMethod = true
			if p.Href != "/docs/sngl/builtin/types/color/darken.html" {
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
	for _, f := range res.Index.Functions {
		switch f.Name {
		case "CanvasApplyStyle", "CanvasDrawRect", "Translate":
			t.Errorf("intrinsic %q from the internal tier is in the sngl index", f.Name)
		}
	}
}

// Naming one still resolves, as an internal Go package does.
func TestLookupInternalPackageByName(t *testing.T) {
	res, err := lookup.Lookup("sngl:internal/draw")
	if err != nil {
		t.Fatal(err)
	}
	if res.Index == nil || len(res.Index.Functions) == 0 {
		t.Fatal("sngl:internal/draw resolved to nothing")
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
	}{
		{"circle", "sngl:ui/draw", false, "component"},
		{"button", "sngl:ui", false, "component"},
		{"Style", "sngl:ui", false, "type"},
		{"color", "sngl:builtin", true, "type"},
	} {
		got := lookup.FindInLibrary(tc.name)
		if len(got) != 1 {
			t.Errorf("%s: got %d origins, want 1 (%v)", tc.name, len(got), got)
			continue
		}
		if got[0].Pkg != tc.pkg || got[0].Ambient != tc.ambient || got[0].Kind != tc.kind {
			t.Errorf("%s: got %+v; want {%s %v %s}", tc.name, got[0], tc.pkg, tc.ambient, tc.kind)
		}
		wantImport := `import . "` + tc.pkg + `"`
		if tc.ambient {
			wantImport = ""
		}
		if got[0].ImportLine() != wantImport {
			t.Errorf("%s: ImportLine = %q; want %q", tc.name, got[0].ImportLine(), wantImport)
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
