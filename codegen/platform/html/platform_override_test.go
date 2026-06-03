package html

import (
	"strings"
	"testing"
)

// A component with a cross-platform default body and a `platform html { … }`
// override must render ONLY the override on html — the default is dropped, not
// rendered alongside it. Regression for the website rendering its sidebar and
// content twice: the override collapse used to depend on the optimizer's
// component-specialization heuristic, so when a component took the
// non-specialized inline path its default body leaked in beside the override.
// passPlatformFilter now resolves the override deterministically in lowering.
func TestPlatformOverrideDropsDefault(t *testing.T) {
	src := `
component Layout() list<component> {
    vbox { text(value="DEFAULT_BODY") }
    platform html {
        html.div(class="site") {
            html.hr
            slot
        }
    }
}
component main {
    Layout() { text(value="CHILD") }
}
`
	out := generateMainPage(t, src)

	if strings.Contains(out, "DEFAULT_BODY") {
		t.Errorf("cross-platform default body leaked into html output (should be overridden):\n%s", out)
	}
	if !strings.Contains(out, `class="site"`) {
		t.Errorf("platform html override body did not render:\n%s", out)
	}
	if n := strings.Count(out, "CHILD"); n != 1 {
		t.Errorf("slot child rendered %d times, want exactly 1:\n%s", n, out)
	}
}
