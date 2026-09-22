package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// A node-attached handler that lowering promotes to a Func on a component
// other than the app root must still be emitted as a Model method.
//
// ctx.AllFuncs() covers pkg-level and main-component funcs only, and a
// window's promoted handlers come from its own Funcs slice — a plain
// non-main component has neither route. The fyne override for `button`
// declares `@click`, so every button gets an attachment
// (`w.OnTapped = m.<h>`) whether or not the caller supplied a handler; with
// no method of that name emitted, the file does not compile. testdata's
// focus_order_loop.sngl is exactly this shape.
func TestNonMainComponentPromotedHandlersAreEmitted(t *testing.T) {
	// No `component main`: RootDecl() then finds none, so this component
	// is emitted through renderIRComponentMethod and its own Funcs slice is
	// the only place its promoted handlers live. That is the arrangement
	// testdata/focus_order_loop.sngl has.
	src := `
import . "sngl:ui"
component focusLoop node {
    vbox {
        button(text="before", focusable=true)
        button(text="after", focusable=true)
    }
}
`
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "fyne", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := string(mem.Files()["model.go"])

	// Every `X.OnTapped = m.<name>` must have a `func (m *Model) <name>(`.
	var missing []string
	for line := range strings.SplitSeq(out, "\n") {
		_, rhs, ok := strings.Cut(strings.TrimSpace(line), ".OnTapped = m.")
		if !ok {
			continue
		}
		if !strings.Contains(out, "func (m *Model) "+rhs+"(") {
			missing = append(missing, rhs)
		}
	}
	if len(missing) == 0 && !strings.Contains(out, ".OnTapped = m.") {
		t.Fatalf("fixture attached no handler at all, so it asserts nothing:\n%s", out)
	}
	if len(missing) > 0 {
		t.Errorf("handlers attached with no Model method emitted: %v\n--- generated ---\n%s", missing, out)
	}
}
