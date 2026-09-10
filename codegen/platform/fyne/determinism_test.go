package fyne

import (
	"strings"
	"testing"
)

// Two widgets from one module, generated repeatedly: the import block and
// every reference must be byte-identical every time. Resolving aliases out of
// a Go map made this a coin flip.
func TestRepeatedGenerationIsIdentical(t *testing.T) {
	src := strings.Replace(versionedWidgetSrc, "window {", `
component Gauge2(level int) node {
    fyne.Widget(
        spec=fyne.Spec{
            new=fyne.Native{path="github.com/example/fyne-charts/v2", name="NewDial"},
            goType=fyne.Native{path="github.com/example/fyne-charts/v2", name="*Dial"},
            args=[fyne.Arg{raw="1"}],
        },
    ) {}
}

window {`, 1)
	if !strings.Contains(src, "Gauge2(level int)") {
		t.Fatal("the second widget was not spliced in; the fixture asserts nothing")
	}
	src = strings.Replace(src, "        Gauge(:level=reading", "        Gauge2(level=1)\n        Gauge(:level=reading", 1)

	first := generateFyneGo(t, src)
	for i := range 25 {
		if got := generateFyneGo(t, src); got != first {
			t.Fatalf("run %d differs from run 0", i+1)
		}
	}
	if !strings.Contains(first, "github.com/example/fyne-charts/v2") {
		t.Fatalf("fixture did not import the module; asserts nothing\n%s", first)
	}
}
