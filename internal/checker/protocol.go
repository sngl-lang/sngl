package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

type protocolMark struct {
	fn  *ir.Func
	pos ast.Pos
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
		if op == ir.ProtocolConnect {
			c.bindConnect(pm)
			continue
		}
		served, verb := fn.Return, "returns"
		if op != ir.ProtocolCreate {
			served, verb = fn.Params[0].Type, "adds to"
		}
		comp, _ := served.Decl.(*ir.Component)
		switch {
		case comp == nil || comp.Intrinsic == "":
			name := "it"
			if comp != nil {
				name = comp.Name
			}
			c.error(pm.pos, "#[gen.protocol(%s)] declares the #[intrinsic] component it %s, and %s is not one", op, verb, name)
			continue
		case comp.Pkg != fn.Pkg:
			c.error(pm.pos, "#[gen.protocol(%s)]: %s is declared by %s, and only the package declaring a component says how its widget is built", op, comp.Name, comp.PkgPath())
			continue
		}
		if comp.Protocol == nil {
			comp.Protocol = &ir.NodeProtocol{}
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
		}
	}
}

// bindConnect gives a #[gen.protocol(connect)] function to the build-target
// node of its package: a platform connects a handler to every widget it
// makes the same way, so the operation is the target's, not a component's.
func (c *checker) bindConnect(pm protocolMark) {
	var node *ir.Component
	for _, comp := range c.declPkg().Components {
		if comp.Gen != nil && comp.Gen.TargetName != "" {
			node = comp
			break
		}
	}
	if node == nil {
		c.error(pm.pos, "#[gen.protocol(connect)] is a platform's, declared beside its build-target node, and this package declares none")
		return
	}
	if node.Protocol == nil {
		node.Protocol = &ir.NodeProtocol{}
	}
	if prev := node.Protocol.Connect; prev != nil {
		c.error(pm.pos, "#[gen.protocol(connect)]: %s already has %s", node.DisplayName(), prev.Name)
		return
	}
	node.Protocol.Connect = pm.fn
}
