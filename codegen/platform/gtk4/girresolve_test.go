package gtk4

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
)

// The bundled subset must cover every widget lib/platforms/gtk4 names, or a
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
