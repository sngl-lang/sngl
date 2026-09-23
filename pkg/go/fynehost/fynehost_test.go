package fynehost_test

import (
	"testing"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/fynehost"
	"git.duckfam.us/jonathan/sngl/pkg/go/fynelayout"
)

// This test imports the compiler, which the package under test must not. That
// is the right way round: a test binary may link both ends to check they meet,
// while the shipped package still carries only the protocol.

// check parses, checks and lowers the way an interpreted fyne run does. The
// lowering matters: a Spec only reaches a node once the platform's override is
// inlined, and the Spec is the whole of what this host is told.
func check(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plat := codegen.LookupPlatform("fyne")
	if plat == nil {
		t.Fatal("fyne platform not registered")
	}
	// The go language is listed even though this run is interpreted: fyne.sngl
	// imports sngl:language/go to name the natives its timer schedules with,
	// and resolving that import is what says the language exists.
	goLang := codegen.LookupLang("go")
	if goLang == nil {
		t.Fatal("go language not registered")
	}
	cfg := &checker.Config{
		IsMain:    true,
		Languages: []ir.Language{goLang},
		Platforms: []ir.Platform{plat},
		Targets:   []ir.StaticTarget{{Platform: "fyne", Language: "none"}},
	}
	pkg, diags := checker.Check(doc, cfg)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	if err := lower.Lower(pkg, lower.InterpreterFeatures(), lower.Options{
		Platform:        "fyne",
		ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat),
	}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	return pkg
}

const src = `import . "sngl:ui"

component main node {
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
	h := fynehost.New(fynehost.Ctors())
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
	h := fynehost.New(fynehost.Ctors())

	var fired []string
	h.OnEvent = func(key interp.Key, event string, args []any) {
		fired = append(fired, event)
		p, err := s.Invoke(key, event, args...)
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
// constructor must say so. Rendering a hole silently is how a preview quietly
// stops matching the program.
func TestAnUnregisteredElementIsReportedNotDropped(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctors := fynehost.Ctors()
	delete(ctors, "fyne.io/fyne/v2/widget.NewButton")
	h := fynehost.New(ctors)
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(h.Unsupported) == 0 {
		t.Error("the missing button constructor was not reported")
	}
	// The rest of the tree still mounted.
	if _, ok := h.Object(s.View().Find("out")[0].Key); !ok {
		t.Errorf("#out did not mount; one missing constructor took the tree with it")
	}
	if _, ok := h.Object(s.View().Find("inc")[0].Key); ok {
		t.Errorf("#inc mounted despite having no constructor")
	}
}

// TestAWrapperGetsItsChild covers the registry's Content field, which named a
// field nothing read: a scroll rendered empty because a wrapper takes its one
// child through a field rather than a list.
func TestAWrapperGetsItsChild(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	src := `import . "sngl:ui"

component main node {
    scroll #s {
        text #inner(value="scrolled")
    }
}
`
	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Ctors())
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	obj, ok := h.Object(s.View().Find("s")[0].Key)
	if !ok {
		t.Fatal("#s is not mounted")
	}
	sc, ok := obj.(*container.Scroll)
	if !ok {
		t.Fatalf("#s is a %T, want a *container.Scroll", obj)
	}
	if sc.Content == nil {
		t.Fatal("the scroll has no content; its Content field was never assigned")
	}
	if _, ok := sc.Content.(*widget.Label); !ok {
		t.Errorf("the scroll holds a %T, want the text's *widget.Label", sc.Content)
	}
}

// TestAnUnsupportedElementSwallowsItsSubtree: children of an element this
// worker cannot build have nowhere to go. Letting them fall through mounts a
// whole subtree at top level and shifts every root index after it.
func TestAnUnsupportedElementSwallowsItsSubtree(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	src := `import . "sngl:ui"

component main node {
    vbox {
        hbox #unknown {
            text #buried(value="should not surface")
        }
        text #after(value="after")
    }
}
`
	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctors := fynehost.Ctors()
	delete(ctors, "fyne.io/fyne/v2/container.NewHBox")
	h := fynehost.New(ctors)
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, mounted := h.Object(s.View().Find("buried")[0].Key); mounted {
		t.Error("#buried was mounted even though its parent could not be built")
	}
	if _, mounted := h.Object(s.View().Find("after")[0].Key); !mounted {
		t.Error("#after was not mounted; an unsupported sibling should not take it out")
	}
}

// TestTwoWayBindingWritesBack is the hello example: `input(:value=name)` binds
// both ways, so typing must reach the var and the text that interpolates it.
//
// It exercises the whole chain -- the binding desugars to a handler that
// assigns its parameter, the widget reports the text it now holds as that
// parameter, and the resulting patch lands on the label.
func TestTwoWayBindingWritesBack(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	src := `import . "sngl:ui"

