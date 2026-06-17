package bubbletea

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// blueprintKind identifies which of the three inlined platform primitives a
// stdlib component renders through. It is decided structurally from the
// inlined node's props (see extractBlueprint): a `model` prop ⇒ Widget, a
// `join` prop ⇒ Layout, otherwise Styled.
type blueprintKind int

const (
	bpStyled blueprintKind = iota
	bpLayout
	bpWidget
)

// joinDir mirrors the JoinDir enum declared in bubbletea.sngl. It selects the
// lipgloss join axis for a Layout primitive.
type joinDir int

const (
	joinVertical joinDir = iota
	joinHorizontal
)

// modelMeta mirrors the SNGL `Model` record: the per-widget bubbles model
// metadata that drives Widget rendering (field type, constructor, view method,
// update/init hooks, and the Go import path).
type modelMeta struct {
	Type   string
	New    string
	View   string
	Update string
	Init   string
	Pkg    string
}

// bindMeta mirrors the SNGL `Bind` record: a two-way binding between a SNGL
// prop and the getter on the underlying bubbles model (e.g. prop "value"
// reads back via ".Value()").
type bindMeta struct {
	Prop string
	Get  string
}

// eventMeta mirrors the SNGL `Event` record: a key-triggered event handler
// (e.g. on "submit" when key "enter").
type eventMeta struct {
	On  string
	Key string
}

// blueprint is the platform-driven render directive extracted off one inlined
// primitive *ir.NodeInst. The renderer (Phase 2) consumes this instead of
// branching on stdlib component name.
type blueprint struct {
	Kind blueprintKind

	// Layout
	Join joinDir

	// Styled / Widget content expression (the `content` prop).
	Content ir.Expr

	// Focus reports whether the primitive allocates a focus index
	// (`focus=Focus{enabled=true}`).
	Focus bool

	// Widget
	Model  modelMeta
	Binds  []bindMeta
	Events []eventMeta

	// Placeholder carries the `placeholder` prop for input-style widgets.
	Placeholder ir.Expr
}

// extractBlueprint reads a blueprint off an inlined primitive node. Kind is
// decided structurally: a `model` prop ⇒ Widget; else a `join` prop ⇒ Layout;
// else Styled.
func extractBlueprint(n *ir.NodeInst) blueprint {
	var bp blueprint

	modelProp := codegen.NodeProp(n, "model")
	joinProp := codegen.NodeProp(n, "join")

	switch {
	case modelProp != nil:
		bp.Kind = bpWidget
	case joinProp != nil:
		bp.Kind = bpLayout
	default:
		bp.Kind = bpStyled
	}

	if joinProp != nil {
		bp.Join = extractJoinDir(joinProp)
	}
	bp.Content = codegen.NodeProp(n, "content")
	bp.Placeholder = codegen.NodeProp(n, "placeholder")
	bp.Focus = extractFocus(codegen.NodeProp(n, "focus"))

	if modelProp != nil {
		bp.Model = extractModel(modelProp)
	}
	bp.Binds = extractBinds(codegen.NodeProp(n, "binds"))
	bp.Events = extractEvents(codegen.NodeProp(n, "events"))

	return bp
}

// extractJoinDir reads a JoinDir enum member off a prop value. A bare enum
// member appears as an *ir.Ident with Member set; a qualified `JoinDir.horizontal`
// (the form the platform bodies use) survives as an *ir.Select whose Field names
// the member. Either shape with the "horizontal" member selects the horizontal
// axis; anything else (incl. "vertical") defaults to vertical.
func extractJoinDir(e ir.Expr) joinDir {
	switch v := e.(type) {
	case *ir.Ident:
		if v.Member == "horizontal" {
			return joinHorizontal
		}
	case *ir.Select:
		if v.Field == "horizontal" {
			return joinHorizontal
		}
	}
	return joinVertical
}

// extractFocus reads a `Focus{enabled=...}` struct literal.
func extractFocus(e ir.Expr) bool {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return false
	}
	if v := structField(sl, "enabled"); v != nil {
		b, _ := codegen.IRLiteralBool(v)
		return b
	}
	return false
}

// extractModel reads a `Model{...}` struct literal.
func extractModel(e ir.Expr) modelMeta {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return modelMeta{}
	}
	var m modelMeta
	for _, f := range sl.Fields {
		s, _ := codegen.IRLiteralString(f.Value)
		switch f.Name {
		case "type":
			m.Type = s
		case "new":
			m.New = s
		case "view":
			m.View = s
		case "update":
			m.Update = s
		case "init":
			m.Init = s
		case "pkg":
			m.Pkg = s
		}
	}
	return m
}

// extractBinds reads a `[Bind{...}, ...]` list-of-structs prop. Lists appear in
// IR as an *ir.ListLit whose Elems are *ir.StructLit.
func extractBinds(e ir.Expr) []bindMeta {
	ll, ok := e.(*ir.ListLit)
	if !ok {
		return nil
	}
	out := make([]bindMeta, 0, len(ll.Elems))
	for _, el := range ll.Elems {
		sl, ok := el.(*ir.StructLit)
		if !ok {
			continue
		}
		var b bindMeta
		if v := structField(sl, "prop"); v != nil {
			b.Prop, _ = codegen.IRLiteralString(v)
		}
		if v := structField(sl, "get"); v != nil {
			b.Get, _ = codegen.IRLiteralString(v)
		}
		out = append(out, b)
	}
	return out
}

// extractEvents reads a `[Event{...}, ...]` list-of-structs prop.
func extractEvents(e ir.Expr) []eventMeta {
	ll, ok := e.(*ir.ListLit)
	if !ok {
		return nil
	}
	out := make([]eventMeta, 0, len(ll.Elems))
	for _, el := range ll.Elems {
		sl, ok := el.(*ir.StructLit)
		if !ok {
			continue
		}
		var ev eventMeta
		if v := structField(sl, "on"); v != nil {
			ev.On, _ = codegen.IRLiteralString(v)
		}
		if v := structField(sl, "key"); v != nil {
			ev.Key, _ = codegen.IRLiteralString(v)
		}
		out = append(out, ev)
	}
	return out
}

// structField returns the value of a named field in a struct literal, or nil.
func structField(sl *ir.StructLit, name string) ir.Expr {
	for _, f := range sl.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}
