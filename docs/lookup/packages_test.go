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

func TestStdlibPackages(t *testing.T) {
	pkgs := lookup.StdlibPackages()
	if len(pkgs) == 0 {
		t.Fatal("StdlibPackages returned empty list")
	}
	if pkgs[0].Path != "sngl" || pkgs[0].Kind != "library" {
		t.Errorf("first entry: got %+v, want sngl/library", pkgs[0])
	}
	var seenAndroid bool
	for _, p := range pkgs {
		if p.Path == "android" {
			seenAndroid = true
			if p.Kind != "platform" {
				t.Errorf("android Kind: got %q, want platform", p.Kind)
			}
		}
	}
	if !seenAndroid {
		t.Error("android missing from StdlibPackages")
	}
}

func TestAllDeclPages(t *testing.T) {
	pages := lookup.AllDeclPages()
	if len(pages) == 0 {
		t.Fatal("AllDeclPages returned no pages")
	}
	var (
		sawSnglType       bool
		sawSnglTypeMethod bool
		sawAndroid        bool
	)
	for _, p := range pages {
		if p.Href == "" {
			t.Errorf("DeclPage missing Href: %+v", p)
		}
		if !strings.HasPrefix(p.Href, "/docs/"+p.Pkg+"/") {
			t.Errorf("Href doesn't match pkg prefix: %+v", p)
		}
		// Stdlib components are skipped by AllDeclPages — the website renders
		// them via a hand-rolled rich loop so the generic per-decl path would
		// double-emit identical hrefs.
		if p.Pkg == "sngl" && p.Kind == "components" {
			t.Errorf("sngl/components page leaked into AllDeclPages: %+v", p)
		}
		if p.Pkg == "sngl" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "" {
			sawSnglType = true
			if p.Href != "/docs/sngl/types/color.html" {
				t.Errorf("color href: got %q", p.Href)
			}
		}
		if p.Pkg == "sngl" && p.Kind == "types" && p.Name == "color" && p.Ident2 == "darken" {
			sawSnglTypeMethod = true
			if p.Href != "/docs/sngl/types/color/darken.html" {
				t.Errorf("color.darken href: got %q", p.Href)
			}
		}
		if p.Pkg == "android" {
			sawAndroid = true
		}
	}
	if !sawSnglType {
		t.Error("missing sngl/types/color page")
	}
	if !sawSnglTypeMethod {
		t.Error("missing sngl/types/color/darken page")
	}
	if !sawAndroid {
		t.Error("no android pages emitted")
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
		{"circle", "sngl:draw", false, "component"},
		{"button", "sngl:std", false, "component"},
		{"Style", "sngl:std", false, "type"},
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
