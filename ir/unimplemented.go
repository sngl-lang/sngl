package ir

import (
	"fmt"

	"duckfam.us/sngl/ast"
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
func UnimplementedNode(comp *Component, src ast.Stmt, tag, platform string) *UnimplementedComponent {
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
		e.DeclPkg = comp.PkgPath()
		e.Wildcard = comp.Wildcard != ""
	}
	return e
}

// DeclinedBy makes an unimplemented component a Declined.
func (e *UnimplementedComponent) DeclinedBy() string { return e.Platform }
