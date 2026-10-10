package golang

import (
	"fmt"
	"go/types"

	"duckfam.us/sngl/ir"
)

// nativeTypes maps a Go type to the SNGL type it imports as, keyed by the
// type's fully qualified name: its package's import path, a dot, and the type
// name ("time.Duration").
//
// The value is a function and not a *ir.Type because the SNGL side of these
// mappings is not knowable at init: ir.DurationType and ir.DateTimeType hand
// out declarations the checker registers when it loads the standard library,
// and a package-level init runs long before that. Storing the result would
// capture the pre-stdlib fallback and keep it forever.
var nativeTypes = map[string]func() *ir.Type{}

// RegisterType records that the Go type named by its fully qualified name
// imports as the type fn yields. fn is called once per type occurrence in an
// imported package, not at registration, so it may depend on the standard
// library being loaded.
//
// This is the import-side half of a pair: see stdtypes.go for the two
// mappings the compiler ships and for where the encode-side half lives.
//
// A key registered twice panics rather than taking the last writer. Two
// mappings for one Go type mean two callers disagree about what that type is,
// and whichever init ran last would decide it — a difference no build could
// report and no reader could see.
func RegisterType(qualifiedName string, fn func() *ir.Type) {
	if qualifiedName == "" || fn == nil {
		panic("golang: RegisterType needs a qualified name and a type function")
	}
	if _, dup := nativeTypes[qualifiedName]; dup {
		panic(fmt.Sprintf("golang: Go type %q is already registered", qualifiedName))
	}
	nativeTypes[qualifiedName] = fn
}

// lookupNativeType returns the SNGL type registered for a named Go type.
func lookupNativeType(t types.Type) (*ir.Type, bool) {
	named, ok := t.(*types.Named)
	if !ok {
		return nil, false
	}
	obj := named.Obj()
	pkg := obj.Pkg()
	if pkg == nil {
		return nil, false // a predeclared name (error, any) has no package
	}
	fn, ok := nativeTypes[pkg.Path()+"."+obj.Name()]
	if !ok {
		return nil, false
	}
	return fn(), true
}
