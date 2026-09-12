package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// buildForGtk4 runs the whole pipeline for one source string and returns the
// generated files, or the error the platform refused with.
func buildForGtk4(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		return nil, err
	}
	return mem.Files(), nil
}

// A stdlib component with no `platform gtk4` body has nothing to emit. Before
// this was an error the node was dropped and the build succeeded, so the window
// came out missing widgets the source asked for with no diagnostic anywhere.
func TestUnimplementedStdlibComponentFailsBuild(t *testing.T) {
	skipWithoutGIR(t)
	files, err := buildForGtk4(t, `
import . "sngl:ui"
window {
    text(value="visible")
    avatar(initials="ab")
}
`)
	if err == nil {
		t.Fatalf("expected the build to fail; it emitted %d files", len(files))
	}
	msg := err.Error()
	// The message has to name both halves: which component, and which target
	// does not have it.
	for _, want := range []string{`"avatar"`, "gtk4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic %q does not mention %s", msg, want)
		}
	}
}

// The same rule must not fire on a component the program declared itself. An
// empty body there is the program saying it draws nothing, which is a legal
// thing to say — the fixtures behind the LSP tests are written that way.
func TestUserComponentWithEmptyBodyStillBuilds(t *testing.T) {
	skipWithoutGIR(t)
	files, err := buildForGtk4(t, `
import . "sngl:ui"
component label(value string) node {
}
window {
    label(value="hello")
    text(value="visible")
}
`)
	if err != nil {
		t.Fatalf("expected a clean build for a user component with an empty body: %v", err)
	}
	if _, ok := files["model.go"]; !ok {
		t.Fatal("model.go not emitted")
	}
}

// TestStdlibOverrides_EmitTheirWidgets is the other side of the rule above:
// the stdlib components gtk4.sngl does implement have to reach the C API the
// override body names. A missing override is a build failure by the rule
// above, so what this adds is that the body was not merely present but
// emitted — the widget constructor and the setter for each prop it forwards.
//
// The props that are not here are the ones the platform cannot carry, and
// they are documented at each override: a GtkOrientation or GtkPositionType
// reachable only from a string literal (divider's direction, popover's
// position), and a `date`, which is a builtin struct with no fields to pull
// GtkCalendar's day/month/year out of.
func TestStdlibOverrides_EmitTheirWidgets(t *testing.T) {
	skipWithoutGIR(t)
	files, err := buildForGtk4(t, `
import . "sngl:ui"
import . "sngl:time"
window {
    var frac = 0.25
    var on = false
    var shown = false
    var d date
    vbox {
        progress(value=frac, label="loading", showValue=true)
        spinner(label="working")
        divider
        link(href="https://example.com", text="a link")
        toggle(checked=on)
        datepicker(value=d)
        spacer
        tooltip(text="explains it") { text(value="hover me") }
        card { text(value="in a card") }
        stack { text(value="a page") }
        popover(open=shown) { button(text="trigger") }
    }
}
`)
	if err != nil {
		t.Fatalf("building the implemented stdlib components: %v", err)
	}
	model := string(files["model.go"])
	if model == "" {
		t.Fatal("model.go not emitted")
	}
	for _, want := range []string{
		// progress
		"C.gtk_progress_bar_new()",
		"C.gtk_progress_bar_set_fraction(",
		"C.gtk_progress_bar_set_show_text(",
		// spinner
		"C.gtk_spinner_set_spinning(",
		// divider
		"C.gtk_separator_new(",
		// link
		"C.gtk_link_button_set_uri(",
		// label is GtkButton's, two links up the chain from GtkLinkButton,
		// and its setter takes the GtkButton the cast names.
		"C.gtk_button_set_label((*C.GtkButton)",
		// toggle
		"C.gtk_switch_set_active(",
		// datepicker: the calendar, and its one connectable signal
		"C.gtk_calendar_new()",
		`C.CString("day-selected")`,
		// spacer, and tooltip -- both GtkWidget properties, so both are
		// reachable only because the parent chain merged.
		"C.gtk_widget_set_hexpand(",
		"C.gtk_widget_set_tooltip_text(",
		// card, stack and popover each host a slot in a container whose
		// child API is not gtk_box_append.
		"C.gtk_frame_set_child(",
		"C.gtk_stack_add_child(",
		"C.gtk_popover_set_child(",
		"C.gtk_popover_set_autohide(",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("the emitted model.go does not call %s", want)
		}
	}
}
