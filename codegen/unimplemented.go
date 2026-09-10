package codegen

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// UnimplementedComponent is returned by a platform generator for a library
// component the platform declares no implementation for. It is a typed error
// rather than a message so a caller running a matrix across platforms can tell
// "this target does not support this component" from "this target is broken",
// without matching on the text: the SNGL test command reports it as a skip for
// that platform, and a build of one target reports it as the error it is.
type UnimplementedComponent struct {
	Component string
	Platform  string
}

func (e *UnimplementedComponent) Error() string {
	return fmt.Sprintf("component %q has no %s implementation", e.Component, e.Platform)
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
		found = &UnimplementedComponent{Component: nodeName(n, kind), Platform: platform}
		return ir.SkipAll
	})
	return found
}
