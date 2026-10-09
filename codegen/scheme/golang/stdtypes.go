package golang

import "duckfam.us/sngl/ir"

// The Go types the compiler itself maps, and the import-side, do-not-own-the-
// type cell of a matrix with two axes: which direction a value crosses, and
// whether whoever maps it owns the Go type.
//
//	                 own the type   do not own it
//	encode (Go→SNGL) MarshalSNGL    consteval.Register[T]
//	import (type)    —              golang.RegisterType    (here)
//
// The encode row lives in pkg/go/consteval/stdtypes.go, which registers the
// same two types in the same order. A type needs both halves: without this one
// the compiler types the value by its Go underlying type, without that one the
// child program encodes it by reflection. The empty cell has no mechanism —
// a third party can reach the importer only through this registry.
func init() {
	// SNGL models a duration as a unit value whose base is milliseconds, and
	// its Go underlying type is an int64, so without this a Duration imports
	// as a bare int and disagrees with the `250ms` the encoder writes.
	RegisterType("time.Duration", ir.DurationType)
	// A datetime is a string-domain type; its Go underlying type is a struct,
	// which would otherwise import as an opaque dyn.
	RegisterType("time.Time", ir.DateTimeType)
}
