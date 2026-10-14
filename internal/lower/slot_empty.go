package lower

import (
	"duckfam.us/sngl/ir"
)

// passSlotEmptyProps hands a component built at run time the answer to each
// `.empty` its body asks of a slot (ir.SlotEmpty), as a bool prop its call
// site sets. A spliced body has the answer written in where it is spliced
// (ir.SlotSplicer); one emitted from its declaration alone -- a recursion, a
// component under a loop with state of its own, on a target that does not
// copy it per content -- has no call site to read it from, so each
// instantiation passes it, and the answer stays as reactive as the content it
// was read from.
//
// Always on: a SlotEmpty left standing has no spelling on any target.
var passSlotEmptyProps = pass{
	name:    "SlotEmptyProps",
	enabled: func(Features) bool { return true },
	apply:   lowerSlotEmptyProps,
}

func lowerSlotEmptyProps(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	type asked struct {
		slot string
		prop *ir.Prop
	}
	props := map[*ir.Component][]asked{}
	for _, c := range pkg.Components {
		byName := map[string]*ir.Prop{}
		var order []asked
		_ = ir.RewriteExprs(c, func(e ir.Expr) (ir.Expr, error) {
			se, ok := e.(*ir.SlotEmpty)
			if !ok {
				return e, nil
			}
			p := byName[se.Slot.Name]
			if p == nil {
				param := &ir.Param{Name: "__empty_" + se.Slot.Name, Type: ir.TypBool}
				p = &ir.Prop{Name: param.Name, Type: ir.TypBool, Default: &ir.Literal{Type: ir.TypBool, Value: "true"}, DefaultWritten: true, Sym: param}
				byName[se.Slot.Name] = p
				order = append(order, asked{se.Slot.Name, p})
				c.Props = append(c.Props, p)
			}
			return &ir.Ident{Name: p.Name, Type: ir.TypBool, Sym: p.Sym}, nil
		})
		if len(order) > 0 {
			props[c] = order
		}
	}
	if len(props) == 0 {
		return nil
	}
	return ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok {
			return nil
		}
		for _, a := range props[inst.Component] {
			p := a.prop
			decl := slotDeclOf(inst.Component, a.slot)
			if decl == nil {
				continue
			}
			body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: decl.Name, Rest: decl.Rest}, inst)
			var answer ir.Expr = &ir.Literal{Type: ir.TypBool, Value: "true"}
			if supplied {
				if a, ok := ir.RendersNothing(deepCloneStmts(body)); ok {
					answer = a
				} else {
					answer = &ir.Literal{Type: ir.TypBool, Value: "false"}
				}
			}
			inst.Props = append(inst.Props, ir.Arg{Name: p.Name, Value: answer})
		}
		return nil
	})
}
