package html

// Everything html needs to know about a raw element comes from the `element`
// declaration in lib/platforms/html — which props exist, which are boolean,
// which prop the matched tag name binds to, and which events carry a payload.
// This file is where those questions are answered; its callers ask them rather
// than keeping a list of names beside the declaration to drift away from it.

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// rawElementDecl returns the wildcard component every HTML tag resolves to —
// `element` in lib/platforms/html. It is found by its mark rather than by its
// name: a wildcard component with a prop to bind the matched name into is
// exactly the declaration that answers to a tag.
//
// The package's own components come first, then its imports', so a program
// shadowing the platform's element with one of its own is read from its own.
// The IR walk is the fallback for a compilation that never names the platform
// package in an import — every stdlib wrapper's body inlines raw elements, and
// those NodeInsts carry the declaration they resolved to.
func rawElementDecl(pkg *ir.Package) *ir.Component {
	if pkg == nil {
		return nil
	}
	if c := wildcardComponent(pkg.Components); c != nil {
		return c
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Pkg == nil {
			continue
		}
		if c := wildcardComponent(imp.Pkg.Components); c != nil {
			return c
		}
	}
	var found *ir.Component
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if found != nil {
			return nil
		}
		if n, ok := s.(*ir.NodeInst); ok && n.Component != nil {
			if n.Component.Wildcard != "" && n.Component.WildcardInto != "" {
				found = n.Component
			}
		}
		return nil
	})
	return found
}

func wildcardComponent(comps []*ir.Component) *ir.Component {
	for _, c := range comps {
		if c != nil && c.Wildcard != "" && c.WildcardInto != "" {
			return c
		}
	}
	return nil
}

// elementDecl is the declaration a node's props and events are read from: the
// node's own component when it has one, else the package's raw-element
// declaration.
func (g *htmlGen) elementDecl(n *ir.NodeInst) *ir.Component {
	if n != nil && n.Component != nil && n.Component.Wildcard != "" {
		return n.Component
	}
	return g.rawElement()
}

// rawElement returns the package's raw-element declaration, resolved once.
func (g *htmlGen) rawElement() *ir.Component {
	if !g.rawElemDone {
		g.rawElemDone = true
		g.rawElem = rawElementDecl(g.pkg)
	}
	return g.rawElem
}

// tagPropName is the prop the matched tag name binds to — the `into` argument
// of the element's #[wildcard] mark. A node's `tag=` prop names the element
// rather than describing it, so it is never emitted as an attribute.
func tagPropName(comp *ir.Component) string {
	if comp == nil {
		return ""
	}
	return comp.WildcardInto
}

// elementProp returns the element's declaration of prop, or nil when the prop
// is one the wildcard accepted (an attribute nobody declared).
func elementProp(comp *ir.Component, prop string) *ir.Prop {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p != nil && p.Wildcard == "" && p.Name == prop {
			return p
		}
	}
	return nil
}

// domOnlyProps are the props no attribute spells: routing them through
// setAttribute would invent an attribute name, or write one the element does
// not read back. They are DOM property writes whatever their declared type.
var domOnlyProps = map[string]bool{
	"textContent": true,
	"innerText":   true,
	"innerHTML":   true,
	"className":   true,
	// A form control's committed value diverges from its `value` attribute
	// once the user has typed, so an update must reach the property.
	"value": true,
}

// domPropNames maps an HTML attribute name to the DOM property spelling that
// differs from it. HTML attribute names are lowercase and DOM property names
// are camelCase, and the two only diverge where a name is more than one word.
var domPropNames = map[string]string{
	"readonly": "readOnly",
}

// domPropForProp reports the DOM property a raw-element prop is written
// through, and false when the prop must go through setAttribute instead.
//
// A prop the element declares `bool` is a boolean attribute, and setAttribute
// cannot express its absence — `setAttribute("open", false)` renders
// `open="false"`, which leaves a <details> open — so it is always a property
// write. Everything else is an attribute unless no attribute spells it.
func domPropForProp(comp *ir.Component, prop string) (string, bool) {
	if domOnlyProps[prop] {
		return domName(prop), true
	}
	if p := elementProp(comp, prop); p != nil && p.Type != nil && p.Type.Kind == ir.TypeBool {
		return domName(prop), true
	}
	return "", false
}

func domName(prop string) string {
	if n, ok := domPropNames[prop]; ok {
		return n
	}
	return prop
}

// domEventName reports the DOM event name a SNGL event on a raw element maps
// to, or "" when the element declares no such event. The element declares its
// payload-bearing events by name and the rest through a wildcard event, so
// every DOM event answers here and the name is its own — an event outside the
// declaration is one the element does not have.
func domEventName(comp *ir.Component, event string) string {
	if comp == nil || event == "" {
		// No declaration to consult: a raw element's event name is the DOM
		// event name, which is the answer the declaration would give.
		return event
	}
	for _, e := range comp.Events {
		if e == nil {
			continue
		}
		if e.Name == event {
			return event
		}
		if e.Wildcard != "" && ir.MatchesWildcard(e.Wildcard, event) {
			return event
		}
	}
	return ""
}

// eventPayloadBase reports the JS expression a handler's event parameter binds
// to for one event: the DOM event object, or the element the event fired on.
//
// The payload declaration decides. A payload field that names a prop the
// element also declares is that element's state read back — `InputEvent.value`
// is the control's `value` — so the parameter stands for the target. A field
// the element declares nothing of is the event's own data —
// `ErrorEvent.message` — so it stands for the event. A payload with no fields
// is read by nobody and takes the event.
//
// Binding every event to the target is what turned `e.message` into
// `e.target.message`; binding every event to the event object would turn
// `evt.value` into `e.value`, which is undefined on every DOM event.
func eventPayloadBase(comp *ir.Component, event string) string {
	if comp == nil {
		// No declaration to read: the target is what a handler reached for
		// before there was one, so an unresolved element keeps reaching it.
		return "e.target"
	}
	for _, f := range eventPayloadFields(comp, event) {
		if elementProp(comp, f) != nil {
			return "e.target"
		}
	}
	return "e"
}

// eventPayloadFields is the field names of an event's declared payload struct,
// or nil for a void event or one the element does not declare by name (its
// wildcard event carries no payload).
func eventPayloadFields(comp *ir.Component, event string) []string {
	if comp == nil {
		return nil
	}
	for _, e := range comp.Events {
		if e == nil || e.Name != event || e.Type == nil {
			continue
		}
		sd, ok := e.Type.Decl.(*ir.StructDef)
		if !ok || sd == nil {
			return nil
		}
		out := make([]string, 0, len(sd.Fields))
		for _, f := range sd.Fields {
			if f != nil {
				out = append(out, f.Name)
			}
		}
		return out
	}
	return nil
}
