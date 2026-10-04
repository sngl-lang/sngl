package ir

// BuiltinKind marks a declaration as one of the compiler's built-ins. The
// #[builtin] mark sets it during checking -- applyMarks runs from the
// registration functions of pass 1, not before them -- and the checker, this
// package and the optimizer read it. BuiltinNone ("") is an ordinary
// declaration.
//
// Type kinds (primitive, string-repr, generic, target-identity) mark a struct;
// node kinds mark a component. A declaration is at most one kind, so a single field carries them
// all, and the value doubles as the generic-constructor id.
//
// The kind classifies one declaration — it does not make two declarations the
// same type. Type identity is per-declaration (ir.Type.Equal compares Decl), so
// two structs sharing a mark would be two incompatible types, not aliases; the
// checker rejects a duplicated node mark outright.
type BuiltinKind string

const (
	BuiltinNone BuiltinKind = ""

	// Scalar primitives. Only the names with a stdlib struct decl are marked;
	// the mark distinguishes the built-in's own decl from a user declaration
	// that shadows the name. The concrete singleton is held by the compiler
	// (BuiltinScalar), keyed by these names.
	BuiltinInt    BuiltinKind = "int"
	BuiltinFloat  BuiltinKind = "float"
	BuiltinString BuiltinKind = "string"

	// String-representable value types.
	BuiltinColor    BuiltinKind = "color"
	BuiltinDate     BuiltinKind = "date"
	BuiltinTime     BuiltinKind = "time"
	BuiltinDateTime BuiltinKind = "datetime"

	// Unit quantities. A unit kind marks a `unit` declaration, so the
	// compiler can hand out its type where no scope is in reach — a foreign
	// type importer mapping time.Duration, for one.
	BuiltinDuration BuiltinKind = "duration"

	// Generic constructors.
	BuiltinList BuiltinKind = "list"
	BuiltinMap  BuiltinKind = "map"
	BuiltinIter BuiltinKind = "iter"
	// Chan is a host channel. It is declared by sngl:language/go rather than
	// by lib/, because only a language with channels can answer it.
	BuiltinChan   BuiltinKind = "chan"
	BuiltinRef    BuiltinKind = "ref"
	BuiltinOption BuiltinKind = "option"
	// Remote is the box a data adapter returns: the last value that arrived,
	// how the most recent attempt failed, and whether one is in flight. See
	// sngl:remote.
	BuiltinRemote BuiltinKind = "remote"
	// TreeOne narrows a slot's content to exactly one member.
	BuiltinTreeOne BuiltinKind = "treeOne"

	// The families the compiler itself has to name, each marked on the
	// component that declares it so no phase spells a package and a name.
	//
	// TreeFamily is `sngl:build`'s `family`, the family of families: every
	// family is a component that is a member of it, and it is the one member of
	// itself. The mark is where that regress stops.
	BuiltinTreeFamily BuiltinKind = "treeFamily"
	// TreeRoot is the tree a package body accepts: the windows a program opens
	// and the build directive saying what it compiles to. It is what makes
	// those top-level without a syntactic rule naming them.
	BuiltinTreeRoot BuiltinKind = "treeRoot"
	// TreeNode is the widget family. It is the one tree whose members are
	// ordinary components -- a button composes away into its caller the way
	// any wrapper does -- so the passes asking "is this rendered rather than
	// composed" have to tell it from a specialised family.
	BuiltinTreeNode BuiltinKind = "treeNode"
	// TreeShape is the drawing tree. A node hosting its members is a canvas,
	// whose shapes passShapeDraw turns into the statements that paint them.
	BuiltinTreeShape BuiltinKind = "treeShape"
	// TreeLanguage and TreePlatform are `sngl:build`'s `language` and
	// `platform`: the families a build-target node is a member of, which is
	// what makes a component a target and says which tier it is.
	BuiltinTreeLanguage BuiltinKind = "treeLanguage"
	BuiltinTreePlatform BuiltinKind = "treePlatform"

	// Built-in visual nodes. Unlike the type marks above, these annotate a
	// component declaration: the checker dispatches a visual node to the
	// matching compiler construct (ir.ErrorBoundary, ir.ContextProvider) when
	// its target resolves to the marked component,
	// rather than matching a literal name.
	BuiltinContext       BuiltinKind = "context"
	BuiltinErrorBoundary BuiltinKind = "errorBoundary"
	// Effect brackets a lifetime: @mount when the node enters the tree,
	// @unmount when it leaves. `on` makes the bracket a keyed identity, so a
	// changed value ends one lifetime and begins the next. See sngl:builtin.
	BuiltinEffect BuiltinKind = "effect"
	// NavStack, NavPage and NavLink are sngl:ui/nav's stack, page and link,
	// which the compiler answers rather than a body: passNavigation lowers them
	// to plain UI on a target that does not declare `navigation`, and a target
	// that does renders them in its own codegen. A platform package may still
	// override one, which is how sngl:platform/none hands them to the
	// interpreter.
	BuiltinNavStack BuiltinKind = "navStack"
	BuiltinNavPage  BuiltinKind = "navPage"
	BuiltinNavLink  BuiltinKind = "navLink"

	BuiltinOutput BuiltinKind = "output"
	// GenInputs is `sngl:x/gen/cache`'s `inputs`: what a generated file was
	// generated from. Like output it is read rather than rendered, and unlike
	// output a library package may carry it -- a target serves generated
	// source of its own.
	BuiltinGenInputs BuiltinKind = "genInputs"

	// GenEmit and GenNode are `sngl:x/gen`'s `emit` and `node`: what a
	// family's override and a member's override say about the file a build
	// writes for the family. Neither is rendered; the emitter pass reads them
	// where they are written and nowhere else.
	BuiltinGenEmit BuiltinKind = "genEmit"
	BuiltinGenNode BuiltinKind = "genNode"
	// GenScheme is `sngl:x/gen`'s `scheme`: an import scheme a package
	// declares. Read where it is written and taken out of the package before
	// anything renders it.
	BuiltinGenScheme BuiltinKind = "genScheme"
	// CLink is `sngl:x/c`'s `link`: a header and the flags a C preamble
	// includes for the package's `#[cnative]` declarations. Read into
	// Package.CLinks rather than rendered.
	BuiltinCLink BuiltinKind = "cLink"

	// Target identities. An opaque value type each of whose values is one
	// target's build-tree node read as a value -- html.platform,
	// go.language. There is no literal, so a string cannot stand in for one,
	// which is the reason the type exists rather than the name being a string.
	BuiltinPlatform BuiltinKind = "platform"
	BuiltinLanguage BuiltinKind = "language"

	// Predeclared constants. These annotate a const declaration whose written
	// value is a placeholder: the real one is not known until a build picks a
	// target, so the compiler supplies it. The two target kinds are named for
	// what they hold -- the target -- because the identity types above own the
	// bare names.
	BuiltinNull           BuiltinKind = "null"
	BuiltinTargetPlatform BuiltinKind = "targetPlatform"
	BuiltinTargetLanguage BuiltinKind = "targetLanguage"
)

