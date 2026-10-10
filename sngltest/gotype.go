package sngltest

import (
	"fmt"
	"go/types"
	"os"
	"reflect"
	"sync"

	"duckfam.us/sngl/codegen/scheme/golang"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/consteval"
)

// mapping is one Go type and the SNGL type a go: import gives it.
type mapping struct {
	rt   reflect.Type // the type under test
	base reflect.Type // the declared type at the bottom of it, past any slice or pointer

	want     *ir.Type // the SNGL type of rt
	baseWant *ir.Type // the SNGL type of base
}

// resolve maps rt the way a go: import of its package does: the importer
// loads the package, and the SNGL type comes back from the same goTypeToIR
// the compiler runs. Nothing here restates that rule, so a test cannot pass
// against a mapping the compiler does not make.
func resolve(rt reflect.Type) (*mapping, error) {
	if rt == nil {
		return nil, fmt.Errorf("sngltest: no type to check")
	}
	base, wrap, err := peel(rt)
	if err != nil {
		return nil, err
	}
	m, err := mapperFor(base.PkgPath())
	if err != nil {
		return nil, fmt.Errorf("sngltest: resolving %s: %w", rt, err)
	}
	gt, err := m.Lookup(base.Name())
	if err != nil {
		return nil, fmt.Errorf("sngltest: resolving %s: %w", rt, err)
	}
	want, usable := m.Type(wrap(gt))
	// The base's own SNGL type is what a MarshalSNGL on it has to write: for
	// []Blob the value is a list of whatever Blob writes, so pointing the
	// reader at list<list<int>> would name a form no marshaller can produce.
	baseWant, _ := m.Type(gt)
	out := &mapping{rt: rt, base: base, want: want, baseWant: baseWant}
	if !usable {
		return nil, out.unusable()
	}
	return out, nil
}

// peel strips the slice and pointer layers off rt down to a declared type, and
// returns the function that puts them back on the go/types side. Rebuilding
// the shape there rather than mapping it here keeps the one rule in the
// importer: only the type at the bottom has to be looked up.
func peel(rt reflect.Type) (reflect.Type, func(types.Type) types.Type, error) {
	wrap := func(t types.Type) types.Type { return t }
	for rt.Name() == "" {
		inner := wrap
		switch rt.Kind() {
		case reflect.Slice:
			wrap = func(t types.Type) types.Type { return inner(types.NewSlice(t)) }
		case reflect.Pointer:
			wrap = func(t types.Type) types.Type { return inner(types.NewPointer(t)) }
		default:
			return nil, nil, fmt.Errorf(`%s has no name, so there is no declaration for the go: importer to read.

Only a declared type, a predeclared one, and slices and pointers of those can
be resolved — which is also what a go: function has to return for the
importer to say anything about it. Name the type and check that:

    type X %s`, rt, rt)
		}
		rt = rt.Elem()
	}
	return rt, wrap, nil
}

// unusable is the message for a type the importer models as nothing.
func (m *mapping) unusable() error {
	return fmt.Errorf(`%s has no SNGL type.

    Go type       %s
    imports as    %s, which the go: importer marks unusable

Every declaration that mentions this type is rejected, so a go: function
returning it cannot be called from SNGL at all — whatever its MarshalSNGL
writes. Return a type the importer models (a struct declared in the same
package, a scalar, or a slice of those), or map this one to a SNGL type with
golang.RegisterType(%q, ...) in the compiler.`,
		m.goName(), m.goTypeLine(), m.want, m.qualified())
}

// goName is how the reader spells the type under test.
func (m *mapping) goName() string { return m.rt.String() }

// goTypeLine names the declaration the importer read, by the path it is
// declared under — which is the key golang.RegisterType takes.
func (m *mapping) goTypeLine() string {
	if m.rt == m.base {
		return m.qualified()
	}
	return fmt.Sprintf("%s, as %s", m.qualified(), m.rt)
}

// qualified is the type's import path and name, the form nativeTypes is keyed
// by ("time.Duration").
func (m *mapping) qualified() string {
	if m.base.PkgPath() == "" {
		return m.base.Name() // predeclared: no package to qualify with
	}
	return m.base.PkgPath() + "." + m.base.Name()
}

// marshaler reports whether the type writes its own SNGL form. The pointer
// method set counts, because the encoder takes it for an addressable value.
func (m *mapping) marshaler() bool {
	i := reflect.TypeFor[consteval.Marshaler]()
	return m.base.Implements(i) || reflect.PointerTo(m.base).Implements(i)
}

var (
	mapperMu sync.Mutex
	mappers  = map[string]mapperResult{}
)

type mapperResult struct {
	m   *golang.TypeMapper
	err error
}

// mapperFor loads a package once per test binary. Loading is a full go/packages
// type-check of the package and its dependencies, which a table of values
// would otherwise pay for per value.
func mapperFor(pkgPath string) (*golang.TypeMapper, error) {
	mapperMu.Lock()
	defer mapperMu.Unlock()
	if r, ok := mappers[pkgPath]; ok {
		return r.m, r.err
	}
	r := mapperResult{}
	// The importer maps time.Duration and time.Time through declarations
	// sngl:time registers when it loads. A compile loads it before any import
	// resolves; a caller reaching the importer on its own has to say so.
	if checker.LibPackage("time") == nil {
		r.err = fmt.Errorf("loading sngl:time, which declares the types a Go time.Time maps to")
	} else {
		dir, err := os.Getwd()
		if err != nil {
			r.err = err
		} else {
			r.m, r.err = golang.LoadTypes(pkgPath, dir)
		}
	}
	mappers[pkgPath] = r
	return r.m, r.err
}
