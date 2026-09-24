package codegen

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// UnimplementedComponent is returned by a platform generator for a library
// component the platform declares no implementation for. It is a typed error
// rather than a message so a caller running a matrix across platforms can tell
// "this target does not support this component" from "this target is broken",
// without matching on the text: the SNGL test command reports it as a skip for
// that platform, and a build of one target reports it as the error it is.
//
// Component is the spelling at the call site (`html.ul`), and Decl the
// declaration that resolved to: a wildcard binds many spellings to one
// declaration, so neither says what the other does.
type UnimplementedComponent struct {
	Component string
	Platform  string
	// Decl is the declaration's name and package; empty where none resolved.
	Decl    string
	DeclPkg string
	// Wildcard is set when the spelling reached Decl through its #[wildcard].
	Wildcard bool
	Pos      ast.Pos
}

func (e *UnimplementedComponent) Error() string {
	msg := fmt.Sprintf("component %q has no %s implementation", e.Component, e.Platform)
	if e.Pos.IsValid() {
		msg = e.Pos.String() + ": " + msg
	}
	if e.Decl != "" {
		msg += " (declared as " + e.Decl
		if e.DeclPkg != "" {
			msg += " in " + e.DeclPkg
		}
		if e.Wildcard {
			msg += ", reached through its #[wildcard]"
		}
		msg += ")"
	}
	return msg
}

// UnimplementedNode is the error for a node this platform has nothing to
// render with. src is the node as written, which is what the diagnostic
// quotes; tag is the lowered name, the fallback where no source survived.
func UnimplementedNode(comp *ir.Component, src ast.Stmt, tag, platform string) *UnimplementedComponent {
	e := &UnimplementedComponent{Component: tag, Platform: platform}
	if vn, ok := src.(*ast.VisualNode); ok && vn != nil {
		if s := vn.TargetName(); s != "" {
			e.Component = s
		}
		e.Pos = vn.Pos
	} else if cs, ok := src.(*ast.CallStmt); ok && cs != nil {
		e.Pos = cs.Pos
	}
	if comp != nil {
		e.Decl = comp.Name
		e.DeclPkg = comp.Pkg
		e.Wildcard = comp.Wildcard != ""
	}
	return e
}

// DeclinesNode reports whether a node reaching platform's emitter under comp
// is one it has no implementation for, by the rule every generator holds a
// node to: a library declaration arriving under its own name was never lowered
// to one of the platform's primitives, since an override would have been
// inlined in its place. A user component is left to the caller; one reaching
// a leaf has an empty body and draws nothing.
func DeclinesNode(comp *ir.Component) bool {
	return comp == nil || comp.Stdlib
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
