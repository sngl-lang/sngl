package html

import (
	"strings"
	"testing"
)

// A component that gates its body on the target must render ONLY the taken
// branch on html — the default is the `else`, not a sibling. Regression for
// the website rendering its sidebar and content twice: a `platform html { … }`
// block dropped its default siblings implicitly, and the collapse depended on
// the optimizer's component-specialization heuristic, so a component taking
// the non-specialized inline path leaked its default in beside the override.
// Written as a branch there is nothing to infer and nothing to leak.
func TestPlatformOverrideDropsDefault(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:platform/html"
import app "sngl:app"
component Layout(children ...component) node {
    if PLATFORM == html.platform {
        html.div(class="site") {
            html.hr
            children
        }
    } else {
        vbox { text(value="DEFAULT_BODY") }
    }
}
app.window {
    Layout() { text(value="CHILD") }
}
`
	out := generateMainPage(t, src)

	if strings.Contains(out, "DEFAULT_BODY") {
		t.Errorf("cross-platform default body leaked into html output (should be overridden):\n%s", out)
	}
	if !strings.Contains(out, `class="site"`) {
		t.Errorf("html branch did not render:\n%s", out)
	}
	if n := strings.Count(out, "CHILD"); n != 1 {
		t.Errorf("slot child rendered %d times, want exactly 1:\n%s", n, out)
	}
}
