package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// A component with a timer must emit a schedule, and its tick body must go
// through the translator like any other imperative body.
//
// The timer is an `effect` over fynert.Every and the schedule it hands back,
// so what says the
// schedule is armed is the mount handler, and what says the gate still works is
// that `running` decides whether the bracket is described at all. The widget
// write is the second claim and is independent of the first: rendered by
// gc.EvalStmt alone it comes out as a raw `__n0.Value =`, which is not Fyne's
// API and does not compile.
func TestTimerEmitsTickerRuntime(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:time"
window {
    var seconds = 0
    var running = false
    timer(interval=1000ms, enabled=running, @tick { seconds += 1 })
    vbox {
        text(value="{seconds} s")
        button(text="Start", @click { running!! })
    }
}
`
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	// The platform's own capabilities rather than a hand-written pair: the
	// timer is an effect now, and a Caps literal that forgets NoEffects leaves
	// the node standing and nothing emits a schedule at all.
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := string(mem.Files()["model.go"])

	for _, snippet := range []string{
		"func (m *Model) __effect0_mount()",
		"fynert.Every(",
		// The schedule is the value, not an index into a registry: cancelling
		// is a method on what Every handed back.
		".Cancel()",
		"m.seconds += 1",
		// Tick body's reactive widget update must use the fyne widget API,
		// qualified with the receiver — not a raw, unqualified field write.
		"m.__n0.SetText(",
		// The gate is the effect's position: while `running` is false the
		// bracket describes no schedule, so nothing is armed to skip a tick.
		"if m.running {",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("generated model.go missing timer snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}
	if strings.Contains(out, "__n0.Value =") {
		t.Errorf("tick body emitted an untranslated raw field write (__n0.Value =):\n%s", out)
	}
}
