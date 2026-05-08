package gtk4_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestPlatformRegistered(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	if gen.PlatformIdentifier() != "gtk4" {
		t.Errorf("identifier = %q, want gtk4", gen.PlatformIdentifier())
	}
}

func TestResolve_KnownWidget(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	sym := gen.Resolve("GtkButton")
	if sym == nil {
		t.Skip("GIR file not available on this machine — skipping")
	}
	comp, ok := sym.(*ir.Component)
	if !ok {
		t.Fatalf("expected *ir.Component, got %T", sym)
	}
	if comp.Name != "GtkButton" {
		t.Errorf("Name = %q, want GtkButton", comp.Name)
	}
}

func TestResolve_Unknown(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Skip("gtk4 not registered")
	}
	if sym := gen.Resolve("NotAWidget"); sym != nil {
		t.Errorf("expected nil for unknown widget, got %v", sym)
	}
}
