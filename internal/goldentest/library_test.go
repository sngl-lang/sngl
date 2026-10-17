package goldentest

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

type guardFixture struct {
	prog, lib  *ir.Package
	mine, base *ir.Component
	override   *ir.Overrides
}

func newGuardFixture() guardFixture {
	lib := &ir.Package{Path: "sngl:x", Library: true}
	base := &ir.Component{Name: "base", Pkg: lib}
	lib.Components = []*ir.Component{base}
	prog := &ir.Package{Imports: []*ir.Import{{Path: "sngl:x", Pkg: lib}}}
	mine := &ir.Component{Name: "mine", Pkg: prog}
	prog.Components = []*ir.Component{mine}
	override := &ir.Overrides{Platform: map[string]ir.Override{"html": {}}}
	prog.Targets.Overrides = map[ir.Symbol]*ir.Overrides{base: override}
	return guardFixture{prog: prog, lib: lib, mine: mine, base: base, override: override}
}

// Fingerprinting the library once per run, a build's write of it is reported
// when the run ends, and a write of the program by the build that made it.
func TestLibraryGuardOncePerRun(t *testing.T) {
	writes := map[string]func(guardFixture){
		"nothing":             func(guardFixture) {},
		"library declaration": func(f guardFixture) { f.base.Doc = "written" },
		"library override":    func(f guardFixture) { f.override.Language = map[string]ir.Override{"go": {}} },
		"program declaration": func(f guardFixture) { f.mine.Doc = "written" },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			f := newGuardFixture()
			g, err := newLibraryGuard("")
			if err != nil {
				t.Fatal(err)
			}
			if err := g.around(f.prog, func() error { return nil }); err != nil {
				t.Fatalf("a build writing nothing: %v", err)
			}
			buildErr := g.around(f.prog, func() error { write(f); return nil })
			runErr := g.finish()
			if name == "nothing" {
				if buildErr != nil || runErr != nil {
					t.Fatalf("builds writing nothing: build %v, run %v", buildErr, runErr)
				}
				return
			}
			if f.mine.Doc != "" {
				if buildErr == nil || runErr != nil {
					t.Fatalf("a write of the program: build %v, run %v", buildErr, runErr)
				}
				return
			}
			if buildErr != nil {
				t.Fatalf("a write of the library reported by the build: %v", buildErr)
			}
			if runErr == nil || !strings.Contains(runErr.Error(), "-golden.fingerprint=each") {
				t.Fatalf("a write of the library at the end of the run: %v", runErr)
			}
		})
	}
}

func TestLibraryGuardEachBuild(t *testing.T) {
	f := newGuardFixture()
	g, err := newLibraryGuard("each")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.around(f.prog, func() error { f.base.Doc = "written"; return nil }); err == nil || !strings.Contains(err.Error(), "library") {
		t.Fatalf("a write of the library reported by the build that made it: %v", err)
	}
	if err := g.finish(); err != nil {
		t.Fatalf("each build already answered for its own writes: %v", err)
	}
}
