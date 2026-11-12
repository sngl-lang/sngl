package checker

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

type protocolMark struct {
	fn  *ir.Func
	pos ast.Pos
	// event is what a #[gen.fires] fires.
	event string
}

// mark is the mark as written, for an error to name.
func (pm protocolMark) mark() string {
	if pm.fn.Protocol == ir.ProtocolFire {
		return fmt.Sprintf("#[gen.fires(%q)]", pm.event)
	}
	return fmt.Sprintf("#[gen.protocol(%s)]", pm.fn.Protocol)
}

// bindProtocols gives each #[gen.protocol] function to the component it
// serves. Run after every mark of the package, so the component's own
// #[intrinsic] is known whichever was declared first.
func (c *checker) bindProtocols() {
	marks := c.protocols
	c.protocols = nil
	for _, pm := range marks {
		fn := pm.fn
		op := fn.Protocol
		switch op {
		case ir.ProtocolConnect, ir.ProtocolHandle, ir.ProtocolAttach, ir.ProtocolDetach, ir.ProtocolContents, ir.ProtocolAnchor:
			c.bindPlatformOp(pm)
			continue
		}
		served, verb := fn.Return, "returns"
		switch op {
		case ir.ProtocolSet:
			served, verb = fn.Params[0].Type, "sets"
		case ir.ProtocolOn:
			served, verb = fn.Params[0].Type, "connects to"
		case ir.ProtocolFire:
			served, verb = fn.Params[0].Type, "fires on"
		case ir.ProtocolAppend, ir.ProtocolRemove, ir.ProtocolInsertBefore:
			served, verb = fn.Params[0].Type, "adds to"
		case ir.ProtocolRedraw:
			served, verb = fn.Params[0].Type, "redraws"
		}
		comp, _ := served.Decl.(*ir.Component)
		switch {
		case comp == nil || comp.Intrinsic == "":
			name := "it"
			if comp != nil {
				name = comp.Name
			}
			c.error(pm.pos, "%s declares the #[intrinsic] component it %s, and %s is not one", pm.mark(), verb, name)
			continue
		case comp.Pkg != fn.Pkg:
			c.error(pm.pos, "%s: %s is declared by %s, and only the package declaring a component says how its widget is built", pm.mark(), comp.Name, comp.PkgPath())
			continue
		}
		if comp.Protocol == nil {
			comp.Protocol = &ir.NodeProtocol{}
		}
		switch op {
		case ir.ProtocolSet:
			c.bindSet(pm, comp)
			continue
		case ir.ProtocolOn:
			c.bindOn(pm, comp)
			continue
		case ir.ProtocolFire:
			c.bindFire(pm, comp)
			continue
		}
		if prev := comp.Protocol.Op(op); prev != nil {
			c.error(pm.pos, "#[gen.protocol(%s)]: %s already has %s", op, comp.Name, prev.Name)
			continue
		}
		switch op {
		case ir.ProtocolCreate:
			comp.Protocol.Create = fn
		case ir.ProtocolAppend:
			comp.Protocol.Append = fn
		case ir.ProtocolRemove:
			comp.Protocol.Remove = fn
		case ir.ProtocolInsertBefore:
			comp.Protocol.InsertBefore = fn
		case ir.ProtocolRedraw:
			if !ir.IsShapeFamily(ir.RestSlotFamily(comp)) {
				c.error(pm.pos, "#[gen.protocol(redraw)] asks a canvas to paint again, and %s takes no shapes", comp.Name)
				continue
			}
			comp.Protocol.Redraw = fn
		}
	}
}

