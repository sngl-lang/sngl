package goldentest

import (
	"flag"
	"fmt"
	"reflect"
	"strings"

	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

var fingerprintMode = flag.String("golden.fingerprint", "", `"each" fingerprints the library around every build, naming the fixture whose build wrote it, rather than once for the whole run`)

// libraryGuard holds every build of a run to leaving the checked package and
// the library as it found them. The program is fingerprinted around each
// build. The library is checked once per process and shared by every fixture,
// so by default it is fingerprinted when a fixture first reaches a part of it
// and again when the run ends: a write from any build is still seen, but not
// which build made it.
type libraryGuard struct {
	each    bool
	program testutil.Fingerprinter
	// reached is every library object a program's fingerprint stopped at,
	// and pending those not yet in before.
	reached map[uintptr]any
	// targets are the library's overrides the program being fingerprinted
	// found, which it holds beside its own.
	targets map[uintptr]bool
	pending []any
	before  testutil.Graph
	declPkg map[reflect.Type][]int
}

func newLibraryGuard(mode string) (*libraryGuard, error) {
	g := &libraryGuard{reached: map[uintptr]any{}, targets: map[uintptr]bool{}, before: testutil.Graph{}, declPkg: map[reflect.Type][]int{}}
	switch mode {
	case "":
		g.program.Skip = g.notProgram
	case "each":
		g.each = true
		g.program.Skip = notIR
	default:
		return nil, fmt.Errorf(`-golden.fingerprint=%q: want "each" or nothing`, mode)
	}
	return g, nil
}

// around fingerprints pkg before and after build, and reports whether build
// wrote it -- or, fingerprinting each build, the library it reaches.
func (g *libraryGuard) around(pkg *ir.Package, build func() error) error {
	clear(g.targets)
	for _, o := range pkg.Targets.Overrides {
		g.targets[reflect.ValueOf(o).Pointer()] = true
	}
	for _, o := range pkg.Overlay.Overrides {
		delete(g.targets, reflect.ValueOf(o).Pointer())
	}
	before := g.program.Of(pkg)
	g.baseline()
	if err := build(); err != nil {
		return err
	}
	if g.program.Of(pkg) == before {
		return nil
	}
	if g.each {
		return fmt.Errorf("the build wrote the checked package or the library it reaches")
	}
	return fmt.Errorf("the build wrote the checked package")
}

// finish reports what the run's builds wrote of the library.
func (g *libraryGuard) finish() error {
	if g.each || len(g.reached) == 0 {
		return nil
	}
	roots := make([]any, 0, len(g.reached))
	for _, v := range g.reached {
		roots = append(roots, v)
	}
	f := testutil.Fingerprinter{Skip: notIR}
	after := f.Graph(roots)
	delete(after, 0)
	changed := g.before.Changed(after)
	if len(changed) == 0 {
		return nil
	}
	const shown = 20
	more := ""
	if len(changed) > shown {
		more = fmt.Sprintf("\n... and %d more", len(changed)-shown)
		changed = changed[:shown]
	}
	return fmt.Errorf("a build wrote the library; rerun with -golden.fingerprint=each to name the fixture:\n%s%s", strings.Join(changed, "\n"), more)
}

// baseline fingerprints the library a program reached that no earlier one
// had, before any build can write it.
func (g *libraryGuard) baseline() {
	if len(g.pending) == 0 {
		return
	}
	f := testutil.Fingerprinter{Skip: func(v reflect.Value) bool {
		_, seen := g.before[v.Pointer()]
		return seen || notIR(v)
	}}
	for addr, n := range f.Graph(g.pending) {
		if addr != 0 {
			g.before[addr] = n
		}
	}
	g.pending = g.pending[:0]
}

var pkgType = reflect.TypeFor[*ir.Package]()

// notProgram stops at the AST and at the library: a library package, a
// declaration one of them declared, and an override one of them wrote. Each
// library object it stops at is noted for baseline.
func (g *libraryGuard) notProgram(v reflect.Value) bool {
	if notIR(v) {
		return true
	}
	if g.targets[v.Pointer()] {
		g.note(v)
		return true
	}
	lib := g.libraryOf(v)
	if !lib.IsValid() {
		return false
	}
	g.note(v)
	g.note(lib)
	return true
}

func (g *libraryGuard) libraryOf(v reflect.Value) reflect.Value {
	if v.Type() == pkgType {
		if v.Interface().(*ir.Package).Library {
			return v
		}
		return reflect.Value{}
	}
	elem := v.Type().Elem()
	index, ok := g.declPkg[elem]
	if !ok {
		if elem.Kind() == reflect.Struct {
			if field, found := elem.FieldByName("DeclInfo"); found && field.Anonymous {
				pkg, _ := elem.FieldByName("Pkg")
				index = pkg.Index
			}
		}
		g.declPkg[elem] = index
	}
	if index == nil {
		return reflect.Value{}
	}
	p := v.Elem().FieldByIndex(index)
	if p.IsNil() || !p.Interface().(*ir.Package).Library {
		return reflect.Value{}
	}
	return p
}

func (g *libraryGuard) note(v reflect.Value) {
	addr := v.Pointer()
	if _, ok := g.reached[addr]; ok {
		return
	}
	g.reached[addr] = v.Interface()
	if _, ok := g.before[addr]; !ok {
		g.pending = append(g.pending, v.Interface())
	}
}
