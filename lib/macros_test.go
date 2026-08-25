package lib_test

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/lib"

	// A macro registers from its package's init(), so the registry is only
	// populated once every macro package is linked in. The checker imports
	// them all for the same reason.
	_ "git.duckfam.us/jonathan/sngl/internal/checker"
	// The checker links the marks it expands over lib/ itself; #[draw.shape]
	// is a program-facing mark and reaches the registry through cmd/sngl.
	_ "git.duckfam.us/jonathan/sngl/internal/macros/draw"
)

// macroDeclRE matches a macro declaration: a func whose return type is
// `Macro`, qualified or not. The qualified form is what lib/ actually writes —
// sngl://internal/ir is imported under an alias so that a dot import of the
// mark's package does not lift the type on — but the unqualified form is legal
// and should count too.
var macroDeclRE = regexp.MustCompile(`(?m)^func\s+([A-Za-z_]\w*)\s*\([^)]*\)\s+(?:\w+\.)?Macro\b`)

// TestEveryMacroIsDeclared pins the macro registry against the declarations,
// so the two cannot drift while both exist.
//
// A macro's declaration is the only place its documentation and argument list
// can be read: `sngl doc sngl://platforms` renders the declaration, not the Go
// handler. So a registration with no declaration is a macro nobody can look
// up, and a declaration with no registration is a mark that expands to nothing
// — the `#[...]` would fail to resolve at a use site far from here.
func TestEveryMacroIsDeclared(t *testing.T) {
	declared := map[string]bool{}
	err := fs.WalkDir(lib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sngl") {
			return err
		}
		src, err := lib.FS.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range macroDeclRE.FindAllStringSubmatch(string(src), -1) {
			declared[path.Dir(p)+"."+m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 {
		t.Fatal("no `func ...() Macro` declarations found in lib/")
	}

	registered := map[string]bool{}
	for uri, names := range expand.Registered() {
		for _, name := range names {
			key := uri + "." + name
			registered[key] = true
			if !declared[key] {
				t.Errorf("macro #[%s] is registered under sngl://%s but no `func %s(...) Macro` declares it there; "+
					"nothing can document it", name, uri, name)
			}
		}
	}
	for key := range declared {
		if !registered[key] {
			uri, name, _ := strings.Cut(key, ".")
			t.Errorf("sngl://%s declares `func %s(...) Macro` but no macro is registered under that package and name; "+
				"#[%s] would not resolve", uri, name, name)
		}
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
