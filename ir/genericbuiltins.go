package ir

import "sync/atomic"

// The #[builtin]-marked `list` and `map` declarations, for ListOf/MapOf to
// attach. Atomic because those are called from every phase while another
// checker may still be loading its own stdlib.
//
// Metadata, never identity: Equal compares a collection by its element types,
// which is what lets each check load its own library copy without two list
// types becoming two types.
var (
	listStructDef atomic.Pointer[StructDef]
	mapStructDef  atomic.Pointer[StructDef]
)

// RegisterGenericBuiltin records a #[builtin]-marked collection declaration,
// ignoring a kind that names no collection. Called as the declaration
// registers, with the kind read off its source, and before its type
// parameters are filled in: a type its own package resolves after that
// attaches this declaration, and one resolved before would attach a previous
// check's.
func RegisterGenericBuiltin(sd *StructDef) {
	if sd == nil {
		return
	}
	switch sd.Builtin {
	case BuiltinList:
		listStructDef.Store(sd)
	case BuiltinMap:
		mapStructDef.Store(sd)
	}
}
