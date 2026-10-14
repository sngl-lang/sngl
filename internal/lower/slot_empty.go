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
	byName := map[*ir.Component]map[string]*ir.Prop{}
	// An answer handed to an instantiation may itself ask: content forwarding
	// the caller's own slot renders nothing where that slot's content does
	// (ir.RendersNothing). So the caller is handed its own answer in turn,
	// round after round until no answer asks.
	for {
		fresh := map[*ir.Component][]asked{}
		for _, c := range pkg.Components {
			_ = ir.RewriteExprs(c, func(e ir.Expr) (ir.Expr, error) {
				se, ok := e.(*ir.SlotEmpty)
				if !ok {
					return e, nil
				}
				props := byName[c]
				if props == nil {
					props = map[string]*ir.Prop{}
					byName[c] = props
				}
				p := props[se.Slot.Name]
				if p == nil {
					param := &ir.Param{Name: "__empty_" + se.Slot.Name, Type: ir.TypBool}
					p = &ir.Prop{Name: param.Name, Type: ir.TypBool, Default: &ir.Literal{Type: ir.TypBool, Value: "true"}, DefaultWritten: true, Sym: param}
					props[se.Slot.Name] = p
					fresh[c] = append(fresh[c], asked{se.Slot.Name, p})
					c.Props = append(c.Props, p)
				}
				return &ir.Ident{Name: p.Name, Type: ir.TypBool, Sym: p.Sym}, nil
			})
		}
		if len(fresh) == 0 {
			return nil
		}
		err := ir.Walk(pkg, func(n ir.Node) error {
			inst, ok := n.(*ir.NodeInst)
			if !ok {
				return nil
			}
			for _, a := range fresh[inst.Component] {
				decl := slotDeclOf(inst.Component, a.slot)
				if decl == nil {
					continue
				}
				body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: decl.Name, Rest: decl.Rest}, inst)
				var answer ir.Expr = &ir.Literal{Type: ir.TypBool, Value: "true"}
				if supplied {
					if e, ok := ir.RendersNothing(deepCloneStmts(body)); ok {
						answer = e
					} else {
						answer = &ir.Literal{Type: ir.TypBool, Value: "false"}
					}
				}
				inst.Props = append(inst.Props, ir.Arg{Name: a.prop.Name, Value: answer})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
}
