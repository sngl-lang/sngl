package golang

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

// A c.link's pkgConfig is cgo's own directive, each package once, ahead of
// the flags the links name.
func TestCPreambleLinesPkgConfig(t *testing.T) {
	links := []*ir.CLink{
		{Include: "gtk/gtk.h", System: true, PkgConfig: []string{"gtk4"}, LDFlags: []string{"-lm"}},
		{Include: "rt.h", PkgConfig: []string{"gtk4", "cairo"}},
	}
	got := CPreambleLines(links)
	want := "#cgo pkg-config: gtk4 cairo\n#cgo LDFLAGS: -lm\n#include <gtk/gtk.h>\n#include \"rt.h\"\n"
	if got != want {
		t.Errorf("CPreambleLines:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(got, "pkg-config") != 1 {
		t.Errorf("pkg-config written more than once:\n%s", got)
	}
}
