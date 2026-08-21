package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// compileSrc compiles a program to Kotlin via CompileIR (non-test mode) or
// CompileTestIR (test mode), returning the generated MainScreen.kt source.
func compileSrc(t *testing.T, src string, testMode bool) string {
	t.Helper()
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, gen.Capabilities(kotlinLang).ToLowerCaps(), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
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
	src := `import . "sngl://std"
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
component main {
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
	src := `import . "sngl://std"
struct User { name string = "" }
component main {
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
	src := `import . "sngl://std"
component main {
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
	src := `import . "sngl://std"
component main {
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
	src := `import . "sngl://std"
component main {
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
	src := `import . "sngl://std"
component main {
    var fruit = ""
    var size = "medium"
    var dob date = date("2000-01-01")
    vbox {
        select(options=["Apple", "Banana"], placeholder="Pick", value=fruit)
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
	// datepicker: a field showing the value (not bare Text).
	if !strings.Contains(out, "OutlinedTextField(") || !strings.Contains(out, "value = dob") {
		t.Errorf("datepicker not rendered as a value-bound field:\n%s", out)
	}
}
