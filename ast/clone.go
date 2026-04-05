package ast

import "maps"

// Clone returns a deep copy of the Document. All slices, maps, and pointer
// fields are duplicated so that mutations to the clone do not affect the
// original.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	c := *d
	c.OutputDefaults = maps.Clone(d.OutputDefaults)
	c.Outputs = cloneSlice(d.Outputs)
	c.Structs = cloneStructDefs(d.Structs)
	c.Enums = cloneSlice(d.Enums)
	c.Imports = cloneSlice(d.Imports)
	c.Consts = cloneSlice(d.Consts)
	c.Data = cloneData(d.Data)
	c.Components = cloneComponents(d.Components)
	c.ImportedComponents = cloneComponents(d.ImportedComponents)
	c.Timers = cloneSlice(d.Timers)
	c.Styles = cloneStyleDecls(d.Styles)
	c.Windows = cloneWindows(d.Windows)
	c.App = d.App.clone()
	return &c
}

func (a *App) clone() *App {
	if a == nil {
		return nil
	}
	c := *a
	c.Children = cloneVisualNodes(a.Children)
	c.Windows = cloneWindows(a.Windows)
	return &c
}

func cloneWindows(s []*Window) []*Window {
	if s == nil {
		return nil
	}
	out := make([]*Window, len(s))
	for i, v := range s {
		c := *v
		c.Props = cloneExprMap(v.Props)
		if v.PropOrder != nil {
			c.PropOrder = make([]string, len(v.PropOrder))
			copy(c.PropOrder, v.PropOrder)
		}
		c.Consts = cloneSlice(v.Consts)
		c.Data = cloneData(v.Data)
		c.Functions = cloneSlice(v.Functions)
		c.Timers = cloneSlice(v.Timers)
		c.Children = cloneVisualNodes(v.Children)
		c.For = cloneForClause(v.For)
		out[i] = &c
	}
	return out
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
	c.Key = cloneExprPtr(vn.Key)
	c.Class = cloneExprPtr(vn.Class)
	c.If = cloneExprPtr(vn.If)
	c.Ref = cloneExprPtr(vn.Ref)
	c.For = cloneForClause(vn.For)
	c.Props = cloneExprMap(vn.Props)
	c.Events = cloneExprMap(vn.Events)
	c.Bindings = cloneExprMap(vn.Bindings)
	if vn.PropOrder != nil {
		c.PropOrder = make([]string, len(vn.PropOrder))
		copy(c.PropOrder, vn.PropOrder)
	}
	c.Children = cloneVisualNodes(vn.Children)
	return &c
}

func cloneExprPtr(e *Expr) *Expr {
	if e == nil {
		return nil
	}
	c := *e
	if e.Resolved != nil {
		r := *e.Resolved
		c.Resolved = &r
	}
	return &c
}

func cloneForClause(f *ForClause) *ForClause {
	if f == nil {
		return nil
	}
	c := *f
	c.Else = cloneVisualNodes(f.Else)
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
		c.Fields = cloneStructFields(v.Fields)
		out[i] = &c
	}
	return out
}

func cloneStructFields(s []*StructField) []*StructField {
	if s == nil {
		return nil
	}
	out := make([]*StructField, len(s))
	for i, v := range s {
		c := *v
		if v.Resolved != nil {
			r := *v.Resolved
			c.Resolved = &r
		}
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
		if v.Resolved != nil {
			r := *v.Resolved
			c.Resolved = &r
		}
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
		c.Consts = cloneSlice(v.Consts)
		c.Data = cloneData(v.Data)
		c.Timers = cloneSlice(v.Timers)
		c.EventDecls = cloneSlice(v.EventDecls)
		c.Body = cloneVisualNodes(v.Body)
		if v.PlatformBodies != nil {
			c.PlatformBodies = make(map[string][]*VisualNode, len(v.PlatformBodies))
			for k, nodes := range v.PlatformBodies {
				c.PlatformBodies[k] = cloneVisualNodes(nodes)
			}
		}
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
