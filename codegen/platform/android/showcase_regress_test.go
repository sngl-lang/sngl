package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// androidTarget is the registered-platform set these tests check against.
// Without it the checker never merges this platform's `component sngl.X`
// extension bodies, so the stdlib components stay abstract and codegen sees a
// tree the real CLI never produces. (Naming this platform directly rather than
// through internal/testtargets: that package imports codegen/platform, which
// this internal test package is part of.)
func androidTarget() []ir.Platform { return []ir.Platform{&Generator{}} }

// androidLangs is what a config naming `Language: "kotlin"` has to provide.
// This platform's own source imports `sngl:language/kotlin` to describe
// Compose, and that resolves against this list whatever language the build is
// for -- see codegen.CollectLangs.
func androidLangs() []ir.Language { return codegen.CollectLangs() }

// compileSrc compiles a program to Kotlin via CompileIR (non-test mode) or
// CompileTestIR (test mode), returning the generated MainScreen.kt source.
func compileSrc(t *testing.T, src string, testMode bool) string {
	t.Helper()
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: androidTarget(), Languages: androidLangs(), Targets: []ir.StaticTarget{{Platform: "android", Language: "kotlin"}}})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, codegen.CapsOrNone(kotlinLang.LanguageIdentifier(), gen.PlatformIdentifier()), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: pkg}, "android")
	var out []byte
	if testMode {
		out, err = CompileTestIR(ctx, Config{})
	} else {
		out, err = CompileIR(ctx, Config{})
	}
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return string(out)
}

// TestStructFieldDefaults guards that data-class fields get usable Kotlin
// defaults. Previously a field with a source default emitted `= ` (the IR
// placeholder carries no value), producing a syntax error; a `null` default
// rendered empty. Regression for the struct-default fix.
func TestStructFieldDefaults(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
struct User {
    name string = ""
    age int = 0
    admin bool = false
}
struct TreeNode {
    value int = 0
    left dyn = null
    right dyn = null
}
ui.window {
    var u = User{}
    var t = TreeNode{}
    vbox { text(value=u.name) text(value=string(t.value)) }
}`
	out := compileSrc(t, src, false)
	for _, want := range []string{
		`var age: Int = 0`,
		`var admin: Boolean = false`,
		`var name: String = ""`,
		`var value: Int = 0`,
		`var left: Any? = null`,
		`var right: Any? = null`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "= ,") || strings.Contains(out, "= \n") {
		t.Errorf("empty field default emitted:\n%s", out)
	}
}

// TestNullStateTyped guards that an option/dyn var initialized to null emits a
// typed mutableStateOf so Kotlin can infer the type. Regression for the
// null-state fix.
func TestNullStateTyped(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
struct User { name string = "" }
ui.window {
    var nickname option<string> = null
    var picked option<User> = null
    vbox { text(value="x") }
}`
	out := compileSrc(t, src, false)
	if !strings.Contains(out, "mutableStateOf<String?>(null)") {
		t.Errorf("expected typed null state for nickname:\n%s", out)
	}
	if !strings.Contains(out, "mutableStateOf<User?>(null)") {
		t.Errorf("expected typed null state for picked:\n%s", out)
	}
}

