package android

import (
	"strings"
	"testing"
)

// TestComputedReferenceResolves guards that a component computed referenced
// from a view attribute resolves to its derivedStateOf property (a bare name),
// rather than the "/* unresolved method main.X */" marker that produced invalid
// Kotlin. Regression for the kotlin evalTypeMethodCall component-self fix.
func TestComputedReferenceResolves(t *testing.T) {
	src := `import . "sngl://std"
component main {
    var count = 0
    func doubled() => count * 2
    vbox {
        text(value=string(doubled))
        text(value="{doubled}")
    }
}`
	out := compileCanvasSrc(t, src)
	if strings.Contains(out, "unresolved method") {
		t.Errorf("computed reference left an unresolved-method marker:\n%s", out)
	}
	if !strings.Contains(out, "val doubled by remember") {
		t.Errorf("expected a derivedStateOf declaration for doubled:\n%s", out)
	}
	if !strings.Contains(out, "doubled.toString()") {
		t.Errorf("expected bare computed reference doubled.toString():\n%s", out)
	}
}
