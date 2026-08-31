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
// ignoring a kind that names no collection. Called from registerStructShell,
// so the pointer's type parameters are filled in after this returns.
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

// ListDecl and MapDecl return the registered declaration, nil before the
// stdlib has loaded.
func ListDecl() *StructDef { return listStructDef.Load() }
func MapDecl() *StructDef  { return mapStructDef.Load() }
