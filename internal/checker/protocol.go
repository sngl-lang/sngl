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
		comp, _ := fn.Return.Decl.(*ir.Component)
		switch {
		case comp == nil || comp.Intrinsic == "":
			name := "it"
			if comp != nil {
				name = comp.Name
			}
			c.error(pm.pos, "#[gen.protocol(create)] declares the #[intrinsic] component it returns, and %s is not one", name)
			continue
		case comp.Pkg != fn.Pkg:
			c.error(pm.pos, "#[gen.protocol(create)]: %s is declared by %s, and only the package declaring a component says how its widget is made", comp.Name, comp.PkgPath())
			continue
		}
		if comp.Protocol == nil {
			comp.Protocol = &ir.NodeProtocol{}
		}
		if prev := comp.Protocol.Create; prev != nil {
			c.error(pm.pos, "#[gen.protocol(create)]: %s is already made by %s", comp.Name, prev.Name)
			continue
		}
		comp.Protocol.Create = fn
	}
}
