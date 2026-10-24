package gtk4

import (
	"errors"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// buildForGtk4 runs the whole pipeline for one source string and returns the
// generated files, or the first error the checker or the platform refused
// with.
func buildForGtk4(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, errors.New(d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	pkg = ir.ClonePackage(pkg)
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		return nil, err
	}
	return mem.Files(), nil
}

// A component the program declared with an empty body builds: the program is
// saying it draws nothing, which is a legal thing to say — the fixtures behind
// the LSP tests are written that way.
func TestUserComponentWithEmptyBodyStillBuilds(t *testing.T) {
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

// TestStdlibOverrides_EmitTheirWidgets: the stdlib components gtk4.sngl
// implements have to reach the C API the override body names. A missing
// override is a build failure in the checker (reportBodylessLibComponents), so
// what this adds is that the body was not merely present but emitted — the
// widget constructor and the setter for each prop it forwards.
//
// The props that are not here are the ones the platform cannot carry, and
// they are documented at each override: a GtkOrientation or GtkPositionType
// reachable only from a string literal (divider's direction, popover's
// position), and a `date`, which is a builtin struct with no fields to pull
// GtkCalendar's day/month/year out of.
func TestStdlibOverrides_EmitTheirWidgets(t *testing.T) {
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
        table(columns=["Name"], selectable=true) { tableRow(cells=["Ada"]) }
        tree { treeItem(label="root") { treeItem(label="leaf") } }
        radio { choice(label="a") }
        select { choice(label="b") }
        menubar { menuItem(label="File") }
        menu(open=shown) { menuItem(label="Open") }
        toolbar { button(text="tool") }
        splitview { text(value="left") }
        drawer(open=shown) { component content { text(value="drawn") } }
        modal(open=shown, title="Title", closable=true) { component content { text(value="modal") } }
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
	// Every one is made, written and filled through gtk4rt, so the stdlib's
	// widget set builds with no cgo of its own.
	if strings.Contains(model, `import "C"`) {
		t.Errorf("the stdlib's widgets put model.go on the cgo path:\n%s", model)
	}
	for _, want := range []string{
		// progress
		`gtk4rt.New("GtkProgressBar")`,
		`"fraction", `,
		`"show-text", `,
		// spinner
		`"spinning", `,
		// divider
		`gtk4rt.New("GtkSeparator")`,
		// link
		`"uri", `,
		// label is GtkButton's, two links up the chain from GtkLinkButton,
		// whose setter gtk4rt wraps.
		"gtk4rt.ButtonSetLabel(",
		// toggle
		`gtk4rt.SetBool(m.__n`,
		// datepicker: the calendar, and its one connectable signal
		`gtk4rt.New("GtkCalendar")`,
		`"day-selected", `,
		// spacer, and tooltip -- both GtkWidget properties, so both are
		// reachable only because the parent chain merged.
		`"hexpand", `,
		`"tooltip-text", `,
		// card, stack and popover each host a slot in a container whose
		// child API is not gtk_box_append.
		"gtk4rt.FrameSetChild(",
		`gtk4rt.ChildCall("gtk_stack_add_child", `,
		`gtk4rt.ChildCall("gtk_popover_set_child", `,
		`"autohide", `,
		// table and splitview: rows and panes in homogeneous boxes, cells
		// left-aligned
		`"homogeneous", `,
		`"xalign", `,
		// table, tree, radio and select mark a selection with a check button
		"gtk4rt.CheckButtonSetActive(",
		`"toggled", func() {`,
		// drawer, menu and modal show while open
		`"visible", `,
	} {
		if !strings.Contains(model, want) {
			t.Errorf("the emitted model.go does not call %s", want)
		}
	}
}