// IsTargetID reports whether the kind marks a target-identity type. Its values
// are synthesized, one per registered target, so nothing declares them and no
// literal spells one.
func (b BuiltinKind) IsTargetID() bool {
	switch b {
	case BuiltinPlatform, BuiltinLanguage:
		return true
	}
	return false
}

// IsPrimitive reports whether the kind is a scalar primitive (int/float/string).
func (b BuiltinKind) IsPrimitive() bool {
	switch b {
	case BuiltinInt, BuiltinFloat, BuiltinString:
		return true
	}
	return false
}

// IsStringRepr reports whether the kind is a string-representable value type
// (color/date/time/datetime).
func (b BuiltinKind) IsStringRepr() bool {
	switch b {
	case BuiltinColor, BuiltinDate, BuiltinTime, BuiltinDateTime:
		return true
	}
	return false
}

// IsUnit reports whether the kind marks a unit declaration.
func (b BuiltinKind) IsUnit() bool {
	return b == BuiltinDuration
}

// IsSlotBound reports whether the kind marks a wrapper read where a slot's type
// is resolved rather than constructed as a type. Nothing builds an ir.Type from
// one, which is why it is not IsGeneric.
func (b BuiltinKind) IsSlotBound() bool {
	return b == BuiltinTreeOne
}

