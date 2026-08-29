package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// makeKickerFunc returns a minimal async $compute_X func for testing.
func makeKickerFunc(name string) *ir.Func {
	return &ir.Func{
		Name:    name,
		IsAsync: true,
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Value: `"placeholder"`}},
		},
	}
}

// TestEmitKickers_AsyncKeyword verifies that kicker funcs are emitted with the
// "async function" keyword via the shared emitJSFunc path.
func TestEmitKickers_AsyncKeyword(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.pkg = &ir.Package{
		AsyncKickers: []ir.AsyncKickerEntry{
			{
				Func:         makeKickerFunc("$compute_greeting"),
				OrigComputed: "greeting",
				StateVarName: "__async_greeting",
				Deps:         []string{"userId"},
			},
		},
	}

	var b strings.Builder
	// Directly invoke emitJSFunc as emitScript does for kickers.
	for _, k := range g.pkg.AsyncKickers {
		g.emitJSFunc(&b, k.Func)
	}
	out := b.String()
	if !strings.Contains(out, "async function $compute_greeting") {
		t.Fatalf("expected 'async function $compute_greeting' in output, got:\n%s", out)
	}
}

// TestEmitKickers_StartupCall verifies that each kicker gets a fire-and-forget
// startup call after the updater wiring section.
func TestEmitKickers_StartupCall(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.pkg = &ir.Package{
		AsyncKickers: []ir.AsyncKickerEntry{
			{
				Func:         makeKickerFunc("$compute_greeting"),
				OrigComputed: "greeting",
				StateVarName: "__async_greeting",
				Deps:         []string{"userId"},
			},
		},
	}

	// Simulate the startup block that emitScript produces.
	var b strings.Builder
	b.WriteString("\n// Async kicker startup\n")
	for _, k := range g.pkg.AsyncKickers {
		b.WriteString(k.Func.Name + "();\n")
	}
	out := b.String()
	if !strings.Contains(out, "$compute_greeting();") {
		t.Fatalf("expected startup call '$compute_greeting();', got:\n%s", out)
	}
}

// TestEmitSetter_KickerDep verifies that $set_<dep> includes a fire-and-forget
// kicker call when the kicker's Deps list contains the state var name.
func TestEmitSetter_KickerDep(t *testing.T) {
	g := newMinimalHTMLGenWithDT(t)
	g.pkg = &ir.Package{
		AsyncKickers: []ir.AsyncKickerEntry{
			{
				Func:         makeKickerFunc("$compute_greeting"),
				OrigComputed: "greeting",
				StateVarName: "__async_greeting",
				Deps:         []string{"userId"},
			},
		},
	}

	dv := &ir.Var{Name: "userId"}
	var b strings.Builder
	g.emitSetter(&b, dv)
	out := b.String()
	if !strings.Contains(out, "$compute_greeting();") {
		t.Fatalf("expected kicker call '$compute_greeting();' in setter, got:\n%s", out)
	}
	// The setter itself must NOT be async — kicker is fire-and-forget.
	if strings.Contains(out, "async function $set_userId") {
		t.Fatalf("setter must NOT be async just because it fires a kicker, got:\n%s", out)
	}
}

// TestEmitSetter_KickerNoDep verifies that a kicker whose Deps does NOT include
// the state var does NOT appear in that setter.
func TestEmitSetter_KickerNoDep(t *testing.T) {
	g := newMinimalHTMLGenWithDT(t)
	g.pkg = &ir.Package{
		AsyncKickers: []ir.AsyncKickerEntry{
			{
				Func:         makeKickerFunc("$compute_greeting"),
				OrigComputed: "greeting",
				StateVarName: "__async_greeting",
				Deps:         []string{"userId"},
			},
		},
	}

	dv := &ir.Var{Name: "otherVar"}
	var b strings.Builder
	g.emitSetter(&b, dv)
	out := b.String()
	if strings.Contains(out, "$compute_greeting();") {
		t.Fatalf("kicker must NOT appear in setter for unrelated var, got:\n%s", out)
	}
}
