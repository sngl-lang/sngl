package codegen

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// UnimplementedComponent is ir.UnimplementedComponent: the error for a
// library component a platform declares no implementation for.
type UnimplementedComponent = ir.UnimplementedComponent

// UnimplementedNode is ir.UnimplementedNode.
func UnimplementedNode(comp *ir.Component, src ast.Stmt, tag, platform string) *UnimplementedComponent {
	return ir.UnimplementedNode(comp, src, tag, platform)
}

// DeclinesNode reports whether a node reaching platform's emitter under comp
// is one it has no implementation for, by the rule every generator holds a
// node to: a bodyless declaration arriving under its own name was never
// lowered to one of the platform's primitives, since an override would have
// been inlined in its place, and a primitive or wildcard element arriving at
// the generic path is another platform's -- a generator answers its own
// before it asks. A component with a body is left to the caller; one reaching
// a leaf has an empty body and draws nothing.
func DeclinesNode(comp *ir.Component) bool {
	return comp == nil || comp.Bodyless || comp.Primitive()
}

// nodeKind is the #[builtin] kind a built-in visual node lowers from, for the
// nodes a platform can decline. An ordinary component declines through the
// translator, which has the tag in hand; these become their own IR shape at
// the checker, so the kind is what a generator has left to name them by.
//
// The kind and not a name: the declaration is `boundary` in sngl:builtin
// today and a program may shadow it, so a table of spellings here would be
// wrong twice over. What the diagnostic reports is the spelling at the call
// site, which nodeName reads back off the AST the checker kept.
func nodeKind(n ir.Node) string {
	if _, ok := n.(*ir.ErrorBoundary); ok {
		return string(ir.BuiltinErrorBoundary)
	}
	return ""
}

// nodeName is how the program wrote the node, for the diagnostic to quote.
// Falls back to the kind where the AST did not survive -- a synthesized
// boundary has no source spelling, and the kind is still true of it.
func nodeName(n ir.Node, kind string) string {
	if b, ok := n.(*ir.ErrorBoundary); ok {
		if s := b.AST.TargetName(); s != "" {
			return s
		}
	}
	return kind
}

// FirstUnimplementedNode reports the first built-in visual node in pkg whose
// kind is named in kinds, as the typed error a matrix run reports as a skip.
//
// A platform that emits nothing for a node kind does not fail where it decides
// not to: the statement travels on to the language renderer, which walks the
// same IR from a different angle and panics on a shape no platform was meant
// to hand it. What reaches the user is a Go stack trace naming irwalk, which
// says nothing about the program that produced it. Declining up front, by
// name, is the same answer the translator already gives for a component.
func FirstUnimplementedNode(pkg *ir.Package, platform string, kinds ...string) error {
	if pkg == nil || len(kinds) == 0 {
		return nil
	}
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	var found error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		kind := nodeKind(n)
		if kind == "" || !want[kind] {
			return nil
		}
		e := &UnimplementedComponent{Component: nodeName(n, kind), Platform: platform}
		if b, ok := n.(*ir.ErrorBoundary); ok && b.AST != nil {
			e.Pos = b.AST.Pos
		}
		found = e
		return ir.SkipAll
	})
	return found
}
