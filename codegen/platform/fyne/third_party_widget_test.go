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

// versionedWidgetSrc is the same widget reached through a semantic-import
// versioned path whose package name is not its last segment. `charts` is what
// the module's Go source calls itself; `v2` is what the path ends with, and
// `fyne-charts` is what stripping the version leaves.
const versionedWidgetSrc = `
import . "sngl://std"
import "sngl://platforms/fyne"

var reading int = 42

component Gauge(:level int, @change ChangeEvent) {
    fyne.Widget(
        spec=fyne.Spec{
            new="charts.NewGauge",
            args=[fyne.Arg{raw="0"}],
            goType="*charts.Gauge",
            imports=["github.com/example/fyne-charts/v2"],
            setters=[fyne.Setter{prop="number", call="SetValue"}],
            handlers=[fyne.Handler{on="change", field="OnValueChanged", signature="func(v int)", param="v"}],
        },
        number=level,
        @change { change() },
    ) {}
}

component main {
    vbox {
        Gauge(:level=reading, @change {})
    }
}
`

// A module's Go package name lives in its source, not in its import path, so
// nothing the compiler can compute from the path recovers it. The Spec's own
// spelling is the authority, and the import must be aliased to match it.
//
// Resolving a selector by matching a path's last segment got this wrong three
// ways at once: `v2` matched no selector, so the bare `charts` was emitted as
// an import path of its own; the real path was imported too, under whatever
// alias the Go emitter derived; and the call sites named neither.
func TestVersionedThirdPartyPathIsAliasedToItsWrittenSelector(t *testing.T) {
	out := generateFyneGo(t, versionedWidgetSrc)

	for _, tc := range []struct{ what, want string }{
		{"import aliased to the written selector", `charts "github.com/example/fyne-charts/v2"`},
		{"constructor", "charts.NewGauge(0)"},
		{"widget field type", "*charts.Gauge"},
		{"setter", ".SetValue(m.reading)"},
	} {
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q\n--- generated ---\n%s", tc.what, tc.want, out)
		}
	}
	// The selector must not also arrive as a path of its own, and the module
	// must not arrive a second time under a derived alias.
	if strings.Contains(out, "\t\"charts\"\n") {
		t.Errorf("the bare selector was imported as a path of its own\n--- generated ---\n%s", out)
	}
	if n := strings.Count(out, "github.com/example/fyne-charts/v2"); n != 1 {
		t.Errorf("module imported %d times, want 1\n--- generated ---\n%s", n, out)
	}
}

// A Spec that declares an import its Go spellings never reach, or reaches a
// package it never declared, cannot be resolved either way round -- so it is
// an error rather than a bogus import or a dangling selector.
func TestSpecImportsMustPairWithTheSelectorsItWrites(t *testing.T) {
	for _, tc := range []struct{ what, spec string }{
		{"an import no spelling names", `new="charts.NewGauge", goType="*charts.Gauge",
             imports=["github.com/example/a", "github.com/example/b"]`},
		{"a bare constructor with an import", `new="NewGauge", goType="Gauge",
             imports=["github.com/example/a"]`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			src := strings.Replace(versionedWidgetSrc,
				`new="charts.NewGauge",
            args=[fyne.Arg{raw="0"}],
            goType="*charts.Gauge",
            imports=["github.com/example/fyne-charts/v2"],`,
				tc.spec+`, args=[fyne.Arg{raw="0"}],`, 1)
			if src == versionedWidgetSrc {
				t.Fatal("the fixture did not substitute; this test asserts nothing")
			}
			if _, err := generateFyneGoErr(t, src); err == nil {
				t.Error("a Spec whose imports and selectors do not pair generated without complaint")
			}
		})
	}
}

// generateFyneGoErr is generateFyneGo returning the Generate error instead of
// failing the test, for the cases where the error is the subject.
func generateFyneGoErr(t *testing.T, src string) (string, error) {
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
	err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem)
	return string(mem.Files()["model.go"]), err
}

// A callback parameter with no name is the ordinary Go spelling of a func
// type, and reads as obviously correct. Dropping it silently emitted a handler
// taking no parameters, assigned to a field that passes one, whose body named
// a variable that did not exist -- three Go compile errors out of a build that
// reported success. Naming it is the whole requirement, so saying so is the
// fix.
func TestUnnamedSignatureParamIsAnError(t *testing.T) {
	src := strings.Replace(versionedWidgetSrc,
		`signature="func(v int)"`, `signature="func(int)"`, 1)
	if src == versionedWidgetSrc {
		t.Fatal("the fixture did not substitute; this test asserts nothing")
	}
	out, err := generateFyneGoErr(t, src)
	if err == nil {
		t.Fatalf("an unnamed callback parameter generated without complaint\n--- generated ---\n%s", out)
	}
	if !strings.Contains(err.Error(), "needs a name for every parameter") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}
