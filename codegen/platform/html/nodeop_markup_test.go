//go:build !js

package html

import (
	"strings"
	"testing"
)

// A node operation is not markup. The static renderer promotes a bodyless call
// in a visual body to an element, and the flattened body of a component that
// survives inlining is nothing but such calls -- so `lower.AppendChild(...)`
// was written into the page as `<AppendChild>`, a tag no browser knows and the
// author never wrote.
//
// `chain` is recursive, so it survives inlining and its body is flattened;
// `main` still holds a node for it, which is what puts the flattened body in
// front of the renderer that writes markup.
func TestNodeOp_IsNeverRenderedAsAnElement(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:macro"
import app "sngl:app"
component panel(header component) node {
    vbox { header }
}
component chain(#[construct] n int) node {
    panel() {
        component header {
            text(value="[" + string(n) + "]")
            if n > 0 {
                chain(n=n - 1)
            }
        }
    }
}
app.window { chain(n=2) }
`
	page := renderComponentHTML(t, src)
	for _, op := range []string{"AppendChild", "CreateNode", "CreateComponent", "ComponentRoot"} {
		if strings.Contains(page, "<"+op) {
			t.Errorf("node operation %s reached the markup as an element:\n%s", op, page)
		}
	}
}
