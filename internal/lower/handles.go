package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// handleType is the type of a reference to n: a handle to the component it
// instantiates, as a program's own `#id` on it is.
func handleType(n *ir.NodeInst) *ir.Type {
	if n.Component == nil {
		// IR built by hand names an element and no declaration.
		return &ir.Type{Kind: ir.TypeComponent}
	}
	return &ir.Type{Kind: ir.TypeComponent, Decl: n.Component}
}

// rendersAsType is what an instance of comp attaches to the tree as: the node
// its body renders, which is a member of comp's family -- so a value of the
// family, as a handle to one of its members is. A component that takes the
// family it is placed in names none, and is a member of hosted, the family
// the position it stands in accepts -- or, where no position is in hand, of
// the type parameter its declaration's return position names.
func rendersAsType(comp *ir.Component, hosted *ir.Component) *ir.Type {
	f := comp.Family
	if f == nil {
		f = hosted
	}
	if f != nil {
		return &ir.Type{Kind: ir.TypeComponent, Decl: f}
	}
	// Asked of the declaration rather than of an instance standing
	// somewhere: its family is the type parameter its return position names.
	if comp.FamilyParam != nil {
		return comp.FamilyParam
	}
	panic(fmt.Sprintf("lower: an instance of %s stands where no family is known", comp.Name))
}

// hostedBy is the family a node of type t accepts as children: its rest
// slot's, or for a family value -- some member of it -- its own.
func hostedBy(t *ir.Type) *ir.Component {
	if t == nil {
		return nil
	}
	c, ok := t.Decl.(*ir.Component)
	if !ok || c == nil {
		return nil
	}
	if c.IsFamily() {
		return c
	}
	if f := ir.RestSlotFamily(c); f != nil {
		return f
	}
	return c.Family
}

// handleRef is a reference to a node a lowering pass named, typed t.
func handleRef(name string, t *ir.Type) *ir.Ident {
	return &ir.Ident{Name: name, Type: t, IsElementRef: true, Synthesized: true}
}

// appRef is the application the package body's nodes attach to: sngl:builtin's
// `_application`, which a target answers with its own.
func appRef() *ir.Ident {
	return handleRef(ir.AppParent, ir.Published().ApplicationType())
}

// propType is the type of n's prop named name: what its component declares,
// or, for a node whose component declares no such prop, the value written.
func propType(n *ir.NodeInst, name string, value ir.Expr) *ir.Type {
	if t := propValueType(n.Component, name); t != nil {
		return t
	}
	if value != nil {
		return value.ExprType()
	}
	return nil
}

// propsRecordType is the type of the props a component instance is built
// from: a struct of the fields the call site wrote, each at the type comp
// declares it, which no program spells and so no declaration names.
func propsRecordType(comp *ir.Component, fields []ir.FieldInit) *ir.Type {
	sd := &ir.StructDef{Anon: true}
	for _, f := range fields {
		t := propValueType(comp, f.Name)
		if t == nil && f.Value != nil {
			t = f.Value.ExprType()
		}
		sd.Fields = append(sd.Fields, &ir.StructField{Name: f.Name, Type: t})
	}
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
}

// nodeFamily is the family n's component is a member of, or nil.
func nodeFamily(n *ir.NodeInst) *ir.Component {
	if n == nil || n.Component == nil {
		return nil
	}
	return n.Component.Family
}
