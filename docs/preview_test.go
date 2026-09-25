package docs

import (
	"strings"
	"testing"
)

// A preview that fails to compile is dropped without a word, and with it the
// whole preview section, snapshots included.
func TestLibraryExamplesPreview(t *testing.T) {
	n := 0
	for _, c := range LibraryComponents() {
		if len(c.Examples) == 0 {
			continue
		}
		n++
		html := compilePreview(c.Examples[0])
		if !strings.Contains(html, "<body>") {
			t.Errorf("%s: example compiled to no preview", c.Name)
		}
	}
	if n == 0 {
		t.Fatal("no library component carries an example")
	}
}
