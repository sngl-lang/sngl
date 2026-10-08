package lib_test

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/lib"
)

// macroDeclRE matches a macro declaration: a func whose return type is
// `Macro`, qualified or not. The qualified form is what lib/ actually writes —
// sngl:internal/ir is imported under an alias so that a dot import of the
// mark's package does not lift the type on — but the unqualified form is legal
// and should count too.
var macroDeclRE = regexp.MustCompile(`(?m)^func\s+([A-Za-z_]\w*)\s*\([^)]*\)\s+(?:\w+\.)?Macro\b`)

// TestEveryDeclaredMacroIsImplemented checks the one direction that is still
// possible to get wrong. A macro exists because a lib package declares it: the
// name, the argument list and the documentation are all read off the
// declaration, and the compiler adds only an implementation. So a Go entry
// with no declaration is unreachable rather than wrong — nothing resolves to
// it — while a declaration with no implementation is a mark that resolves and
// then fails at every use site, far from here.
//
// It reads the loaded packages rather than the source: a macro is a declared
// signature, and what the checker dispatches on is the declaration it built.
func TestEveryDeclaredMacroIsImplemented(t *testing.T) {
	declared := 0
	for _, name := range lib.Packages() {
		pkg := checker.LibPackage(name)
		if pkg == nil {
			continue
		}
		for _, m := range pkg.Macros {
			declared++
			if !checker.MacroIsImplemented(name, m.Name) {
				t.Errorf("sngl:%s declares `func %s(...) Macro` but the compiler implements no mark for it; "+
					"#[%s] would resolve and then fail at every use site", name, m.Name, m.Name)
			}
		}
	}
	if declared == 0 {
		t.Fatal("no macro declarations found in lib/")
	}
}

// A macro is looked up by name, so its declaration has to carry the prose that
// says what the mark does. `sngl doc` renders a bare signature otherwise, the
// way the draw shapes did before TestExportedComponentsAreDocumented.
func TestDeclaredMacrosAreDocumented(t *testing.T) {
	var undocumented []string
	err := fs.WalkDir(lib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sngl") {
			return err
		}
		src, err := lib.FS.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			m := macroDeclRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//") {
				undocumented = append(undocumented, fmt.Sprintf("%s: %s", p, m[1]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(undocumented)
	for _, u := range undocumented {
		t.Errorf("macro %s has no doc comment", u)
	}
}
