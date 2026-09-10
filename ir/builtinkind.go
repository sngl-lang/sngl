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
	BuiltinList   BuiltinKind = "list"
	BuiltinMap    BuiltinKind = "map"
	BuiltinIter   BuiltinKind = "iter"
	BuiltinRef    BuiltinKind = "ref"
	BuiltinOption BuiltinKind = "option"
	// Remote is the box a data adapter returns: the last value that arrived,
	// how the most recent attempt failed, and whether one is in flight. See
	// sngl:remote.
	BuiltinRemote BuiltinKind = "remote"
	// TreeOne narrows a slot's content to exactly one member.
	BuiltinTreeOne BuiltinKind = "treeOne"

	// The three trees the compiler itself has to name, each marked on the
	// struct that declares it so no phase spells a package and a name.
	//
	// TreeRoot is the tree a package body accepts: the windows a program opens
	// and the build directive saying what it compiles to. It is what makes
	// those top-level without a syntactic rule naming them.
	BuiltinTreeRoot BuiltinKind = "treeRoot"
	// TreeNode is the widget family. It is the one tree whose members are
	// ordinary components -- a button composes away into its caller the way
	// any wrapper does -- so the passes asking "is this rendered rather than
	// composed" have to tell it from a specialised family.
	BuiltinTreeNode BuiltinKind = "treeNode"
	// TreeShape is the drawing tree. passCanvas emits that package's own
	// primitives, so it is the one specialised tree there are rules about.
	BuiltinTreeShape BuiltinKind = "treeShape"

	// Built-in visual nodes. Unlike the type marks above, these annotate a
	// component declaration: the checker dispatches a visual node to the
	// matching compiler construct (ir.Window, ir.ErrorBoundary) when
	// its target resolves to the marked component,
	// rather than matching a literal name.
	BuiltinWindow        BuiltinKind = "window"
	BuiltinContext       BuiltinKind = "context"
	BuiltinErrorBoundary BuiltinKind = "errorBoundary"
	// Effect brackets a lifetime: @mount when the node enters the tree,
	// @unmount when it leaves. `on` makes the bracket a keyed identity, so a
	// changed value ends one lifetime and begins the next. See sngl:app.
	BuiltinEffect BuiltinKind = "effect"

	BuiltinOutput BuiltinKind = "output"

	// Target identities. An opaque value type each of whose values is a const
	// the compiler synthesizes into one target's package -- html.platform,
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

// IsTreeRole reports whether the kind marks a tree the compiler itself has to
// name. Three do -- the package body's, the widget family, and the drawing
// tree -- because a phase asks after each by role rather than by declaration.
// Every other tree is compared by declaration and never spelled.
func (b BuiltinKind) IsTreeRole() bool {
	switch b {
	case BuiltinTreeRoot, BuiltinTreeNode, BuiltinTreeShape:
		return true
	}
	return false
}

// IsGeneric reports whether the kind is a generic type constructor
// (list/map/iter/ref/option/remote).
func (b BuiltinKind) IsGeneric() bool {
	switch b {
	case BuiltinList, BuiltinMap, BuiltinIter, BuiltinRef, BuiltinOption, BuiltinRemote:
		return true
	}
	return false
}

// IsNode reports whether the kind marks a built-in visual node. Node kinds are
// stamped on component declarations, not structs.
func (b BuiltinKind) IsNode() bool {
	switch b {
	case BuiltinWindow, BuiltinErrorBoundary, BuiltinContext, BuiltinEffect:
		return true
	}
	return false
}

func (b BuiltinKind) IsDirective() bool {
	return b == BuiltinOutput
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
		BuiltinList, BuiltinMap, BuiltinIter, BuiltinRef, BuiltinOption, BuiltinRemote,
		BuiltinTreeOne, BuiltinTreeRoot, BuiltinTreeNode, BuiltinTreeShape,
		BuiltinWindow, BuiltinErrorBoundary, BuiltinContext, BuiltinEffect,
		BuiltinOutput,
		BuiltinPlatform, BuiltinLanguage,
		BuiltinNull, BuiltinTargetPlatform, BuiltinTargetLanguage,
	}
}

// Valid reports whether the kind names a built-in (i.e. is not BuiltinNone and
// not an unrecognised string).
func (b BuiltinKind) Valid() bool {
	return b.IsPrimitive() || b.IsStringRepr() || b.IsUnit() || b.IsGeneric() ||
		b.IsSlotBound() || b.IsTreeRole() || b.IsNode() || b.IsDirective() || b.IsTargetID() || b.IsConst()
}