component main node {
    var name = "World"

    vbox {
        text #greeting(value="Hello, {name}!")
        input #field(:value=name, placeholder="Enter your name")
    }
}
`
	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Ctors())
	h.OnEvent = func(key interp.Key, event string, args []any) {
		p, err := s.Invoke(key, event, args...)
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

	greeting, _ := h.Object(s.View().Find("greeting")[0].Key)
	label, ok := greeting.(*widget.Label)
	if !ok {
		t.Fatalf("#greeting is a %T, want a *widget.Label", greeting)
	}
	if label.Text != "Hello, World!" {
		t.Fatalf("initial render is %q", label.Text)
	}

	// Type into the field: the widget reports the text it now holds.
	if err := h.Fire(s.View().Find("field")[0].Key, "input", "SNGL"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if label.Text != "Hello, SNGL!" {
		t.Errorf("after typing, the label reads %q; the binding did not write back", label.Text)
	}
}

// TestAContainerCarriesItsChildrensFlex: Fyne's own boxes pack children at
// their minimum size, so a keypad laid out with `flex=1` came up as small
// buttons in the corner. The layout is what answers it, and its weights are
// positional, so it is rebuilt as children arrive.
func TestAContainerCarriesItsChildrensFlex(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	src := `import . "sngl:ui"

component main node {
    hbox #row(style={gap=4, padding=6}) {
        button(text="a", style={flex=1, margin=3})
        button(text="b", style={flex=2})
    }
}
`
	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Ctors())
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obj, ok := h.Object(s.View().Find("row")[0].Key)
	if !ok {
		t.Fatal("#row is not mounted")
	}
	c, ok := obj.(*fyne.Container)
	if !ok {
		t.Fatalf("#row is a %T, want a *fyne.Container", obj)
	}
	w, ok := c.Layout.(fynelayout.Weighted)
	if !ok {
		t.Fatalf("#row lays out with %T; its children's flex has nowhere to be said", c.Layout)
	}
	if !w.Horizontal {
		t.Error("an hbox laid out vertically")
	}
	if len(w.Weights) != 2 || w.Weights[0] != 1 || w.Weights[1] != 2 {
		t.Errorf("weights are %v, want [1 2]", w.Weights)
	}
	if len(w.Margins) != 2 || w.Margins[0] != 3 {
		t.Errorf("margins are %v, want the first child's 3", w.Margins)
	}
	if w.Gap != 4 || w.Padding != 6 {
		t.Errorf("gap/padding are %v/%v, want 4/6", w.Gap, w.Padding)
	}
}

// TestPaintStylesReachATheme: Fyne has no per-widget styling, so a colour or a
// text size is a theme over the subtree it applies to. Without this the
// calculator's keys were all one colour.
func TestPaintStylesReachATheme(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	src := `import . "sngl:ui"

component main node {
    vbox {
        button #plain(text="plain")
        button #painted(text="painted", style={background=#f59e0b, color=#ffffff, fontSize=22})
    }
}
`
	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h := fynehost.New(fynehost.Ctors())
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	root, _ := h.Object(s.View().Find("plain")[0].Key)
	if root == nil {
		t.Fatal("#plain is not mounted")
	}
	// A node asking for nothing is not wrapped: a theme that answers nothing
	// still costs a node in the tree.
	if h.ViewOf(s.View().Find("plain")[0].Key) != root {
		t.Error("#plain was wrapped in a theme it did not ask for")
	}
	painted := s.View().Find("painted")[0].Key
	wrapper := h.ViewOf(painted)
	obj, _ := h.Object(painted)
	if wrapper == obj {
		t.Fatal("#painted was not wrapped; its background and text size reach nothing")
	}
	if _, ok := wrapper.(*container.ThemeOverride); !ok {
		t.Errorf("#painted is wrapped in a %T, want a *container.ThemeOverride", wrapper)
	}
}