// IsTreeRole reports whether the kind marks a family the compiler itself has
// to name. Six do -- the family of families, the package body's, the widget
// family, the drawing family and the two build-target tiers -- because a phase
// asks after each by role rather than by declaration. Every other family is compared by declaration
// and never spelled.
func (b BuiltinKind) IsTreeRole() bool {
	switch b {
	case BuiltinTreeFamily, BuiltinTreeRoot, BuiltinTreeNode, BuiltinTreeShape,
		BuiltinTreeLanguage, BuiltinTreePlatform:
		return true
	}
	return false
}

// IsGeneric reports whether the kind is a generic type constructor
// (list/map/iter/ref/option/remote).
func (b BuiltinKind) IsGeneric() bool {
	switch b {
	case BuiltinList, BuiltinMap, BuiltinIter, BuiltinChan, BuiltinRef, BuiltinOption, BuiltinRemote:
		return true
	}
	return false
}

// IsNode reports whether the kind marks a built-in visual node. Node kinds are
// stamped on component declarations, not structs.
func (b BuiltinKind) IsNode() bool {
	switch b {
	case BuiltinErrorBoundary, BuiltinContext, BuiltinEffect,
		BuiltinNavStack, BuiltinNavPage, BuiltinNavLink:
		return true
	}
	return false
}

// IsDirective reports whether the kind marks a directive: a component a file's
// root instantiates to say something to the compiler, which is read rather
// than rendered.
func (b BuiltinKind) IsDirective() bool {
	return b == BuiltinOutput || b == BuiltinGenInputs || b == BuiltinGenScheme || b == BuiltinCLink
}

// IsEmitter reports whether the kind marks one of the declarations an emitter
// is written from.
func (b BuiltinKind) IsEmitter() bool {
	return b == BuiltinGenEmit || b == BuiltinGenNode
}

// IsConst reports whether the kind marks a predeclared constant. Const kinds
// are stamped on const declarations, and the compiler replaces the declared
// type and value.
func (b BuiltinKind) IsConst() bool {
	switch b {
	case BuiltinNull, BuiltinTargetPlatform, BuiltinTargetLanguage:
		return true
	}
	return false
}

// AllBuiltinKinds returns every valid kind, in declaration order.
func AllBuiltinKinds() []BuiltinKind {
	return []BuiltinKind{
		BuiltinInt, BuiltinFloat, BuiltinString,
		BuiltinColor, BuiltinDate, BuiltinTime, BuiltinDateTime,
		BuiltinDuration,
		BuiltinList, BuiltinMap, BuiltinIter, BuiltinChan, BuiltinRef, BuiltinOption, BuiltinRemote,
		BuiltinTreeOne, BuiltinTreeFamily, BuiltinTreeRoot, BuiltinTreeNode, BuiltinTreeShape,
		BuiltinTreeLanguage, BuiltinTreePlatform,
		BuiltinErrorBoundary, BuiltinContext, BuiltinEffect,
		BuiltinNavStack, BuiltinNavPage, BuiltinNavLink,
		BuiltinOutput, BuiltinGenInputs, BuiltinGenScheme, BuiltinCLink, BuiltinGenEmit, BuiltinGenNode,
		BuiltinPlatform, BuiltinLanguage,
		BuiltinNull, BuiltinTargetPlatform, BuiltinTargetLanguage,
	}
}

// Valid reports whether the kind names a built-in (i.e. is not BuiltinNone and
// not an unrecognised string).
func (b BuiltinKind) Valid() bool {
	return b.IsPrimitive() || b.IsStringRepr() || b.IsUnit() || b.IsGeneric() ||
		b.IsSlotBound() || b.IsTreeRole() || b.IsNode() || b.IsDirective() || b.IsEmitter() || b.IsTargetID() || b.IsConst()
}
