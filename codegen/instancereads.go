package codegen

import (
	"cmp"
	"slices"

	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// InstanceRead is a member a test reads or calls through an instance a target
// builds at run time -- `c.b.hits`, `c.b.label`, `c.r[1].reset()` -- which the
// test reaches through a method of its own surface (InstanceReadMethod), since
// the instance is the target's: a record on fyne and gtk4, a factory's closure
// on html, a composable's state on android.
type InstanceRead struct {
	// ID is the instance's `#id`, and Comp what it is an instance of.
	ID   string
	Comp *ir.Component
	// Member is the var, prop or func read. Func is set for a call, and Var
	// for a read: the var, or the cell a prop is promoted to (lower.PropCell),
	// beside the Prop.
	Member string
	Var    *ir.Var
	Prop   *ir.Prop
	Func   *ir.Func
	// Keyed is set for a read through one copy, which the method is handed
	// the key of (ir.CopyKey).
	Keyed bool
}

// Type is what the read yields: the var's or the prop's type, or the func's
// result.
func (r InstanceRead) Type() *ir.Type {
	switch {
	case r.Func != nil:
		return r.Func.Return
	case r.Prop != nil:
		return r.Prop.Type
	case r.Var != nil:
		return r.Var.Type
	}
	return ir.TypVoid
}

// InstanceReadMethod is the method of the test's surface answering r:
// `bReadHits`, `rReadTapsAt`, `bCallReset`.
func InstanceReadMethod(r InstanceRead) string {
	verb := "Read"
	if r.Func != nil {
		verb = "Call"
	}
	name := r.ID + verb + upperFirst(r.Member)
	if r.Keyed {
		name = InvokerAt(name)
	}
	return name
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]&^0x20) + s[1:]
}

// InstanceReads is every member pkg's tests read or call through an instance
// built at run time, once per method.
func InstanceReads(pkg *ir.Package) []InstanceRead {
	if pkg == nil {
		return nil
	}
	runtime := runtimeInstances(pkg)
	if len(runtime) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []InstanceRead
	fns, _, _ := CollectTestFuncs(pkg)
	for _, fn := range fns {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if r, ok := instanceRead(n, runtime); ok && !seen[InstanceReadMethod(r)] {
				seen[InstanceReadMethod(r)] = true
				out = append(out, r)
			}
			return nil
		})
	}
	slices.SortFunc(out, func(a, b InstanceRead) int { return cmp.Compare(InstanceReadMethod(a), InstanceReadMethod(b)) })
	return out
}

// runtimeInstances is the component each `#id` built at run time is an
// instance of: the node a target renders from the tree, or where a target
// building the tree by node ops binds its root.
func runtimeInstances(pkg *ir.Package) map[string]*ir.Component {
	out := map[string]*ir.Component{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.ID != "" && ni.Component != nil && ni.Component.RuntimeInstance {
			out[ni.ID] = ni.Component
			return nil
		}
		c, ok := n.(*ir.Call)
		if !ok || !isLowerIntrinsic(c, ir.NodeOpComponentRoot) || len(c.Args) == 0 {
			return nil
		}
		id := IdentBareName(c.Args[0].Value)
		if t := c.Args[0].Value.ExprType(); id != "" && t != nil && t.Kind == ir.TypeComponent {
			if comp, ok := t.Decl.(*ir.Component); ok && comp.RuntimeInstance {
				out[id] = comp
			}
		}
		return nil
	})
	return out
}

// instanceRead reads n as a member read or call through an instance in
// runtime.
func instanceRead(n ir.Node, runtime map[string]*ir.Component) (InstanceRead, bool) {
	var operand ir.Expr
	member := ""
	var fn *ir.Func
	switch x := n.(type) {
	case *ir.Select:
		operand, member = x.Operand, x.Field
	case *ir.Call:
		if x.Func == nil || x.Event != "" {
			return InstanceRead{}, false
		}
		operand, member, fn = x.Receiver, x.Func.Name, x.Func
		if operand == nil && len(x.Args) > 0 && len(x.Func.Params) > 0 && x.Func.Params[0].Receiver {
			operand = x.Args[0].Value
		}
	default:
		return InstanceRead{}, false
	}
	if operand == nil {
		return InstanceRead{}, false
	}
	id, path, ok := ir.InstanceRef(operand)
	if !ok {
		return InstanceRead{}, false
	}
	comp := runtime[id]
	if comp == nil {
		return InstanceRead{}, false
	}
	r := InstanceRead{ID: id, Comp: comp, Member: member, Keyed: len(path) > 0}
	if fn != nil {
		for _, f := range comp.Funcs {
			if f == fn || f.Name == fn.Name {
				r.Func = f
				return r, true
			}
		}
		return InstanceRead{}, false
	}
	for _, p := range comp.Props {
		if p.Name != member {
			continue
		}
		r.Prop = p
		// A prop of an instance is the cell it was promoted to.
		for _, v := range comp.Vars {
			if v.Name == lower.PropCell(member) {
				r.Var = v
			}
		}
		return r, true
	}
	for _, v := range comp.Vars {
		if v.Name == member {
			r.Var = v
			return r, true
		}
	}
	return InstanceRead{}, false
}

// LowerInstanceReads is a test body with each read or call through an instance
// built at run time replaced by a call of its InstanceReadMethod on the test's
// instance, handed the copy's key first for one copy. The body is copied: the
// test's own IR is shared by every target built from the package.
func LowerInstanceReads(stmts []ir.Stmt, pkg *ir.Package) []ir.Stmt {
	runtime := runtimeInstances(pkg)
	if len(runtime) == 0 {
		return stmts
	}
	out := ir.CloneStmtsSharingDecls(stmts)
	_ = ir.Rewrite(out, func(n ir.Node) (ir.Node, error) {
		r, ok := instanceRead(n, runtime)
		if !ok {
			return n, nil
		}
		var operand ir.Expr
		var rest []ir.CallArg
		switch x := n.(type) {
		case *ir.Select:
			operand = x.Operand
		case *ir.Call:
			operand, rest = x.Receiver, x.Args
			if operand == nil {
				operand, rest = x.Args[0].Value, x.Args[1:]
			}
		}
		_, path, _ := ir.InstanceRef(operand)
		var args []ir.CallArg
		if key := ir.CopyKey(path); key != nil {
			args = append(args, ir.CallArg{Value: key})
		}
		args = append(args, rest...)
		return &ir.Call{
			Type:     r.Type(),
			Receiver: instanceBase(operand),
			Func:     &ir.Func{Name: InstanceReadMethod(r), Return: r.Type()},
			Args:     args,
		}, ir.SkipDir
	})
	return out
}

// instanceBase is the test's instance a ref is selected off.
func instanceBase(e ir.Expr) ir.Expr {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Conversion:
			e = x.Operand
		default:
			return e
		}
	}
}
