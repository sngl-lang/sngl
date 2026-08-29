package checker

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A wildcard is matched where every other name is: a scope checks its
// wildcards after its map of identifiers (ir.Scope.WildcardMatches), so
// nothing here knows about packages, platforms, or roots. What remains is the
// prop side, where the "scope" is a component's prop list.

// compileWildcard reports whether a pattern is usable, at the position it was
// written, rather than leaving a bad one to match nothing at every call site.
func compileWildcard(pattern string) error {
	_, err := ir.CompileWildcard(pattern)
	return err
}

// wildcardProp returns the prop of comp whose wildcard pattern covers name.
// A declared prop of that name beats every wildcard, so the caller looks one
// up first; two wildcards covering one name is ambiguous and reported by the
// caller, which knows the use site.
func wildcardProp(comp *ir.Component, name string) (*ir.Prop, bool, error) {
	if comp == nil {
		return nil, false, nil
	}
	var found *ir.Prop
	for _, p := range comp.Props {
		if !ir.MatchesWildcard(p.Wildcard, name) {
			continue
		}
		if found != nil {
			return nil, false, fmt.Errorf("prop %q matches both wildcard props %q and %q on component %s", name, found.Name, p.Name, comp.Name)
		}
		found = p
	}
	return found, found != nil, nil
}

// applyEventMarks runs the marks written on a component's event declaration.
// An event sits in the prop list, so the marks legal there are the same ones,
// and applyMark is told so.
func (c *checker) applyEventMarks(e ast.EventDecl, evt *ir.EventDecl) {
	for _, attr := range e.MacroAttrs() {
		c.applyMark(attr, e, evt, true)
	}
}

func (c *checker) applySlotMarks(d ast.SlotDecl, slot *ir.SlotDecl) {
	for _, attr := range d.MacroAttrs() {
		c.applyMark(attr, d, slot, true)
	}
}

// wildcardEvent returns the event of comp whose pattern covers name. A
// declared event of that name beats every wildcard, so the caller looks one up
// first; two wildcards covering one name is ambiguous and reported by the
// caller, which knows the use site.
func wildcardEvent(comp *ir.Component, name string) (*ir.EventDecl, bool, error) {
	if comp == nil {
		return nil, false, nil
	}
	var found *ir.EventDecl
	for _, e := range comp.Events {
		if !ir.MatchesWildcard(e.Wildcard, name) {
			continue
		}
		if found != nil {
			return nil, false, fmt.Errorf("event %q matches both wildcard events %q and %q on component %s", name, found.Name, e.Name, comp.Name)
		}
		found = e
	}
	return found, found != nil, nil
}
