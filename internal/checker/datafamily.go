package checker

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A component that names itself as its family -- `component choice(label
// string) choice` -- is data (Component.Data). Written in a slot of its type it
// is a record of its props, never rendered; the slot is never inserted, and its
// name in the container's body is the list of records the population renders
// (SlotDecl.Values). lower.DataFamilies builds that list at each call site once
// the components composing records are spliced, ahead of the first optimize;
// the checker holds the population to the type as it holds any slot's, and
// types the component's name in a value position as its record (dataRecord).

// bindDataFamily gives a data component its record and a component its data
// slots' props, as each registers. A slot naming a data component in this
// package finds its record, since what a slot names registers first
// (familiesFirst), and a data component's own slot finds the record made here.
// A data component's data slots are fields of its record, so a record may be a
// tree of them.
//
// A data component may also declare a rest slot of content, which is no field:
// what each record was written with is rendered by the container, and a
// container of such records is given a slot to insert it through
// (contentSlot), whose population lower.DataFamilies writes at each call site.
func (c *checker) bindDataFamily(comp *ir.Component) {
	if comp.Data {
		comp.Record = &ir.StructDef{Name: comp.Name + "__record", Pkg: c.declPkg(), RecordOf: comp}
	}
	var shows []*ir.SlotDecl
	for _, slot := range comp.Slots {
		f := ir.TypeFamily(slot.Content)
		if f == nil || !f.Data || f.Record == nil {
			continue
		}
		if comp.AST != nil && slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == slot.Name }) {
			c.error(comp.AST.Pos, "component %s: slot %q is read as a list of %s by its name, which a prop already declares", comp.Name, slot.Name, f.Name)
			continue
		}
		list := ir.ListOf(&ir.Type{Kind: ir.TypeStruct, Decl: f.Record})
		slot.Values = &ir.Prop{
			Name:           slot.Name,
			Type:           list,
			Default:        &ir.ListLit{Type: list},
			DefaultWritten: true,
			Doc:            slot.Doc,
		}
		slot.Values.Sym = &ir.Param{Name: slot.Name, Type: list}
		comp.Props = append(comp.Props, slot.Values)
		if cs := contentSlot(f); cs != nil && !slices.ContainsFunc(shows, func(s *ir.SlotDecl) bool { return s.Shows == cs }) {
			shows = append(shows, cs)
		}
	}
	comp.Slots = append(comp.Slots, shows...)
	if comp.Data {
		for _, p := range comp.Props {
			comp.Record.Fields = append(comp.Record.Fields, &ir.StructField{Name: p.Name, Type: p.Type})
		}
		if comp.ContentSlot() != nil {
			comp.Record.Fields = append(comp.Record.Fields, &ir.StructField{Name: ir.RecordID, Type: TypInt})
		}
		// An event is a function of the record taking the event's
		// parameters: the call site's handler, which the container calls.
		for _, e := range comp.Events {
			if slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == e.Name }) {
				c.error(comp.AST.Pos, "component %s: event @%s is a field of its record, which a prop of that name already is", comp.Name, e.Name)
				continue
			}
			comp.Record.Fields = append(comp.Record.Fields, &ir.StructField{Name: e.Name, Type: ir.EventFuncType(e)})
		}
		c.declPkg().Structs = append(c.declPkg().Structs, comp.Record)
		c.checkDataComponent(comp)
	}
}

// contentSlot is the slot a container of fam's records is given for their
// content, or nil where fam declares none: invoked with the record, and
// accepting what fam's content slot does.
func contentSlot(fam *ir.Component) *ir.SlotDecl {
	cs := fam.ContentSlot()
	if cs == nil {
		return nil
	}
	return &ir.SlotDecl{
		Name:    ir.ContentSlotName(fam),
		Params:  []*ir.Param{{Name: "r", Type: &ir.Type{Kind: ir.TypeStruct, Decl: fam.Record}}},
		Content: cs.Content,
		Card:    cs.Card,
		Shows:   cs,
	}
}

// checkDataComponent holds a data component to being a record of its props
// and events: it declares nothing else but slots of records and one rest slot
// of content, and has no body to render.
func (c *checker) checkDataComponent(comp *ir.Component) {
	if comp.AST == nil {
		return
	}
	pos := comp.AST.Pos
	if !comp.Bodyless {
		c.error(pos, "component %s names itself as its family, so it is data, read as a value and never rendered: remove the block", comp.Name)
	}
	var extra []string
	for _, s := range comp.Slots {
		if s.Values == nil && !s.Rest && s.Shows == nil {
			extra = append(extra, s.Name)
		}
	}
	if len(extra) > 0 {
		c.error(pos, "component %s names itself as its family, so it is data, whose props, events, slots of records and one rest slot of content are all it declares: %s", comp.Name, strings.Join(extra, ", "))
	}
	if cs := comp.ContentSlot(); cs != nil && cs.Arity() > 0 {
		c.error(pos, "component %s: its content is inserted by its container with nothing to bind, so slot %q takes no parameters", comp.Name, cs.Name)
	}
}

// refuseDataSlotInsertion reports an insertion of a data slot: the slot is a
// value its container reads, and nothing renders a record.
func (c *checker) refuseDataSlotInsertion(pos ast.Pos, slot *ir.SlotDecl) bool {
	if slot == nil || slot.Values == nil {
		return false
	}
	c.error(pos, "slot %q is a list of %s, read as a value and never inserted", slot.Name, ir.TypeFamily(slot.Content).Name)
	return true
}

// recordContent checks `r.content`, where r is a record of a data family whose
// content slot is named content: an insertion of the content slot the
// container was given for that family, handed r. It reports false where name
// is no such thing, and the node is checked as any other.
func (c *checker) recordContent(vn *ast.VisualNode, name string) (ir.Stmt, bool) {
	recv, field, ok := strings.Cut(name, ".")
	if !ok || strings.Contains(field, ".") {
		return nil, false
	}
	sym, ok := c.scope.Lookup(recv)
	if !ok {
		return nil, false
	}
	switch sym.(type) {
	case *ir.Namespace, *ir.Component:
		return nil, false
	}
	t := sym.SymType()
	if t == nil || t.Kind != ir.TypeStruct {
		return nil, false
	}
	sd, _ := t.Decl.(*ir.StructDef)
	if sd == nil || sd.RecordOf == nil {
		return nil, false
	}
	fam := sd.RecordOf
	cs := fam.ContentSlot()
	if cs == nil || cs.Name != field {
		return nil, false
	}
	slot := c.enclosingSlot(ir.ContentSlotName(fam))
	if slot == nil {
		c.error(vn.Pos, "%s.%s is what a %s was written with, which only a component taking %ss in a slot inserts, in its own body", recv, field, fam.Name, fam.Name)
		return nil, true
	}
	if len(vn.Args.Args) > 0 {
		c.error(vn.Pos, "%s.%s takes no arguments: it is inserted as it was written", recv, field)
	}
	arg := c.checkExpr(&ast.IdentExpr{Pos: vn.Pos, Name: recv})
	inst := &ir.SlotInst{AST: vn, Name: slot.Name, Decl: slot, Args: []ir.Expr{arg}}
	inst.Children = c.checkBlockIR(&vn.Block)
	return inst, true
}
