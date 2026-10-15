package lower

import (
	"fmt"
	"slices"

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
		slot *ir.SlotDecl
		prop *ir.Prop
	}
	bySlot := map[*ir.Component]map[*ir.SlotDecl]*ir.Prop{}
	// An answer handed to an instantiation may itself ask: content forwarding
	// the caller's own slot renders nothing where that slot's content does
	// (ir.RendersNothing). So the caller is handed its own answer in turn,
	// round after round until no answer asks.
	for {
		fresh := map[*ir.Component][]asked{}
		for _, c := range pkg.Components {
			_ = ir.RewriteExprs(c, func(e ir.Expr) (ir.Expr, error) {
				se, ok := e.(*ir.SlotEmpty)
				if !ok || !slices.Contains(c.Slots, se.Slot) {
					return e, nil
				}
				props := bySlot[c]
				if props == nil {
					props = map[*ir.SlotDecl]*ir.Prop{}
					bySlot[c] = props
				}
				p := props[se.Slot]
				if p == nil {
					param := &ir.Param{Name: "__empty_" + se.Slot.Name, Type: ir.TypBool}
					p = &ir.Prop{Name: param.Name, Type: ir.TypBool, Default: &ir.Literal{Type: ir.TypBool, Value: "true"}, DefaultWritten: true, Sym: param}
					props[se.Slot] = p
					fresh[c] = append(fresh[c], asked{se.Slot, p})
					c.Props = append(c.Props, p)
				}
				return &ir.Ident{Name: p.Name, Type: ir.TypBool, Sym: p.Sym}, nil
			})
		}
		if len(fresh) == 0 {
			return refuseUnansweredSlotEmpty(pkg)
		}
		err := ir.Walk(pkg, func(n ir.Node) error {
			inst, ok := n.(*ir.NodeInst)
			if !ok {
				return nil
			}
			for _, a := range fresh[inst.Component] {
				decl := a.slot
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

// refuseUnansweredSlotEmpty reports a `.empty` no splice and no instantiation
// answered: one asking about a component entry that a population forwarded,
// where the component inserting the population is built at run time, so no
// splice of it hands the population what the entry was populated with.
func refuseUnansweredSlotEmpty(pkg *ir.Package) error {
	var err error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || err != nil {
			return nil
		}
		for _, a := range inst.Props {
			_ = ir.WalkExprs(a.Value, func(e ir.Expr) error {
				if se, ok := e.(*ir.SlotEmpty); ok && err == nil {
					err = fmt.Errorf("%s: what this supplies forwards %s, which is populated where a component built at run time inserts it; asking %s.%s of it is not supported yet", ir.NodePos(inst), se.Slot.Name, se.Slot.Name, ir.SlotEmptyMember)
				}
				return nil
			})
		}
		return nil
	})
	return err
}
