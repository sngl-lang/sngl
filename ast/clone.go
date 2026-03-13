package ast

import "maps"

// Clone returns a deep copy of the Document. All slices, maps, and pointer
// fields are duplicated so that mutations to the clone do not affect the
// original. *cel.Ast pointers are shared because they are immutable after
// checking.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	c := *d
	c.Outputs = cloneSlice(d.Outputs)
	c.Structs = cloneStructDefs(d.Structs)
	c.Enums = cloneSlice(d.Enums)
	c.Imports = cloneSlice(d.Imports)
	c.Data = cloneData(d.Data)
	c.Computeds = cloneComputeds(d.Computeds)
	c.Components = cloneComponents(d.Components)
	c.Styles = cloneStyleDecls(d.Styles)
	c.StyleDefs = cloneSlice(d.StyleDefs)
	c.App = d.App.clone()
	return &c
}

func (a *App) clone() *App {
	if a == nil {
		return nil
	}
	c := *a
	c.Children = cloneVisualNodes(a.Children)
	return &c
}

func cloneVisualNodes(nodes []*VisualNode) []*VisualNode {
	if nodes == nil {
		return nil
	}
	out := make([]*VisualNode, len(nodes))
	for i, n := range nodes {
		out[i] = n.clone()
	}
	return out
}

func (vn *VisualNode) clone() *VisualNode {
	if vn == nil {
		return nil
	}
	c := *vn
	c.ID = cloneExprPtr(vn.ID)
	c.Key = cloneExprPtr(vn.Key)
	c.Class = cloneExprPtr(vn.Class)
	c.If = cloneExprPtr(vn.If)
	c.Ref = cloneExprPtr(vn.Ref)
	c.For = cloneForClause(vn.For)
	c.Props = cloneExprMap(vn.Props)
	c.Events = cloneExprMap(vn.Events)
	c.StyleAttrs = cloneExprMap(vn.StyleAttrs)
	c.StyleBlock = cloneExprMap(vn.StyleBlock)
	c.AttrNodes = cloneAttrNodes(vn.AttrNodes)
	c.Children = cloneVisualNodes(vn.Children)
	return &c
}

func cloneExprPtr(e *Expr) *Expr {
	if e == nil {
		return nil
	}
	c := *e
	return &c
}

func cloneForClause(f *ForClause) *ForClause {
	if f == nil {
		return nil
	}
	c := *f
	return &c
}

func cloneExprMap(m map[string]Expr) map[string]Expr {
	if m == nil {
		return nil
	}
	out := make(map[string]Expr, len(m))
	maps.Copy(out, m)
	return out
}

func cloneAttrNodes(m map[string]*AttrNode) map[string]*AttrNode {
	if m == nil {
		return nil
	}
	out := make(map[string]*AttrNode, len(m))
	for k, v := range m {
		c := *v
		c.Props = cloneExprMap(v.Props)
		out[k] = &c
	}
	return out
}

// cloneSlice shallow-copies a slice of pointers.
func cloneSlice[T any](s []*T) []*T {
	if s == nil {
		return nil
	}
	out := make([]*T, len(s))
	for i, v := range s {
		c := *v
		out[i] = &c
	}
	return out
}

func cloneStructDefs(s []*StructDef) []*StructDef {
	if s == nil {
		return nil
	}
	out := make([]*StructDef, len(s))
	for i, v := range s {
		c := *v
		c.Fields = cloneSlice(v.Fields)
		out[i] = &c
	}
	return out
}

func cloneData(s []*Data) []*Data {
	if s == nil {
		return nil
	}
	out := make([]*Data, len(s))
	for i, v := range s {
		c := *v
		if v.ParamTypes != nil {
			c.ParamTypes = make([]string, len(v.ParamTypes))
			copy(c.ParamTypes, v.ParamTypes)
		}
		out[i] = &c
	}
	return out
}

func cloneComputeds(s []*Computed) []*Computed {
	if s == nil {
		return nil
	}
	out := make([]*Computed, len(s))
	for i, v := range s {
		c := *v
		out[i] = &c
	}
	return out
}

func cloneComponents(s []*Component) []*Component {
	if s == nil {
		return nil
	}
	out := make([]*Component, len(s))
	for i, v := range s {
		c := *v
		c.Params = cloneSlice(v.Params)
		c.PropDecls = cloneSlice(v.PropDecls)
		c.EventDecls = cloneSlice(v.EventDecls)
		c.Body = cloneVisualNodes(v.Body)
		out[i] = &c
	}
	return out
}

func cloneStyleDecls(s []*StyleDecl) []*StyleDecl {
	if s == nil {
		return nil
	}
	out := make([]*StyleDecl, len(s))
	for i, v := range s {
		c := *v
		c.Props = cloneExprMap(v.Props)
		out[i] = &c
	}
	return out
}
