package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// An intrinsic may be answered by either side of a build. A language emitter is
// the common case; a platform emitter is for an id whose native form is a call
// into the surface the platform renders onto, which only builds targeting that
// platform can emit at all.
//
// The 2D primitives are why this exists. They were registered against the
// language "js", which claimed every js build could draw — and gtk4 and android
// emit the same ids in Go and Kotlin, where registering per language would have
// claimed the same untrue thing for bubbletea and every other platform sharing
// those languages.
func TestPlatformEmitterAnswersWhereTheLanguageCannot(t *testing.T) {
	const id = "TestOnly.platformOwned"
	RegisterPlatformIntrinsic("testplat", id, func([]ir.Expr, func(ir.Expr) string) (string, []string) {
		return "drawn()", nil
	})

	call := &ir.Call{Func: &ir.Func{Name: "p", Intrinsic: id}}
	tr := func(ir.Expr) string { return "" }

	// The language alone cannot answer it.
	if _, _, ok := EmitIntrinsicCall("testlang", "", call, tr); ok {
		t.Error("a language with no emitter answered a platform-owned id")
	}
	// Nor can a different platform.
	if _, _, ok := EmitIntrinsicCall("testlang", "otherplat", call, tr); ok {
		t.Error("the wrong platform answered")
	}
	// The platform that owns it does.
	out, _, ok := EmitIntrinsicCall("testlang", "testplat", call, tr)
	if !ok || out != "drawn()" {
		t.Errorf("EmitIntrinsicCall = (%q, %v); want (\"drawn()\", true)", out, ok)
	}
}

// A platform may instead declare the whole library package it implements, for
// emitters that could not be an IntrinsicEmitter and should not be reshaped into
// one: gtk4's return []ir.Stmt and carry a pending style between calls, android's
// write lines into a stateful Compose context.
//
// Naming the package rather than the ids is what keeps it from drifting — a
// primitive added to that package is covered with nothing to update here. This
// is what let the completeness check stop exempting sngl:internal/draw by name,
// back when that package existed; it is also all that exercises the mechanism
// now, its only real user having gone with the drawing primitives.
func TestDeclaredPackageSatisfiesTheCompletenessCheck(t *testing.T) {
	const pkg = "sngl:testonly/surface"
	def := &ir.IntrinsicDef{Name: "TestOnly.declaredByPackage", Pkg: pkg}

	if AnyTargetImplements(def) {
		t.Fatal("an id nothing implements was reported as implemented")
	}
	DeclarePlatformImplements("testplat2", pkg)
	if !AnyTargetImplements(def) {
		t.Error("a platform declaring the package did not satisfy the check")
	}
	// The declaration is per package, so a sibling id in it is covered too.
	sibling := &ir.IntrinsicDef{Name: "TestOnly.addedLater", Pkg: pkg}
	if !AnyTargetImplements(sibling) {
		t.Error("an id added to a declared package is not covered; the declaration drifts")
	}
	// And it says nothing about any other package.
	other := &ir.IntrinsicDef{Name: "TestOnly.elsewhere", Pkg: "sngl:testonly/other"}
	if AnyTargetImplements(other) {
		t.Error("declaring one package covered another")
	}
}
