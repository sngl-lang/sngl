package fyne

import (
	"strings"
	"testing"
)

// TestStylesDedupeToThemes: Fyne has no per-widget colour, only a theme, and
// container.ThemeOverride to scope one to a subtree. So the paint half of
// style becomes a table of themes -- two widgets styled alike share one
// declaration, and each styled widget is wrapped in the override naming it.
func TestStylesDedupeToThemes(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    hbox {
        button(text="a", style={background=#2c2f36, color=#f5f7fa, fontSize=22, fontWeight=bold, borderRadius=8})
        button(text="b", style={background=#2c2f36, color=#f5f7fa, fontSize=22, fontWeight=bold, borderRadius=8})
        button(text="c", style={background=#f59e0b})
    }
}
`)
	for _, want := range []string{
		"fynetheme.Theme{Theme: theme.DefaultTheme()",
		"Background: color.NRGBA{R: 44, G: 47, B: 54, A: 255}",
		"Foreground: color.NRGBA{R: 245, G: 247, B: 250, A: 255}",
		"TextSize: 22",
		"Radius: 8",
		"Bold: true",
		"Background: color.NRGBA{R: 245, G: 158, B: 11, A: 255}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("emitted Go missing %q\n--- generated ---\n%s", want, out)
		}
	}
	// Two buttons, one style: one theme, and no third for the pair.
	if n := strings.Count(out, "var _snglTheme"); n != 2 {
		t.Errorf("three styled buttons over two distinct styles declared %d themes, want 2\n--- generated ---\n%s", n, out)
	}
	if n := strings.Count(out, "container.NewThemeOverride("); n != 3 {
		t.Errorf("wrapped %d widgets, want one override per styled widget (3)\n--- generated ---\n%s", n, out)
	}
}

// TestUnstyledWidgetsAreNotWrapped: an override costs a widget in the tree and
// a Refresh on every theme change, so a widget that asked for no paint styling
// is appended as itself.
func TestUnstyledWidgetsAreNotWrapped(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    hbox(style={gap=4}) {
        button(text="a")
        text(value="b")
    }
}
`)
	if strings.Contains(out, "NewThemeOverride") {
		t.Errorf("unstyled widgets should not be wrapped\n--- generated ---\n%s", out)
	}
	if strings.Contains(out, "snglTheme") {
		t.Errorf("a program with no paint styling should declare no themes\n--- generated ---\n%s", out)
	}
}

// TestLayoutOnlyStyleDeclaresNoTheme: flex and gap are answered by the layout,
// not by a theme -- a box that only positions its children needs neither an
// override nor a declaration.
func TestLayoutOnlyStyleDeclaresNoTheme(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    hbox(style={gap=4, padding=6}) {
        text(value="a", style={flex=1, margin=3})
    }
}
`)
	if strings.Contains(out, "snglTheme") {
		t.Errorf("layout-only style should declare no theme\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "fynelayout.New(") {
		t.Errorf("layout-only style should still reach the weighted layout\n--- generated ---\n%s", out)
	}
}