// bindPlatformOp gives a #[gen.protocol(connect)], (handle), (attach) or
// (detach) function to the build-target node of its package: a platform
// connects a handler to every widget it makes the same way, holds each node
// as one type and shows each window alike, so the operation is the
// target's, not a component's.
func (c *checker) bindPlatformOp(pm protocolMark) {
	op := pm.fn.Protocol
	var node *ir.Component
	for _, comp := range c.declPkg().Components {
		if comp.Gen != nil && comp.Gen.TargetName != "" {
			node = comp
			break
		}
	}
	if node == nil {
		c.error(pm.pos, "#[gen.protocol(%s)] is a platform's, declared beside its build-target node, and this package declares none", op)
		return
	}
	if node.Protocol == nil {
		node.Protocol = &ir.NodeProtocol{}
	}
	if prev := node.Protocol.Op(op); prev != nil {
		c.error(pm.pos, "#[gen.protocol(%s)]: %s already has %s", op, node.DisplayName(), prev.Name)
		return
	}
	switch op {
	case ir.ProtocolConnect:
		node.Protocol.Connect = pm.fn
	case ir.ProtocolHandle:
		node.Protocol.Handle = pm.fn
	case ir.ProtocolAttach:
		node.Protocol.Attach = pm.fn
	case ir.ProtocolDetach:
		node.Protocol.Detach = pm.fn
	case ir.ProtocolContents:
		node.Protocol.Contents = pm.fn
	case ir.ProtocolAnchor:
		node.Protocol.Anchor = pm.fn
	}
}

// bindSet gives a #[gen.protocol(set)] function to the prop of comp its value
// parameter is named as.
func (c *checker) bindSet(pm protocolMark, comp *ir.Component) {
	prop := pm.fn.Params[1].Name
	if !slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == prop }) {
		c.error(pm.pos, "#[gen.protocol(set)] sets the prop its value parameter is named as, and %s has no prop %s", comp.Name, prop)
		return
	}
	if prev := comp.Protocol.Setter(prop); prev != nil {
		c.error(pm.pos, "#[gen.protocol(set)]: %s already sets %s with %s", comp.Name, prop, prev.Name)
		return
	}
	if comp.Protocol.Set == nil {
		comp.Protocol.Set = map[string]*ir.Func{}
	}
	comp.Protocol.Set[prop] = pm.fn
}

// bindOn gives a #[gen.protocol(on)] function to the event of comp its handler
// parameter is named as, or to a two-way prop's report, which reaches the
// node as an event of the prop's name.
func (c *checker) bindOn(pm protocolMark, comp *ir.Component) {
	event := pm.fn.Params[1].Name
	isEvent := slices.ContainsFunc(comp.Events, func(e *ir.EventDecl) bool { return e.Name == event })
	isReport := slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == event && p.Bidirectional })
	if !isEvent && !isReport {
		c.error(pm.pos, "#[gen.protocol(on)] connects the event its handler parameter is named as, and %s has no event @%s nor two-way prop :%s", comp.Name, event, event)
		return
	}
	if prev := comp.Protocol.Handler(event); prev != nil {
		c.error(pm.pos, "#[gen.protocol(on)]: %s already connects @%s with %s", comp.Name, event, prev.Name)
		return
	}
	if comp.Protocol.On == nil {
		comp.Protocol.On = map[string]*ir.Func{}
	}
	comp.Protocol.On[event] = pm.fn
}

// bindFire gives a #[gen.fires(event)] function to the event of comp it
// names, or to a two-way prop's report, as bindOn does.
func (c *checker) bindFire(pm protocolMark, comp *ir.Component) {
	event := pm.event
	isEvent := slices.ContainsFunc(comp.Events, func(e *ir.EventDecl) bool { return e.Name == event })
	isReport := slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == event && p.Bidirectional })
	if !isEvent && !isReport {
		c.error(pm.pos, "#[gen.fires(%q)] fires an event of %s, which has no event @%s nor two-way prop :%s", event, comp.Name, event, event)
		return
	}
	if prev := comp.Protocol.Firer(event); prev != nil {
		c.error(pm.pos, "#[gen.fires(%q)]: %s already fires @%s with %s", event, comp.Name, event, prev.Name)
		return
	}
	if comp.Protocol.Fire == nil {
		comp.Protocol.Fire = map[string]*ir.Func{}
	}
	comp.Protocol.Fire[event] = pm.fn
}
