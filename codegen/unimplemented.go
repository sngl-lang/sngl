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

// builtinNodeNames maps the IR construct a built-in visual node lowers to back
// to the name a program writes. Only the nodes a platform can decline are
// here: an ordinary component declines through the translator, which has the
// tag in hand, but these become their own IR shape at the checker and carry
// the name nowhere a generator can read it.
var builtinNodeNames = map[string]string{"errorBoundary": "errorBoundary"}

// FirstUnimplementedNode reports the first built-in visual node in pkg named
// in kinds, as the typed error a matrix run reports as a skip.
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
		name := ""
		if _, isBoundary := n.(*ir.ErrorBoundary); isBoundary {
			name = builtinNodeNames["errorBoundary"]
		}
		if name == "" || !want[name] {
			return nil
		}
		found = &UnimplementedComponent{Component: name, Platform: platform}
		return ir.SkipAll
	})
	return found
}
