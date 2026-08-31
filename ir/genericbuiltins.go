package ir

import "sync/atomic"

// The #[builtin]-marked generic collection declarations, published by the
// checker as it registers them.
//
// `list` and `map` are declared in lib/builtin the same way `color` is —
// `#[builtin("list")] struct list<T> {}`, with generic methods written against
// its type parameters. But a struct-backed built-in resolves to a TypeStruct
// carrying its StructDef, whereas these resolve to their own kind, and the
// declaration the mark was read off was then dropped: `list<int>` was
// `{Kind: TypeList, Elems: [int], Decl: nil}`, and nothing downstream could
// reach the declaration or its type parameters. That is why ir.Intrinsics
// spelled its collection signatures with dyn.
//
// Every list type is built through ListOf and every map type through MapOf, so
// attaching the declaration there is what makes it consistent: an annotated
// `list<int>`, one inferred from a literal, and one a lowering pass
// synthesizes all carry the same declaration. Setting it only where an
// annotation is resolved would be worse than leaving it nil.
//
// Registration is by pointer, before the declaration's type parameters are
// resolved (registerStructShell runs first), so a reader sees the finished
// declaration. Atomic because ListOf is called from every phase while another
// checker may still be loading its own stdlib.
//
// Every check loads its own copy of the library, so the last one to register
// wins and two list types built either side of that carry two declarations of
// the same source. That is why this is metadata and not identity: Equal
// compares a list by its element type and never looks at Decl. A struct-backed
// built-in is the other way round — its declaration *is* its identity, which
// is what TestLibraryTypeSurvivesASecondLoad guards.
var (
	listStructDef atomic.Pointer[StructDef]
	mapStructDef  atomic.Pointer[StructDef]
)

// RegisterGenericBuiltin records a #[builtin]-marked generic collection
// declaration. Idempotent, and ignores a kind that names no collection.
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

// ListDecl and MapDecl return the registered declaration, or nil before the
// stdlib has been loaded. A type built then carries no declaration, which is
// why callers read it off the type rather than from here.
func ListDecl() *StructDef { return listStructDef.Load() }
func MapDecl() *StructDef  { return mapStructDef.Load() }
