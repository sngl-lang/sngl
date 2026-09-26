package docs

import (
	"strings"
	"testing"
)

// A preview that fails to compile renders as nothing at all, so a library
// example drifting from the language empties its component page in silence.
func TestLibraryExamplesRenderPreviews(t *testing.T) {
	n := 0
	for _, c := range LibraryComponents() {
		if len(c.Examples) == 0 {
			continue
		}
		n++
		src := c.Examples[0]
		if strings.Contains(src, "import . ") {
			t.Errorf("%s: example dot-imports:\n%s", c.Name, src)
		}
		if c.PreviewHTML == "" {
			t.Errorf("%s: example does not compile to a preview:\n%s", c.Name, src)
		}
	}
	if n == 0 {
		t.Fatal("no library component carries an example")
	}
}
