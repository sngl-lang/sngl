//go:build !js

package html

import (
	"bytes"
	"testing"
)

func TestSnapshotHTML(t *testing.T) {
	g := &Generator{}
	png, err := g.SnapshotHTML([]byte("<html><body>hi</body></html>"), 200, 100)
	if err != nil {
		t.Skipf("browser not available: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("expected non-empty PNG bytes")
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("expected PNG magic prefix, got %x", png[:min(8, len(png))])
	}
}
