package lower

import (
	"slices"
	"strconv"

	"duckfam.us/sngl/ir"
)

// aliasHandleLocals makes a local initialized from a node's `#id`, and never
// assigned, that `#id`: `var h = f` then `h.value` is `f.value`, which
// passNodePropReads answers as it answers any read through the `#id`. Left a
// local, no Go or Kotlin target had a value to hold: bubbletea and android
// keep no field for `f`, and fyne and gtk4 read `h.Value` off the widget.
func aliasHandleLocals(pkg *ir.Package) {
	blocks := imperativeBlocks(pkg)
	inits := map[*ir.Var]ir.Expr{}
	var collect func(stmts []ir.Stmt)
	collect = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil && lv.Type != nil && lv.Type.Kind == ir.TypeComponent {
				if id, ok := lv.Init.(*ir.Ident); ok {
					if _, ok := id.Sym.(*ir.Var); ok {
						inits[lv.Sym] = id
					}
				}
			}
			for _, b := range ir.TransparentBlocks(s) {
				collect(*b)
			}
		}
	}
	for _, b := range blocks {
		collect(*b)
	}
	if len(inits) == 0 {
		return
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		var target ir.Expr
		switch x := n.(type) {
		case *ir.Assign:
			target = x.Target
		case *ir.Toggle:
			target = x.Target
		}
		if id, ok := target.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok {
				delete(inits, v)
			}
		}
		return nil
	})
	var handleOf func(v *ir.Var, depth int) ir.Expr
	handleOf = func(v *ir.Var, depth int) ir.Expr {
		init, ok := inits[v]
		if !ok || depth > len(inits) {
			return nil
		}
		next := init.(*ir.Ident).Sym.(*ir.Var)
		if next.NodeHandle {
			return init
		}
		return handleOf(next, depth+1)
	}
	aliases := map[*ir.Var]ir.Expr{}
	for v := range inits {
		if h := handleOf(v, 0); h != nil {
			aliases[v] = h
		}
	}
	if len(aliases) == 0 {
		return
	}
	_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && aliases[v] != nil {
				return ir.CloneExprSharingDecls(aliases[v]), ir.SkipDir
			}
		}
		return n, nil
	})
	var drop func(stmts *[]ir.Stmt)
	drop = func(stmts *[]ir.Stmt) {
		*stmts = slices.DeleteFunc(*stmts, func(s ir.Stmt) bool {
			lv, ok := s.(*ir.LocalVar)
			return ok && aliases[lv.Sym] != nil
		})
		for _, s := range *stmts {
			for _, b := range ir.TransparentBlocks(s) {
				drop(b)
			}
		}
	}
	for _, b := range blocks {
		drop(b)
	}
}

// handPropsForNodeParams hands a function that reads a node's prop through
// a parameter the prop rather than the node: `func grab(x ui.input) =>
// x.value` called `grab(f)` becomes `grab(x__value string)` called
// `grab(f.value)`, a read through the `#id` that passNodePropReads answers
// where the call is, the binding included. A parameter nothing else reads is
// dropped, since no Go or Kotlin target has a node value to pass; the read
// still happens when the call does, as it did through the parameter.
//
// To a fixed point, since a caller passing its own parameter on, `grab(y)`,
// now reads `y.value` itself.
func (st *handleParams) handPropsForNodeParams() {
	for st.handPropsOnce() {
	}
}

func (st *handleParams) handPropsOnce() bool {
	pkg := st.pkg
	asValue := map[*ir.Func]bool{}
	calls := map[*ir.Func][]*ir.Call{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Ident:
			if fn, ok := x.Sym.(*ir.Func); ok {
				asValue[fn] = true
			}
		case *ir.Call:
			if x.Func != nil {
				calls[x.Func] = append(calls[x.Func], x)
			}
		}
		return nil
	})
	funcs := slices.Clone(pkg.Funcs)
	for _, c := range pkg.Components {
		funcs = append(funcs, c.Funcs...)
	}
	changed := false
	for _, fn := range funcs {
		if fn == nil || asValue[fn] || !st.passesProps(fn, calls[fn]) {
			continue
		}
		if st.specializePropParams(fn, calls[fn]) {
			changed = true
		}
	}
	return changed
}

