package golang

import (
	"fmt"
	"go/types"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TypeMapper answers, for one Go package, the question an import of that
// package answers about each of its types: which SNGL type is this?
//
// It exists so a caller outside the compiler can ask that question without
// restating the rule. The mapping lives in goTypeToIR and nowhere else; a
// second implementation would agree with it exactly until the day it did not.
type TypeMapper struct {
	pkg     *types.Package
	path    string
	structs map[string]*ir.StructDef
}

// LoadTypes loads the Go package at pkgPath — resolved from dir, as a go:
// import of it would be — and returns a mapper over its types.
//
// A pkgPath of "" yields a mapper over no package, which still resolves the
// predeclared names: a predeclared type belongs to no package to load.
func LoadTypes(pkgPath, dir string) (*TypeMapper, error) {
	if pkgPath == "" {
		return &TypeMapper{}, nil
	}
	ni, structs, pkg, err := (&GoImporter{}).load(pkgPath, dir)
	if err != nil {
		return nil, err
	}
	return &TypeMapper{pkg: pkg, path: ni.ImportPath, structs: structs}, nil
}

// Lookup returns the type declared under name: in the loaded package, or among
// the predeclared names when the mapper has none. The name is the Go spelling
// of the declaration, so byte is uint8 and rune is int32.
func (m *TypeMapper) Lookup(name string) (types.Type, error) {
	scope := types.Universe
	where := "the predeclared names"
	if m.pkg != nil {
		scope = m.pkg.Scope()
		where = m.path
	}
	obj := scope.Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("%s declares no type %s", where, name)
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("%s declares %s, but not as a type", where, name)
	}
	return tn.Type(), nil
}

// Type maps a Go type to the SNGL type it imports as, and reports whether that
// type is usable: a false second result is what makes the go: importer mark
// a declaration Unusable, so no value of the type can reach a program.
func (m *TypeMapper) Type(t types.Type) (*ir.Type, bool) {
	return goTypeToIR(t, m.path, m.structs)
}
