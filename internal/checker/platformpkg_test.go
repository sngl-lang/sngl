package checker_test

import (
	"errors"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestPlatformPackageDiagnosticsAreReported pins the reason platform packages
// load through libPkg: the path they used before type-checked them with a
// nested Check whose diagnostics were dropped on the floor, so a platform
// package could be arbitrarily broken and every build still succeeded.
func TestPlatformPackageDiagnosticsAreReported(t *testing.T) {
	const platSource = `
import sngl "sngl://std"

component Wrap() {
    sngl.text(value=nosuchthing)
}
`
	const userSource = `
import . "sngl://std"
import "sngl://platforms/extstub"

component main {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(userSource))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, extStubConfig(t, platSource))
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "nosuchthing") {
			return
		}
	}
	t.Errorf("broken platform package produced no error mentioning nosuchthing; got %v", diags)
}

// TestPlatformPackageSharesStdlibIdentity pins the other half: the platform
// package and the program must resolve one `text` component, not two. The old
// path ran a nested Check that loaded its own sngl://std, so a platform
// wrapper's body pointed at a different *ir.Component than user code held —
// which is why the extension merge had to bypass it entirely.
//
// It also pins Pkg stamping, which that nested Check left empty: a platform
// declaration must know the package it came from.
func TestPlatformPackageSharesStdlibIdentity(t *testing.T) {
	const platSource = `
import sngl "sngl://std"

component Wrap() {
    sngl.text(value="x")
}
`
	const userSource = `
import . "sngl://std"
import "sngl://platforms/extstub"

component main {
    text(value="hi")
    platform extstub {
        Wrap {}
    }
}
`
	doc, err := parser.Parse("main.sngl", []byte(userSource))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, extStubConfig(t, platSource))
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected error: %s", d.Error())
		}
	}

	sym, _ := pkg.Symbols.LookupComponent("text")
	userText, _ := sym.(*ir.Component)
	if userText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}

	var wrap *ir.Component
	for _, imp := range pkg.Imports {
		if imp.Path != "sngl://platforms/extstub" || imp.Pkg == nil {
			continue
		}
		for _, comp := range imp.Pkg.Components {
			if comp.Name == "Wrap" {
				wrap = comp
			}
		}
	}
	if wrap == nil {
		t.Fatal("platform package contributed no Wrap component")
	}
	if wrap.Pkg != "sngl://platforms/extstub" {
		t.Errorf("Wrap.Pkg = %q; want sngl://platforms/extstub", wrap.Pkg)
	}
	if len(wrap.Body) == 0 {
		t.Fatal("Wrap has no checked body")
	}
	inst, ok := wrap.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("Wrap body[0] = %T; want *ir.NodeInst", wrap.Body[0])
	}
	if inst.Component != userText {
		t.Errorf("text inside the platform package is %p; the program holds %p — two identities", inst.Component, userText)
	}

	// The `platform extstub { ... }` block resolves through
	// buildPlatformPkgScope, a second entry point into the platform package.
	// It has to reach the memoized instance: before, it built its own.
	var main *ir.Component
	for _, comp := range pkg.Components {
		if comp.Name == "main" {
			main = comp
		}
	}
	if main == nil {
		t.Fatal("component main missing")
	}
	var blockWrap *ir.Component
	for _, stmt := range main.Body {
		pf, ok := stmt.(*ir.PlatformFilter)
		if !ok {
			continue
		}
		for _, s := range pf.Body {
			if n, ok := s.(*ir.NodeInst); ok {
				blockWrap = n.Component
			}
		}
	}
	if blockWrap == nil {
		t.Fatal("platform block resolved no component")
	}
	if blockWrap != wrap {
		t.Errorf("Wrap in a platform block is %p; the imported package holds %p — loaded twice", blockWrap, wrap)
	}
}

// unavailStubPlatform is registered but reports itself unusable here, as gtk4
// does with no GIR file installed.
type unavailStubPlatform struct{}

func (unavailStubPlatform) PlatformIdentifier() string { return "unavailstub" }
func (unavailStubPlatform) Description() string        { return "unavailable test stub" }
func (unavailStubPlatform) Resolve(string) ir.Symbol   { return nil }
func (unavailStubPlatform) Unavailable() error {
	return errors.New("no widget metadata; pass --opt stub=PATH")
}

// TestImportUnavailablePlatformIsAnError keeps the degradation actionable.
// Loading the package would report every declaration in it as undefined, so
// the import says what is missing instead.
func TestImportUnavailablePlatformIsAnError(t *testing.T) {
	const userSource = `
import . "sngl://std"
import "sngl://platforms/unavailstub"

component main {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(userSource))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{unavailStubPlatform{}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "unavailstub") && strings.Contains(d.Msg, "--opt stub=PATH") {
			return
		}
	}
	t.Errorf("importing an unavailable platform reported no actionable error; got %v", diags)
}
