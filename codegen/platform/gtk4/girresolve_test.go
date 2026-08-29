package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
)

// The bundled subset must cover every widget codegen/platform/gtk4 names, or a
// host without GTK 4 development files cannot check the stdlib overrides.
// Spelled out rather than derived from the source so adding an override that
// names a new widget is a failure here, with the fix being to extend the
// bundled GIR.
var bundledWidgets = []string{
	"GtkWidget", "GtkWindow", "GtkApplicationWindow",
	"GtkBox", "GtkButton", "GtkCalendar", "GtkCheckButton", "GtkEntry",
	"GtkFrame", "GtkImage", "GtkLabel", "GtkLinkButton", "GtkPopover",
	"GtkProgressBar", "GtkScrolledWindow", "GtkSeparator", "GtkSpinner",
	"GtkStack", "GtkSwitch",
}

func TestBundledGIRCoversTheWrappedWidgets(t *testing.T) {
	reg, err := gir.Minimal()
	if err != nil {
		t.Fatalf("bundled GIR does not parse: %v", err)
	}
	for _, cType := range bundledWidgets {
		if reg.ByCType[cType] == nil {
			t.Errorf("bundled GIR declares no %s", cType)
		}
	}
}

// One resolver, one answer. The code generator used to resolve its own registry
// and discard the parse error, so a Config naming a GIR that did not load left
// it with none and every widget was reported as undeclared. These are the four
// answers girRegistry may give, and useGIR must agree with each.
func TestGIRResolutionIsSingleSourced(t *testing.T) {
	bundled, err := gir.Minimal()
	if err != nil {
		t.Fatalf("bundled GIR: %v", err)
	}

	t.Run("builtin is the bundled subset", func(t *testing.T) {
		reg, minimal, err := girRegistry(girBuiltin)
		if err != nil || !minimal || reg != bundled {
			t.Fatalf("girRegistry(builtin) = (%p, %v, %v); want the bundled registry", reg, minimal, err)
		}
		got, err := (&Generator{girOpt: girBuiltin}).useGIR("")
		if err != nil || got != bundled {
			t.Errorf("useGIR disagreed with girRegistry: (%p, %v)", got, err)
		}
	})

	t.Run("a named path that does not load is an error", func(t *testing.T) {
		if _, _, err := girRegistry("/nonexistent.gir"); err == nil {
			t.Error("a GIR path that does not exist resolved without error")
		}
		if _, err := (&Generator{}).useGIR("/nonexistent.gir"); err == nil {
			t.Error("useGIR swallowed the load failure; this is the bug that hid it")
		}
	})

	t.Run("a Config path overrides the configured option", func(t *testing.T) {
		g := &Generator{girOpt: girBuiltin}
		if _, err := g.useGIR(""); err != nil {
			t.Fatalf("priming: %v", err)
		}
		// The generator is cached on the bundled subset; a Config naming a real
		// file must re-arm rather than answer from that cache.
		reg, err := g.useGIR("gir/minimal/Gtk-4.0.gir")
		if err != nil {
			t.Fatalf("useGIR(path): %v", err)
		}
		if reg == bundled {
			t.Error("answered from the cached bundled registry, ignoring the Config path")
		}
	})

	t.Run("a nil generator still resolves", func(t *testing.T) {
		var g *Generator
		if _, err := g.useGIR(girBuiltin); err != nil {
			t.Errorf("nil generator: %v", err)
		}
	})
}

// The bundled subset has to carry the setter GIR records for every property the
// stdlib overrides bind, not merely the classes. A property it declares without
// one still compiles: the emitter falls through to g_object_set_property, so the
// generated code differs by host and nothing says so. Asserted as the absence of
// that fallback across every override, which needs no system GIR to check.
func TestBundledGIRLosesNoSetter(t *testing.T) {
	const src = `import . "sngl:std"
component main() {
    window(title="t") {
        vbox {
            text(value="a")
            button(text="b")
            input(value="c")
            checkbox(label="d", checked=true)
            image(src="e")
            progress(value=0.5, showValue=true)
            spinner(label="f")
            divider()
            toggle(checked=true)
            datepicker()
            link(href="g", text="h")
            card {}
            stack {}
            popover(open=true)
            tooltip(text="i") { text(value="j") }
            spacer()
            scroll { text(value="s") }
            textarea(value="k")
        }
    }
}
`
	out := generateWithBundledGIR(t, src)
	if generic := strings.Count(out, "sngl_set_prop_"); generic != 0 {
		t.Errorf("%d property assignments fell through to g_object_set_property; the bundled GIR records no setter for them:\n%s",
			generic, genericPropLines(out))
	}
	// A guard, because zero fallbacks is also what an empty program emits.
	if n := strings.Count(out, "C.gtk_"); n < 20 {
		t.Errorf("only %d gtk calls emitted; the fixture did not exercise the overrides", n)
	}
}

// genericPropLines is the fallback call sites, for a failure that names them.
func genericPropLines(out string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "sngl_set_prop_") && strings.Contains(line, "C.CString(") {
			b.WriteString(strings.TrimSpace(line))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// generateWithBundledGIR compiles src with every gtk4 registry pinned to the
// bundled subset: the registered platform the checker reads declarations from,
// and the generator that emits. Both, because they are separate objects -- and a
// helper that configured only one silently measured the host's GIR, which is how
// the first version of this test passed with a setter deliberately removed.
func generateWithBundledGIR(t *testing.T, src string) string {
	t.Helper()

	g := &Generator{}
	if err := g.Configure(map[string]string{"gir": girBuiltin}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !g.usingMinimalGIR() {
		t.Fatal("the generator did not take the bundled subset; this would measure the host GIR")
	}
	for _, p := range codegen.CollectPlatforms() {
		reg, ok := p.(*Generator)
		if !ok || reg.PlatformIdentifier() != platformName {
			continue
		}
		if err := reg.Configure(map[string]string{"gir": girBuiltin}); err != nil {
			t.Fatalf("configure registered gtk4: %v", err)
		}
		t.Cleanup(func() { _ = reg.Configure(map[string]string{}) })
	}

	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	lang := codegen.LookupLang("go")
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: platformName}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate against the bundled GIR: %v", err)
	}
	model, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatal("no model.go emitted")
	}
	return string(model)
}