// TestColorToComposeColor guards that color values convert to a Compose Color
// constructor with 0..255 channels, never android.graphics.Color.parseColor of
// a Color struct. Regression for the color-conversion fix.
func TestColorToComposeColor(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
ui.window {
    vbox {
        text(value="hi", style={color="#216"})
    }
}`
	out := compileSrc(t, src, false)
	if strings.Contains(out, "parseColor") {
		t.Errorf("parseColor should not appear:\n%s", out)
	}
	if !strings.Contains(out, "ComposeColor(red = ") {
		t.Errorf("expected ComposeColor(red = ...) form:\n%s", out)
	}
}

// TestAlertToast guards that Alert.toast maps to Android Toast, not a bogus
// String.toast extension. Regression for the toast fix.
func TestAlertToast(t *testing.T) {
	src := `import . "sngl:ui"
import . "sngl:dialog"
import ui "sngl:ui"
ui.window {
    vbox { button(text="go", @click { Alert.toast("done", "success") }) }
}`
	out := compileSrc(t, src, false)
	if strings.Contains(out, ".toast(") {
		t.Errorf("bogus String.toast extension emitted:\n%s", out)
	}
	if !strings.Contains(out, "Toast.makeText(context,") {
		t.Errorf("expected Toast.makeText(context, ...):\n%s", out)
	}
}

// TestTestModeStateHoisting guards the test-mode scope fixes: computeds become
// MainScreenState members (so the test accessor reads c.<name>) and are read as
// state.<name> at view sites; null state in the class is typed; class init
// exprs reference siblings bare (not via the out-of-scope `state` param); and
// timer active-var references route through state.
func TestTestModeStateHoisting(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
ui.window {
    var count = 0
    var nick option<string> = null
    func doubled() => count * 2
    vbox { text(value=string(doubled)) }
}`
	out := compileSrc(t, src, true)
	if !strings.Contains(out, "class MainScreenState") {
		t.Fatalf("expected MainScreenState class:\n%s", out)
	}
	// computed is a member of the state class (bare ref inside the class)...
	if !strings.Contains(out, "val doubled by derivedStateOf { (count * 2) }") {
		t.Errorf("expected computed as state member with bare ref:\n%s", out)
	}
	// ...and read via state.<name> at view sites.
	if !strings.Contains(out, "state.doubled") {
		t.Errorf("expected state.doubled at view site:\n%s", out)
	}
	if !strings.Contains(out, "mutableStateOf<String?>(null)") {
		t.Errorf("expected typed null state in class:\n%s", out)
	}
}

// TestSelectRadioDatepickerWidgets guards that the select, radio, and
// datepicker components render real Compose widgets (not the old TODO stubs /
// bare Text). select must write its two-way :value on selection.
func TestSelectRadioDatepickerWidgets(t *testing.T) {
	src := `import . "sngl:ui"
import . "sngl:time"
import . "sngl:dialog"
import ui "sngl:ui"
ui.window {
    var fruit = ""
    var size = "medium"
    var dob date = date("2000-01-01")
    vbox {
        select(options=["Apple", "Banana"], placeholder="File.pick", value=fruit)
        radio(direction="horizontal", options=["small", "medium", "large"], value=size)
        datepicker(placeholder="Birthday", value=dob)
    }
}`
	out := compileSrc(t, src, false)
	if strings.Contains(out, "TODO") {
		t.Errorf("widget rendered as a TODO stub:\n%s", out)
	}
	// select: dropdown with two-way write-back.
	for _, want := range []string{"DropdownMenu(", "DropdownMenuItem(", "fruit = opt"} {
		if !strings.Contains(out, want) {
			t.Errorf("select missing %q:\n%s", want, out)
		}
	}
	// radio: a RadioButton per option, selected reflects the bound value.
	if !strings.Contains(out, "RadioButton(selected = (size == opt)") {
		t.Errorf("radio not rendered as RadioButton group:\n%s", out)
	}
	// datepicker: a field showing the value (not bare Text). `value` is an
	// option<date>, so what the field shows is the date under a null test --
	// a date nobody picked shows nothing rather than the zero date.
	if !strings.Contains(out, "OutlinedTextField(") || !strings.Contains(out, "dob != null") {
		t.Errorf("datepicker not rendered as a value-bound field:\n%s", out)
	}
}

// TestFeedbackIntrinsicsFromDeclarations covers the four stdlib components
// whose android override is a declared #[intrinsic] rather than a Go switch
// case. `tabs` also guards the bound `selected` index reaching TabRow: the
// switch it replaced passed a literal 0, so a program could not select a tab.
func TestFeedbackIntrinsicsFromDeclarations(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
ui.window {
    var idx = 0
    vbox {
        divider
        spinner(label="Loading...")
        card(variant="outlined") {
            text(value="in a card")
        }
        tabs(items=["One", "Two"], selected=idx) {
            text(value="one")
            text(value="two")
        }
    }
}`
	out := compileSrc(t, src, false)
	for _, want := range []string{
		"HorizontalDivider(modifier = Modifier)",
		"CircularProgressIndicator(modifier = Modifier)",
		"Card(modifier = Modifier) {",
		"TabRow(selectedTabIndex = idx) {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