// passesProps reports whether fn is a plain function every call of which is
// a direct, positional call handing each node parameter a path.
func (st *handleParams) passesProps(fn *ir.Func, calls []*ir.Call) bool {
	if fn.IsTest || len(fn.Block) == 0 || fn.Intrinsic != "" || fn.Foreign.Name != "" || fn.Receiver != nil || fn.RecvParam != nil || len(fn.TypeParams) > 0 || len(calls) == 0 {
		return false
	}
	for _, p := range fn.Params {
		if p.Receiver || p.Ambient || p.Variadic || p.Const {
			return false
		}
	}
	for _, c := range calls {
		if c.Receiver != nil || c.ReceiverArg || len(c.Args) != len(fn.Params) || c.Event != "" {
			return false
		}
		for i, a := range c.Args {
			if a.Name != "" && a.Name != fn.Params[i].Name {
				return false
			}
			if nodeParam(fn.Params[i]) != nil && !isPath(a.Value) {
				return false
			}
		}
	}
	return true
}

// nodeParam is the component p is a node of, or nil.
func nodeParam(p *ir.Param) *ir.Component {
	if p.Type == nil || p.Type.Kind != ir.TypeComponent {
		return nil
	}
	c, _ := p.Type.Decl.(*ir.Component)
	return c
}

func isPath(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		return true
	case *ir.Select:
		return isPath(x.Operand)
	}
	return false
}

type propParam struct {
	field string
	param *ir.Param
}

func (st *handleParams) specializePropParams(fn *ir.Func, calls []*ir.Call) bool {
	written := st.written(fn)
	own := map[*ir.Param]bool{}
	for _, p := range fn.Params {
		own[p] = true
	}
	reads := map[*ir.Param][]propParam{}
	taken := map[string]bool{}
	for _, p := range fn.Params {
		taken[p.Name] = true
	}
	propRead := func(sel *ir.Select) (*ir.Param, bool) {
		id, ok := sel.Operand.(*ir.Ident)
		if !ok || sel.Ref != ir.RefNone {
			return nil, false
		}
		p, ok := id.Sym.(*ir.Param)
		if !ok || written[p] || !own[p] {
			return nil, false
		}
		comp := nodeParam(p)
		if comp == nil || !slices.ContainsFunc(comp.Props, func(q *ir.Prop) bool { return q.Name == sel.Field }) {
			return nil, false
		}
		return p, true
	}
	_ = ir.Walk(fn, func(n ir.Node) error {
		sel, ok := n.(*ir.Select)
		if !ok {
			return nil
		}
		p, ok := propRead(sel)
		if !ok || slices.ContainsFunc(reads[p], func(r propParam) bool { return r.field == sel.Field }) {
			return nil
		}
		name := p.Name + "__" + sel.Field
		for i := 1; taken[name]; i++ {
			name = p.Name + "__" + sel.Field + strconv.Itoa(i)
		}
		taken[name] = true
		reads[p] = append(reads[p], propParam{field: sel.Field, param: &ir.Param{Name: name, Type: sel.Type}})
		return nil
	})
	if len(reads) == 0 {
		return false
	}
	// A parameter is kept where anything but a prop read or its own place in
	// a recursive call reads it.
	recursive := map[*ir.Ident]bool{}
	for _, c := range calls {
		for i, a := range c.Args {
			if id, ok := a.Value.(*ir.Ident); ok && id.Sym == fn.Params[i] {
				recursive[id] = true
			}
		}
	}
	keep := map[*ir.Param]bool{}
	_ = ir.Walk(fn, func(n ir.Node) error {
		if sel, ok := n.(*ir.Select); ok {
			if _, ok := propRead(sel); ok {
				return ir.SkipDir
			}
		}
		if id, ok := n.(*ir.Ident); ok && !recursive[id] {
			if p, ok := id.Sym.(*ir.Param); ok && reads[p] != nil {
				keep[p] = true
			}
		}
		return nil
	})

	for _, c := range calls {
		var args []ir.CallArg
		for i, p := range fn.Params {
			a := c.Args[i]
			if reads[p] == nil || keep[p] {
				args = append(args, a)
			}
			for _, r := range reads[p] {
				args = append(args, ir.CallArg{Value: &ir.Select{Type: r.param.Type, Operand: ir.CloneExprSharingDecls(a.Value), Field: r.field}})
			}
		}
		c.Args = args
	}
	var params []*ir.Param
	for _, p := range fn.Params {
		if reads[p] == nil || keep[p] {
			params = append(params, p)
		}
		for _, r := range reads[p] {
			params = append(params, r.param)
		}
	}
	fn.Params = params
	_ = ir.Rewrite(fn, func(n ir.Node) (ir.Node, error) {
		sel, ok := n.(*ir.Select)
		if !ok {
			return n, nil
		}
		p, ok := propRead(sel)
		if !ok {
			return n, nil
		}
		for _, r := range reads[p] {
			if r.field == sel.Field {
				return &ir.Ident{Name: r.param.Name, Type: r.param.Type, Sym: r.param}, ir.SkipDir
			}
		}
		return n, nil
	})
	return true
}
