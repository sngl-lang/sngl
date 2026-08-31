package fynehost_test

import (
	"strings"
	"testing"

	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/fynehost"
)

// This test imports the compiler, which the package under test must not. That
// is the right way round: a test binary may link both ends to check they meet,
// while the shipped package still carries only the protocol.

func check(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}

const src = `import . "sngl:ui"

component main {
    var (
        count = 0
        label = "hello"
    )
    vbox {
        text #out(value="{label}: {count}")
        button #inc(text="+", @click { count += 1 })
    }
}
`

// TestASessionDrivesRealFyneWidgets is the whole point of the host: an
// interpreted program, no code generated and nothing compiled, putting real
// widgets on a real Fyne canvas.
func TestASessionDrivesRealFyneWidgets(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Default())
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(h.Unsupported) != 0 {
		t.Fatalf("the registry could not build %v", h.Unsupported)
	}

	// The widgets are real, and they are the ones the declarations name.
	outKey := s.View().Find("out")[0].Key
	obj, ok := h.Object(outKey)
	if !ok {
		t.Fatal("#out is not mounted")
	}
	lbl, ok := obj.(*widget.Label)
	if !ok {
		t.Fatalf("#out is a %T, want a *widget.Label", obj)
	}
	if lbl.Text != "hello: 0" {
		t.Errorf("label reads %q", lbl.Text)
	}

	// A patch reaches the widget.
	interp.SetComponentVar(s, "label", "goodbye")
	p, err := s.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := interp.Apply(h, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if lbl.Text != "goodbye: 0" {
		t.Errorf("after a patch the label reads %q, want %q", lbl.Text, "goodbye: 0")
	}
}

// TestClickingTheWidgetRunsTheSNGLHandler closes the loop the other way: the
// event goes through the widget's own callback, as it does on every compiled
// target, and comes back out as a patch.
func TestClickingTheWidgetRunsTheSNGLHandler(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Default())

	var fired []string
	h.OnEvent = func(key interp.Key, event string) {
		fired = append(fired, event)
		p, err := s.Invoke(key, event)
		if err != nil {
			t.Errorf("Invoke: %v", err)
			return
		}
		if err := interp.Apply(h, p); err != nil {
			t.Errorf("Apply: %v", err)
		}
	}
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	incKey := s.View().Find("inc")[0].Key
	btn, _ := h.Object(incKey)
	if _, ok := btn.(*widget.Button); !ok {
		t.Fatalf("#inc is a %T, want a *widget.Button", btn)
	}

	// Fire the widget's own callback, not a synthetic path around it.
	if err := h.Fire(incKey, "click"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(fired) != 1 || fired[0] != "click" {
		t.Fatalf("host reported %v, want one click", fired)
	}

	obj, _ := h.Object(s.View().Find("out")[0].Key)
	if got := obj.(*widget.Label).Text; got != "hello: 1" {
		t.Errorf("label reads %q; the SNGL handler did not run through the widget", got)
	}
}

// TestAnUnregisteredElementIsReportedNotDropped: a worker built without a
// widget must say so. Rendering a hole silently is how a preview quietly stops
// matching the program.
func TestAnUnregisteredElementIsReportedNotDropped(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	reg := fynehost.Default()
	delete(reg, "button")
	h := fynehost.New(reg)
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(h.Unsupported) != 1 || h.Unsupported[0] != "button" {
		t.Errorf("unsupported = %v, want [button]", h.Unsupported)
	}
	if !strings.Contains(h.Tree(), "text") {
		t.Errorf("the rest of the tree did not mount:\n%s", h.Tree())
	}
}
