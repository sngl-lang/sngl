package html

// Everything html needs to know about a raw element comes from the `element`
// declaration in codegen/platform/html — which props exist, which are boolean,
// which prop the matched tag name binds to, and which events carry a payload.
// This file is where those questions are answered; its callers ask them rather
// than keeping a list of names beside the declaration to drift away from it.

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// elementDecl is the declaration a node's props and events are read from: the
// node's own component when it has one, else the package's raw-element
// declaration.
func (g *htmlGen) elementDecl(n *ir.NodeInst) *ir.Component {
	if n == nil {
		return nil
	}
	return n.Component
}

// tagProp is the prop the matched tag name binds to. It is html's own prop on
// html's own `element`, declared in html.sngl beside this file, so it is named
// here rather than read back off the declaration at run time.
// TestElementDeclaresTagProp keeps the two in step.
//
// A node's `tag=` names the element rather than describing it, so it is never
// emitted as an attribute.
const tagProp = "tag"

// attrsProp is the prop html's `element` collects every attribute it does not
// declare into, keyed by the name it was written under. Named here for the
// reason tagProp is: it is html's own prop, and TestElementDeclaresTagProp
// holds it to the declaration.
//
// The collecting is the checker's — a name matching the prop's #[wildcard]
// pattern lands in the map, and binding both forms at one call site is an
// error there. What reaches codegen is the map, and codegen.WildcardProps
// unpacks it back into the names it was written under.
const attrsProp = "attrs"

// isElement reports whether a component is html's `element`. It is recognised
// by the two props html gave it, for the reason their names are constants
// here: they are html's own, and nothing else html renders declares them.
// TestElementDeclaresTagProp holds this to the declaration.
func isElement(c *ir.Component) bool {
	if c == nil {
		return false
	}
	var tag, attrs bool
	for _, p := range c.Props {
		switch {
		case p == nil:
		case p.Name == tagProp:
			tag = true
		case p.Name == attrsProp:
			attrs = true
		}
	}
	return tag && attrs
}

// isDOMEventName reports whether a name is one the DOM fires. `element`
// declares its payload-bearing events by name and accepts every other DOM
// event, and DOM event names are lowercase — so a camelCase name is a SNGL
// spelling that the override wrapping the element should have mapped, and
// attaching a listener for it would listen for an event nothing fires.
func isDOMEventName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// elementProp returns the element's declaration of prop, or nil when the prop
// is one the wildcard accepted (an attribute nobody declared).
func elementProp(comp *ir.Component, prop string) *ir.Prop {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p != nil && p.Name == prop && p.Name != attrsProp {
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
		if e != nil && e.Name == event {
			return event
		}
	}
	if isDOMEventName(event) {
		return event
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

// wildcardPropNamed reports whether prop is the declaration's wildcard prop --
// the map every name it matched was collected into. Such a prop names no
// attribute of its own, so a caller writing one has to unpack it first.
// voidElements are the tags that hold no content: the HTML parser closes them
// itself, and a close tag for one -- `</input>` -- is invalid markup. The
// static and route renders read this same list; the route render had none, and
// emitted a close tag for every element.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"source": true, "track": true, "wbr": true,
}

// contentProp reports how a prop's value is written when the element is
// rendered as markup rather than patched through the DOM: as escaped text
// content, as raw markup, or not as content at all.
//
// Only the DOM-side content properties are content. A prop the client render
// writes through a DOM property (domPropForProp) has no attribute that spells
// it, but in markup the attribute is the initial value the DOM reads -- an
// <input>'s `value` is the attribute, and writing it as a text node both
// invents a child and loses the value.
type contentKind int

const (
	notContent contentKind = iota
	textContentKind
	rawContentKind
)

func contentProp(prop string) contentKind {
	switch prop {
	case "textContent", "innerText":
		return textContentKind
	case "innerHTML":
		return rawContentKind
	}
	return notContent
}
