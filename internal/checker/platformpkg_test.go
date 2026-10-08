package checker_test

import (
	"errors"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// TestPlatformPackageDiagnosticsAreReported pins the reason platform packages
// load through libPkg: the path they used before type-checked them with a
// nested Check whose diagnostics were dropped on the floor, so a platform
// package could be arbitrarily broken and every build still succeeded.
func TestPlatformPackageDiagnosticsAreReported(t *testing.T) {
	const platSource = `
import sngl "sngl:ui"

const component Wrap() sngl.node {
    sngl.text(value=nosuchthing)
}
`
	const userSource = `
import . "sngl:ui"
import "sngl:platform/extstub"

component main node {
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
// path ran a nested Check that loaded its own sngl:ui, so a platform
// wrapper's body pointed at a different *ir.Component than user code held —
// which is why the extension merge had to bypass it entirely.
//
// It also pins Pkg stamping, which that nested Check left empty: a platform
// declaration must know the package it came from.
func TestPlatformPackageSharesStdlibIdentity(t *testing.T) {
	const platSource = `
import sngl "sngl:ui"

const component Wrap() sngl.node {
    sngl.text(value="x")
}
`
	const userSource = `
import . "sngl:ui"
import "sngl:platform/extstub"

component main node {
    text(value="hi")
    extstub.Wrap {}
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

	sym, _ := pkg.Symbols.LookupRootComponent("text")
	userText, _ := sym.(*ir.Component)
	if userText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}

	var wrap *ir.Component
	for _, imp := range pkg.Imports {
		if imp.Path != "sngl:platform/extstub" || imp.Pkg == nil {
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
	if wrap.Pkg != "sngl:platform/extstub" {
		t.Errorf("Wrap.Pkg = %q; want sngl:platform/extstub", wrap.Pkg)
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

	// Naming the package's component from user code resolves through the
	// namespace, a second entry point into the platform package. It has to
	// reach the memoized instance: before, it built its own.
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
		if n, ok := stmt.(*ir.NodeInst); ok && n.Component != nil && n.Component.Name == "Wrap" {
			blockWrap = n.Component
		}
	}
	if blockWrap == nil {
		t.Fatal("extstub.Wrap resolved no component")
	}
	if blockWrap != wrap {
		t.Errorf("Wrap named from user code is %p; the imported package holds %p — loaded twice", blockWrap, wrap)
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
import . "sngl:ui"
import "sngl:platform/unavailstub"

component main node {
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
