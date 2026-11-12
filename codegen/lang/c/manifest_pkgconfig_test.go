package c

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// A c.link's pkgConfig is a manifest's `pkg-config` line, after the ones the
// platform names and not again where it names the same package.
func TestManifestPkgConfig(t *testing.T) {
	links := []*ir.CLink{{Include: "rt.h", PkgConfig: []string{"gtk4", "cairo"}, LDFlags: []string{"-lm"}}}
	got := string(Manifest(links, "gtk4"))
	want := "# What this program links, which `sngl build` reads.\npkg-config gtk4\npkg-config cairo\nflag -lm\n"
	if got != want {
		t.Errorf("Manifest:\n%s\nwant:\n%s", got, want)
	}
}
