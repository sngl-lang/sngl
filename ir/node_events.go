package ir

import (
	"slices"

	"duckfam.us/sngl/ast"
)

// Events is the events n's component declares, each parameter typed by what
// n's call site bound the component's type parameters to: an effect keyed on a
// list<Todo> reports `@insert(at int, item Todo)`. The declaration's own events
// where nothing was bound.
func (n *NodeInst) Events() []*EventDecl {
	if n == nil || n.Component == nil {
		return nil
	}
	comp := n.Component
	if len(n.TypeArgs) == 0 || len(comp.TypeParams) == 0 {
		return comp.Events
	}
	bindings := make(map[string]*Type, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		if i < len(n.TypeArgs) && n.TypeArgs[i] != nil && n.TypeArgs[i].Kind != TypeTypeParam {
			bindings[tp.Name] = n.TypeArgs[i]
		}
	}
	out := make([]*EventDecl, len(comp.Events))
	for i, e := range comp.Events {
		ev := *e
		ev.Params = make([]*Param, len(e.Params))
		for j, p := range e.Params {
			q := *p
			q.Type = p.Type.Substitute(bindings)
			ev.Params[j] = &q
		}
		out[i] = &ev
	}
	return out
}

// EventRefusal is why a handler for event may not be written on n, or "" where
// it may. It is the checker's rule, asked here too so that what an editor
// offers is what the checker accepts.
func (n *NodeInst) EventRefusal(event string) string {
	if n == nil || n.Component == nil || n.Component.Builtin != BuiltinEffect {
		return ""
	}
	return EffectEventRefusal(event, n.EffectKey())
}

// EffectKey is the value an effect's `on` was given, or nil.
func (n *NodeInst) EffectKey() Expr {
	for _, p := range n.Props {
		if p.Name == EffectOn {
			return p.Value
		}
	}
	return nil
}

// EffectEventRefusal is why an effect keyed on key -- nil for none -- cannot
// report event, phrased to follow "an effect's @<event> ", or "".
//
// A change event describes a write to a list held in a cell, so it needs a key
// that is one; `@update` describes a write to what the key reads, so it needs a
// key. `@mount` and `@unmount` need a key that compares, since a key that does
// not identifies no lifetime -- but that one is stated where the key's type is
// known for every call site that binds it (the checker's rebuild-key rule), so
// it is answered here only for a key whose type is already concrete.
func EffectEventRefusal(event string, key Expr) string {
	switch {
	case event == EffectMount || event == EffectUnmount:
		if key != nil {
			if t := key.ExprType(); t != nil && t.Kind != TypeTypeParam && RebuildIncomparable(t) != "" {
				return "brackets a lifetime, and `on` is " + t.String() + ", which does not compare, so it identifies none; handle @update to react to a write"
			}
		}
		return ""
	case event == EffectUpdate:
		if key == nil {
			return "reports a write to what `on` reads, and this effect has no `on`"
		}
		if name := copiedLoopVar(key); name != "" {
			return copiedLoopVarRefusal(name)
		}
		return ""
	case slices.Contains(EffectDeltas, event):
		switch {
		case key == nil:
			return "reports a change to the list `on` holds, and this effect has no `on`"
		case copiedLoopVar(key) != "":
			return copiedLoopVarRefusal(copiedLoopVar(key))
		case key.ExprType() == nil || key.ExprType().Kind != TypeList:
			return "reports a change to a list, and `on` is " + key.ExprType().String() + "; handle @update to react to a write"
		case !IsCellPath(key):
			return "reports a change to the list a var or prop holds, and `on` is computed from one; key the effect on the list itself, or handle @update"
		}
	}
	return ""
}

// copiedLoopVar is the first loop variable key reads that the loop bound by
// copy, or "". Nothing writes one -- a loop's variables are read-only unless
// bound with `&` -- so a report of writes to it would never run.
func copiedLoopVar(key Expr) string {
	name := ""
	_ = WalkExprs(key, func(e Expr) error {
		if id, ok := e.(*Ident); ok && name == "" {
			if lv, ok := id.Sym.(*LoopVar); ok && (lv.Type == nil || lv.Type.Kind != TypeRef) {
				name = id.Name
			}
		}
		return nil
	})
	return name
}

func copiedLoopVarRefusal(name string) string {
	return "reports a write to what `on` reads, and `on` reads the loop variable \"" + name + "\", a copy nothing writes; bind the element with `for var &" + name + "` to report writes to it"
}

// IsCellPath is whether e names storage a write lands in -- a var, a prop or a
// parameter, a list element a loop bound with `&`, or a field of one -- rather
// than a value computed from it.
func IsCellPath(e Expr) bool {
	switch x := e.(type) {
	case *Ident:
		switch s := x.Sym.(type) {
		case *Var, *Param:
			return true
		case *LoopVar:
			return s.Type != nil && s.Type.Kind == TypeRef
		}
	case *Unary:
		return x.Op == ast.UnaryDeref && IsCellPath(x.Operand)
	case *Select:
		if t := x.Operand.ExprType(); t == nil || t.Kind != TypeStruct {
			return false
		}
		return IsCellPath(x.Operand)
	}
	return false
}
