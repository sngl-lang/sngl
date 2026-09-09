package fyne

import (
	"regexp"
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
import . "sngl:ui"
import "sngl:platform/fyne"

var reading int = 42

component Gauge(:level int, @change ChangeEvent) ui {
    fyne.Widget(
        spec=fyne.Spec{
            new=fyne.Native{path="github.com/example/fyne-gauge/fynegauge", name="NewGauge"},
            args=[fyne.Arg{raw="0"}],
            goType=fyne.Native{path="github.com/example/fyne-gauge/fynegauge", name="*Gauge"},
            setters=[fyne.Setter{prop="number", call="SetValue"}],
            handlers=[fyne.Handler{on="change", field="OnValueChanged", signature="func(v int)", param="v"}],
        },
        number=level,
        @change { change() },
    ) {}
}

component main ui {
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
import . "sngl:ui"
import "sngl:platform/fyne"

component main ui {
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
import . "sngl:ui"
import "sngl:platform/fyne"

var reading int = 42

component Gauge(:level int, @change ChangeEvent) ui {
    fyne.Widget(
        spec=fyne.Spec{
            new=fyne.Native{path="github.com/example/fyne-charts/v2", name="NewGauge"},
            args=[fyne.Arg{raw="0"}],
            goType=fyne.Native{path="github.com/example/fyne-charts/v2", name="*Gauge"},
            setters=[fyne.Setter{prop="number", call="SetValue"}],
            handlers=[fyne.Handler{on="change", field="OnValueChanged", signature="func(v int)", param="v"}],
        },
        number=level,
        @change { change() },
    ) {}
}

component main ui {
    vbox {
        Gauge(:level=reading, @change {})
    }
}
`

// A module's Go package name is in its source, not in its path, and another
// import may already hold the obvious name -- so no Spec can spell the
// selector its symbols are reached through, and none does. It declares the
// import path and the identifier, and the Go backend names the package.
//
// What must hold is that the emitter agrees with itself: whatever alias the
// import block gives a path is the alias every reference to it uses. Asserting
// a particular name here would be asserting the backend's private choice, and
// would have to change whenever an unrelated import changed it.
func TestAThirdPartyPackageIsQualifiedWithWhateverAliasTheBackendAssigns(t *testing.T) {
	const path = "github.com/example/fyne-charts/v2"
	out := generateFyneGo(t, versionedWidgetSrc)

	alias := importAlias(t, out, path)
	// A /vN module whose stripped name collides with fyne's own is the case
	// that forces a suffix, so the alias here is emphatically not derivable
	// from the path by anyone but the backend.
	if alias == "" {
		t.Fatalf("no import of %s\n--- generated ---\n%s", path, out)
	}
	for _, tc := range []struct{ what, want string }{
		{"constructor", alias + ".NewGauge(0)"},
		{"widget field type", "*" + alias + ".Gauge"},
		{"setter", ".SetValue(m.reading)"},
	} {
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q (alias assigned: %s)\n--- generated ---\n%s",
				tc.what, tc.want, alias, out)
		}
	}
	if n := strings.Count(out, path); n != 1 {
		t.Errorf("module imported %d times, want 1\n--- generated ---\n%s", n, out)
	}
}

// A third-party package whose Go name is one of fyne's own is the case that
// had no answer while a Spec named its own selector: the name is the package's,
// the author cannot rename it, and forcing it claimed an alias fyne's call
// sites were already using. Letting the backend assign both is what resolves
// it -- one of the two gets a suffix, and every reference follows.
func TestAThirdPartyPackageMayShareFynesOwnName(t *testing.T) {
	src := strings.Replace(versionedWidgetSrc,
		`path="github.com/example/fyne-charts/v2"`,
		`path="github.com/example/widget"`, -1)
	if src == versionedWidgetSrc {
		t.Fatal("the fixture did not substitute; this test asserts nothing")
	}
	// `text` brings in fyne's own widget package alongside it.
	src = strings.Replace(src, "        Gauge(", "        text(value=\"x\")\n        Gauge(", 1)

	out := generateFyneGo(t, src)
	third := importAlias(t, out, "github.com/example/widget")
	fyneOwn := importAlias(t, out, "fyne.io/fyne/v2/widget")
	if third == "" || fyneOwn == "" {
		t.Fatalf("both packages must be imported; got %q and %q\n--- generated ---\n%s", third, fyneOwn, out)
	}
	if third == fyneOwn {
		t.Fatalf("both packages were given the alias %q\n--- generated ---\n%s", third, out)
	}
	if !strings.Contains(out, third+".NewGauge(0)") {
		t.Errorf("the third-party constructor is not qualified with %q\n--- generated ---\n%s", third, out)
	}
	if !strings.Contains(out, fyneOwn+".NewLabel") {
		t.Errorf("fyne's own widget is not qualified with %q\n--- generated ---\n%s", fyneOwn, out)
	}
}

// importAlias is the alias the generated import block gives path, or "" when
// it does not import it. An unaliased import uses its own package name, which
// the backend chose too -- so it is read out of the path the same way the
// backend derived it.
func importAlias(t *testing.T, out, path string) string {
	t.Helper()
	if m := regexp.MustCompile(`(?m)^\t([A-Za-z_][A-Za-z0-9_]*) "` + regexp.QuoteMeta(path) + `"$`).FindStringSubmatch(out); m != nil {
		return m[1]
	}
	if !strings.Contains(out, `"`+path+`"`) {
		return ""
	}
	base := path[strings.LastIndex(path, "/")+1:]
	return base
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

// The signature is read as Go, so what counts as a named parameter is Go's
// answer. Each of these was decided wrongly by splitting on spaces: the first
// three were accepted and their parameter dropped, emitting a handler with no
// parameter whose body named one; the last names every parameter and was
// rejected.
func TestSignatureParametersAreJudgedAsGo(t *testing.T) {
	for _, tc := range []struct {
		sig     string
		wantErr bool
		what    string
	}{
		{`func(int)`, true, "unnamed"},
		{`func(chan int)`, true, "unnamed two-word type"},
		{"func(v\tint)", false, "tab between name and type"},
		{`func(v, w int)`, false, "grouped names"},
		{`func(v int) error`, true, "a return value the handler cannot produce"},
		{`func(v int)`, false, "the ordinary form"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			src := strings.Replace(versionedWidgetSrc,
				`signature="func(v int)"`, `signature="`+tc.sig+`"`, 1)
			if src == versionedWidgetSrc && tc.sig != `func(v int)` {
				t.Fatal("the fixture did not substitute; this test asserts nothing")
			}
			out, err := generateFyneGoErr(t, src)
			switch {
			case tc.wantErr && err == nil:
				t.Errorf("%s generated without complaint\n--- generated ---\n%s", tc.sig, out)
			case !tc.wantErr && err != nil:
				t.Errorf("%s was rejected: %v", tc.sig, err)
			case !tc.wantErr:
				// The handler must actually take the parameter its body names.
				if !strings.Contains(out, "__n0_change_handler(v ") {
					t.Errorf("%s: handler does not take v\n--- generated ---\n%s", tc.sig, out)
				}
			}
		})
	}
}

// A Native with a path names a package; one without is a bare identifier. A
// qualified spelling with no path was how the bare selector became an import
// path of its own -- there is no such spelling now, because the selector is
// never written.
func TestANativeWithoutAPathIsABareIdentifier(t *testing.T) {
	src := strings.Replace(versionedWidgetSrc,
		`fyne.Native{path="github.com/example/fyne-charts/v2", name="NewGauge"}`,
		`fyne.Native{name="newLocalGauge"}`, 1)
	if src == versionedWidgetSrc {
		t.Fatal("the fixture did not substitute; this test asserts nothing")
	}
	out, err := generateFyneGoErr(t, src)
	if err != nil {
		t.Fatalf("a pathless Native was rejected: %v", err)
	}
	if !strings.Contains(out, "newLocalGauge(0)") {
		t.Errorf("the bare identifier was not emitted as written\n--- generated ---\n%s", out)
	}
	for _, bogus := range []string{"\t\"newLocalGauge\"\n", "\t\"charts\"\n"} {
		if strings.Contains(out, bogus) {
			t.Errorf("an identifier was imported as a path: %q\n--- generated ---\n%s", bogus, out)
		}
	}
}
