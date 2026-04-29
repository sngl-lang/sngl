package js

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/sngl-lang/typescript-go/snglts"
)

// tsTypeToIR maps a TS type-expression node to an SNGL IR type.
//
// Returns (TypDyn, "<reason>") for shapes SNGL cannot model precisely; the
// caller marks the surrounding declaration Unusable. Returns nil with no
// reason when the input node is itself nil (declaration without an annotation
// — caller decides whether that means void or any).
func (w *walker) tsTypeToIR(n *snglts.Node) (*ir.Type, string) {
	if n == nil {
		return ir.TypDyn, ""
	}
	switch n.Kind {
	case snglts.KindStringKeyword:
		return ir.TypString, ""
	case snglts.KindNumberKeyword:
		return ir.TypFloat, ""
	case snglts.KindBigIntKeyword:
		return ir.TypInt, ""
	case snglts.KindBooleanKeyword:
		return ir.TypBool, ""
	case snglts.KindVoidKeyword:
		return ir.TypVoid, ""
	case snglts.KindNullKeyword, snglts.KindUndefinedKeyword:
		return ir.TypNull, ""
	case snglts.KindAnyKeyword, snglts.KindUnknownKeyword:
		return ir.TypDyn, ""
	case snglts.KindNeverKeyword:
		return ir.TypVoid, ""
	case snglts.KindObjectKeyword:
		return ir.TypDyn, "object keyword type not supported"

	case snglts.KindLiteralType:
		return literalTypeToIR(n)

	case snglts.KindParenthesizedType:
		return w.tsTypeToIR(n.Type())

	case snglts.KindArrayType:
		elem, unusable := w.tsTypeToIR(n.Type())
		if unusable != "" {
			return ir.TypDyn, unusable
		}
		return ir.ListOf(elem), ""

	case snglts.KindUnionType:
		return w.unionToIR(n)

	case snglts.KindIntersectionType:
		return ir.TypDyn, "intersection types not supported"

	case snglts.KindTupleType:
		return ir.TypDyn, "tuple types not supported"

	case snglts.KindFunctionType:
		return ir.TypDyn, "function-typed values not supported"

	case snglts.KindTypeLiteral:
		// Inline object type — could synth anonymous struct, but v1 keeps
		// it dyn until we have a clean way to anchor it for LSP.
		return ir.TypDyn, "inline object types not supported"

	case snglts.KindTypeReference:
		return w.typeRefToIR(n)
	}
	return ir.TypDyn, "unsupported TS type kind"
}

// unionToIR maps `T | null`, `T | undefined`, `T | null | undefined` to
// OptionOf(T). Anything else is dyn-with-unusable.
func (w *walker) unionToIR(n *snglts.Node) (*ir.Type, string) {
	var nonNullables []*snglts.Node
	hasNullable := false
	for _, t := range childTypes(n) {
		if isNullableTypeNode(t) {
			hasNullable = true
			continue
		}
		nonNullables = append(nonNullables, t)
	}
	if !hasNullable || len(nonNullables) != 1 {
		return ir.TypDyn, "union types not supported"
	}
	inner, unusable := w.tsTypeToIR(nonNullables[0])
	if unusable != "" {
		return ir.TypDyn, unusable
	}
	return ir.OptionOf(inner), ""
}

// typeRefToIR resolves a TypeReference (named type with optional type args)
// to an SNGL IR type. Recognises Promise<T>, Array<T>, Date; defers to
// pre-registered struct/enum names; otherwise dyn.
func (w *walker) typeRefToIR(n *snglts.Node) (*ir.Type, string) {
	name := typeRefName(n)
	args := n.TypeArguments()
	switch name {
	case "Array", "ReadonlyArray":
		if len(args) == 1 {
			elem, unusable := w.tsTypeToIR(args[0])
			if unusable != "" {
				return ir.TypDyn, unusable
			}
			return ir.ListOf(elem), ""
		}
	case "Promise":
		// Plain Promise<T> in non-return position: leave as dyn since
		// SNGL has no top-level promise type. The function-decl path
		// handles return-position promises separately.
		return ir.TypDyn, "Promise type only supported as a function return"
	case "Date":
		return ir.TypDateTime, ""
	case "Record", "Map":
		return ir.TypDyn, "map/record types not supported"
	}
	if sd, ok := w.structs[name]; ok {
		return sd.SymType(), ""
	}
	if ed, ok := w.enums[name]; ok {
		return ed.SymType(), ""
	}
	return ir.TypDyn, "unknown type reference: " + name
}

// literalTypeToIR maps a TS LiteralType (e.g. `"foo"`, `42`, `null`) to
// its widened SNGL type.
func literalTypeToIR(n *snglts.Node) (*ir.Type, string) {
	inner := firstChild(n)
	if inner == nil {
		return ir.TypDyn, "literal type"
	}
	switch inner.Kind {
	case snglts.KindNullKeyword:
		return ir.TypNull, ""
	}
	return ir.TypDyn, "literal type"
}

// isNullableTypeNode reports whether a type node represents `null` or
// `undefined`, either bare or wrapped in a LiteralType.
func isNullableTypeNode(n *snglts.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == snglts.KindNullKeyword || n.Kind == snglts.KindUndefinedKeyword {
		return true
	}
	if n.Kind == snglts.KindLiteralType {
		inner := firstChild(n)
		if inner != nil && (inner.Kind == snglts.KindNullKeyword || inner.Kind == snglts.KindUndefinedKeyword) {
			return true
		}
	}
	return false
}

// firstChild returns the first child node visited by ForEachChild, or nil.
func firstChild(n *snglts.Node) *snglts.Node {
	var first *snglts.Node
	n.ForEachChild(func(c *snglts.Node) bool {
		first = c
		return true
	})
	return first
}

// typeRefName returns the textual name of a TypeReference's TypeName.
// Uses ForEachChild because n.Name() returns nil for type-reference nodes
// (the name field is TypeName, not Name).
func typeRefName(n *snglts.Node) string {
	if n == nil {
		return ""
	}
	tn := firstChild(n)
	if tn == nil {
		return ""
	}
	if tn.Kind == snglts.KindIdentifier {
		return tn.Text()
	}
	return ""
}

// isPromiseTypeNode reports whether a TS type node is `Promise<T>`.
func isPromiseTypeNode(n *snglts.Node) bool {
	if n == nil || n.Kind != snglts.KindTypeReference {
		return false
	}
	return typeRefName(n) == "Promise"
}

// promiseInnerNode returns the T from `Promise<T>`, or nil if not a Promise.
func promiseInnerNode(n *snglts.Node) *snglts.Node {
	if !isPromiseTypeNode(n) {
		return nil
	}
	args := n.TypeArguments()
	if len(args) == 0 {
		return nil
	}
	return args[0]
}
