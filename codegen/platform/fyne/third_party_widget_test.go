package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// thirdPartyWidgetSrc wraps a Fyne widget from a Go module that is not Fyne's
// and that this compiler has never heard of, entirely in SNGL.
//
// Every fact the emitter needs is in the `Spec`: the constructor to call, what
// to call it with, the Go type it returns, the import path — which is the one
// no hardcoded table could ever have held — the setter behind the value prop,
// and the callback field, Go signature and value parameter behind the event.
//
// It is deliberately written the way a user would write it, against the same
// three primitives fyne.sngl's own widgets use, with no `#[intrinsic]` of its
// own and nothing registered on the Go side.
const thirdPartyWidgetSrc = `
import . "sngl://std"
import "sngl://platforms/fyne"

var reading int = 42

component Gauge(:level int, @change ChangeEvent) {
    fyne.Widget(
        spec=fyne.Spec{
            new="fynegauge.NewGauge",
            args=[fyne.Arg{raw="0"}],
            goType="*fynegauge.Gauge",
            imports=["github.com/example/fyne-gauge/fynegauge"],
            setters=[fyne.Setter{prop="number", call="SetValue"}],
            handlers=[fyne.Handler{on="change", field="OnValueChanged", signature="func(v int)", param="v"}],
        },
        number=level,
        @change { change() },
    ) {}
}

component main {
    vbox {
        Gauge(:level=reading)
    }
}
`

// TestThirdPartyWidgetIsWrappedInSNGLAlone is the bar for the redesign: a Fyne
// widget the compiler has no Go knowledge of must be usable after writing SNGL
// and nothing else.
//
// Each assertion names one fact that used to live in a Go table
// (widgets.go's GoFn, GoType, CtorArgs, Props, Events) or in a Go map of
// selectors to import paths, and now travels in the declaration.
func TestThirdPartyWidgetIsWrappedInSNGLAlone(t *testing.T) {
	out := generateFyneGo(t, thirdPartyWidgetSrc)

	for _, tc := range []struct {
		what string
		want string
	}{
		// The import path. No table in this package holds it, and the
		// selector "fynegauge" resolves to it only through the Spec.
		{"third-party import", `"github.com/example/fyne-gauge/fynegauge"`},
		// Constructor and its declared argument.
		{"constructor", "fynegauge.NewGauge(0)"},
		// The Go type, as the Model field's declared type.
		{"widget field type", "*fynegauge.Gauge"},
		// The value prop reaching its declared setter.
		{"setter", ".SetValue(m.reading)"},
		// The event reaching its declared callback field.
		{"callback field", ".OnValueChanged = m."},
		// The callback's Go signature, which is the widget's and not the
		// SNGL event's — the handler is promoted with the params the Spec
		// named.
		{"callback signature", "func (m *Model) __n0_change_handler(v int)"},
		// The Spec's `param`, naming the value inside that signature: a
		// `:level` binding writes back from the callback's own parameter, so
		// a widget the compiler has never heard of takes part in two-way
		// binding on the strength of its declaration alone. Matched with its
		// following line so it cannot be satisfied by the generated
		// SetReading accessor, whose body is the same assignment.
		{"two-way write-back", "\tm.reading = v\n\tm.__n0.SetValue(m.reading)"},
	} {
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q\n--- generated ---\n%s", tc.what, tc.want, out)
		}
	}

	// The declared path must be the *only* thing the selector resolved to. A
	// resolver that could not reach the Spec falls back to emitting the bare
	// selector as an import path of its own — a second, unbuildable import of
	// a package that does not exist, alongside the right one.
	if strings.Contains(out, "\t\"fynegauge\"\n") || strings.Contains(out, " \"fynegauge\"\n") {
		t.Errorf("the bare selector was imported as a path of its own\n--- generated ---\n%s", out)
	}
}

// generateFyneGo runs the full check → lower → generate pipeline and returns
// model.go, the file carrying the Model struct, the import block and BuildUI.
func generateFyneGo(t *testing.T, src string) string {
	t.Helper()
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return string(mem.Files()["model.go"])
}

// TestFynePrimitiveWithoutASpecIsAnError closes the hole the Spec opened. A
// primitive is only a widget once a Spec says which one; instantiating one
// without a Spec would otherwise emit no node while the AppendChild naming it
// survived — a successful build producing Go that does not compile, which is
// issue #120's shape.
func TestFynePrimitiveWithoutASpecIsAnError(t *testing.T) {
	src := `
import . "sngl://std"
import "sngl://platforms/fyne"

component main {
    vbox {
        fyne.Widget(text="no spec") {}
    }
}
`
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, codegen.NewMemSink())
	if err == nil {
		t.Fatal("generate succeeded for a primitive with no Spec")
	}
	if !strings.Contains(err.Error(), "without a spec record") {
		t.Errorf("error does not name the missing Spec: %v", err)
	}
}
