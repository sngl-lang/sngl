package lower

import (
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passReleaseTemps releases each owned value a statement makes and only
// borrows: `showText(ctx, cstring(s))` binds the `c.str` to a `__relN` and
// frees it after the statement, and one built from constants is made once and
// freed at teardown. What counts as a temp, and what is not released yet, is
// design/ownership.md's.
var passReleaseTemps = pass{
	name:    "ReleaseTemps",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, feats Features, opts Options) error {
		st := newReleaseTemps(pkg)
		for _, block := range append(imperativeBlocks(pkg), drawBlocks(pkg)...) {
			st.block(block)
		}
		if st.promote {
			return lowerLibFuncs(pkg, feats, opts)
		}
		return nil
	},
}

// ReleaseTemps is passReleaseTemps for the interpreter, which calls a library
// declaration where it is declared, so every body the program reaches is
// rewritten rather than the package's own. A frozen declaration is one the
// program does not reach (ir.ClonePackageFor).
func ReleaseTemps(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := newReleaseTemps(pkg)
	for _, block := range reachedBlocks(pkg) {
		st.block(block)
	}
	return nil
}

func reachedBlocks(pkg *ir.Package) []*[]ir.Stmt {
	out := imperativeBlocks(pkg)
	c := &blockCollector{imperative: true, seen: map[*[]ir.Stmt]bool{}}
	for _, b := range out {
		c.seen[b] = true
	}
	lambdas := func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
				c.add(&l.Func.Block)
			}
			return nil
		})
	}
	fn := func(f *ir.Func) {
		if f != nil && !f.Frozen && len(f.Block) > 0 {
			c.add(&f.Block)
			lambdas(f)
		}
	}
	seen := map[*ir.Package]bool{pkg: true}
	var visit func(p *ir.Package)
	visit = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			for _, o := range ir.Owners(imp.Pkg) {
				if o.Comp != nil {
					if o.Comp.Frozen {
						continue
					}
					c.viewIn(o.Body)
					lambdas(o.Comp)
				}
				for _, f := range o.Funcs {
					fn(f)
				}
			}
			visit(imp.Pkg)
		}
	}
	visit(pkg)
	return append(out, c.out...)
}

// drawBlocks is each canvas's children, which passShapeDraw made the
// statements that paint it: a block a host runs per frame, which no view walk
// treats as one.
func drawBlocks(pkg *ir.Package) []*[]ir.Stmt {
	var out []*[]ir.Stmt
	seen := map[*ir.NodeInst]bool{}
	add := func(n *ir.NodeInst) {
		if n != nil && !seen[n] && ir.IsShapeContainer(n) {
			seen[n] = true
			out = append(out, &n.Children)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			add(x)
		case *ir.LocalVar:
			add(x.CanvasNode)
		}
		return nil
	})
	return out
}

type releaseTemps struct {
	pkg     *ir.Package
	counter int
	// app is each application-lifetime value by what it computes, so two
	// writings of one constant share a declaration.
	app map[string]*ir.Var
	// promote says a library release was called after LibFuncs ran.
	promote bool
	event   *ir.Type
}

func newReleaseTemps(pkg *ir.Package) *releaseTemps {
	return &releaseTemps{pkg: pkg, app: map[string]*ir.Var{}}
}

type temp struct {
	sym     *ir.Var
	release *ir.Func
}

func (st *releaseTemps) block(block *[]ir.Stmt) {
	out := make([]ir.Stmt, 0, len(*block))
	// pending is the temps ErrorCatch bound as `__argN` for the call after
	// them, released once that call has been made.
	var pending []temp
	for _, s := range *block {
		switch n := s.(type) {
		case *ir.If, *ir.For, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				st.block(b)
			}
		}
		if t, ok := st.boundArg(s); ok {
			pending = append(pending, t)
			out = append(out, s)
			continue
		}
		out = append(out, st.stmt(s)...)
		out = append(out, st.releases(pending)...)
		pending = nil
	}
	*block = out
}

// boundArg is the temp a `__argN` ErrorCatch bound an allocation to.
func (st *releaseTemps) boundArg(s ir.Stmt) (temp, bool) {
	lv, ok := s.(*ir.LocalVar)
	if !ok || lv.Sym == nil || !lv.Sym.Synthesized || !isAllocation(lv.Init) {
		return temp{}, false
	}
	return temp{sym: lv.Sym, release: ir.ReleaseOf(lv.Init.ExprType())}, true
}

// stmt is s with its temps bound before it and released after it.
func (st *releaseTemps) stmt(s ir.Stmt) []ir.Stmt {
	var c tempCollector
	discarded := false
	switch n := s.(type) {
	case *ir.CallStmt:
		if n.Call == nil {
			return []ir.Stmt{s}
		}
		var e ir.Expr = n.Call
		c.visit(&e, isAllocation(e))
		if isAllocation(e) {
			// The statement's call is the temp: binding it is the call.
			discarded = true
		}
	case *ir.Assign:
		c.visit(&n.Value, false)
	case *ir.LocalVar:
		if n.Init != nil {
			c.visit(&n.Init, false)
		}
	case *ir.Return:
		if n.Value != nil {
			c.visit(&n.Value, false)
		}
	case *ir.If:
		if n.Catch == nil {
			c.visit(&n.Cond, false)
		}
	case *ir.Match:
		c.visit(&n.Subject, false)
	default:
		return []ir.Stmt{s}
	}
	if len(c.found) == 0 {
		return []ir.Stmt{s}
	}
	var pre []ir.Stmt
	var temps []temp
	for _, f := range c.found {
		if st.constant(f.expr) {
			*f.slot = st.application(f.expr)
			continue
		}
		name := "__rel" + strconv.Itoa(st.counter)
		st.counter++
		typ := f.expr.ExprType()
		sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
		pre = append(pre, &ir.LocalVar{Name: name, Type: typ, Init: f.expr, Sym: sym})
		*f.slot = &ir.Ident{Name: name, Sym: sym, Type: typ}
		temps = append(temps, temp{sym: sym, release: ir.ReleaseOf(typ)})
	}
	if discarded {
		return append(pre, st.releases(temps)...)
	}
	if len(temps) == 0 {
		return append(pre, s)
	}
	body, after := st.split(s)
	if raising := raisingCalls(body); len(raising) > 0 {
		body = []ir.Stmt{st.catchReleasing(body, temps, raising)}
	}
	out := append(pre, body...)
	out = append(out, st.releases(temps)...)
	return append(out, after...)
}

// split is s as what reads the temps and what runs once they are released. A
// `return` binds its result first; an `if` or `match` tests a bound head, so
// a branch that returns does not skip the release.
func (st *releaseTemps) split(s ir.Stmt) (body, after []ir.Stmt) {
	bind := func(e *ir.Expr, prefix string) ir.Stmt {
		name := prefix + strconv.Itoa(st.counter)
		st.counter++
		typ := (*e).ExprType()
		sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
		decl := &ir.LocalVar{Name: name, Type: typ, Init: *e, Sym: sym}
		*e = &ir.Ident{Name: name, Sym: sym, Type: typ}
		return decl
	}
	switch n := s.(type) {
	case *ir.Return:
		return []ir.Stmt{bind(&n.Value, "__ret")}, []ir.Stmt{s}
	case *ir.If:
		return []ir.Stmt{bind(&n.Cond, "__cond")}, []ir.Stmt{s}
	case *ir.Match:
		return []ir.Stmt{bind(&n.Subject, "__subj")}, []ir.Stmt{s}
	}
	return []ir.Stmt{s}, nil
}

func (st *releaseTemps) releases(temps []temp) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(temps))
	for _, t := range slices.Backward(temps) {
		out = append(out, st.releaseCall(t.release, &ir.Ident{Name: t.sym.Name, Sym: t.sym, Type: t.sym.Type}))
	}
	return out
}

func (st *releaseTemps) releaseCall(release *ir.Func, arg ir.Expr) ir.Stmt {
	if release.PkgPath() != "" {
		st.promote = true
	}
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: release, Args: []ir.CallArg{{Value: arg}}}}
}

// constant reports whether e is built from constants: a literal of an owned
// type whose fields are, or a call of a const function, or of sngl:x/c's
// `cstring`, whose arguments are.
func (st *releaseTemps) constant(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Spread || !constantArg(f.Value) {
				return false
			}
		}
		return true
	case *ir.Call:
		fn := x.Func
		if fn == nil || x.Receiver != nil || x.ErrorMode != ir.ErrorNone || !(fn.Const || fn.Builtin == ir.BuiltinCString) {
			return false
		}
		for _, a := range x.Args {
			if !constantArg(a.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// constantArg is a value the package itself can evaluate: a literal, or a
// package-level const. ir.IsConst also admits a const parameter, which is
// constant at each call site and not at all at package level.
func constantArg(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		v, ok := x.Sym.(*ir.Var)
		return x.Member != "" || ok && v.IsConst
	}
	return false
}

// application is the package-level declaration holding e, made once and
// released when the application tears down.
func (st *releaseTemps) application(e ir.Expr) ir.Expr {
	typ := e.ExprType()
	key, ok := exprKey(e)
	v := st.app[key]
	if !ok || v == nil {
		v = &ir.Var{Name: "__rel" + strconv.Itoa(st.counter), Type: typ, Init: e, Synthesized: true}
		st.counter++
		st.pkg.Vars = append(st.pkg.Vars, v)
		if ok {
			st.app[key] = v
		}
		st.teardown().Block = append(st.teardown().Block, st.releaseCall(ir.ReleaseOf(typ), &ir.Ident{Name: v.Name, Sym: v, Type: typ}))
	}
	return &ir.Ident{Name: v.Name, Sym: v, Type: typ}
}

func (st *releaseTemps) teardown() *ir.Func {
	if st.pkg.Teardown == nil {
		fn := &ir.Func{Name: TeardownFunc, Return: ir.TypVoid, Purity: ir.PurityMutates, Synthesized: true}
		st.pkg.Funcs = append(st.pkg.Funcs, fn)
		st.pkg.Teardown = fn
	}
	return st.pkg.Teardown
}

// catchReleasing is body run so that a raise out of it releases the temps
// first and then goes on as it would have: to the boundary or window it
// resolved to, or out to the caller.
//
// A raise resolved to a handler is turned into one that bubbles, so this catch
// sees it, and the raise again is resolved as it was. The interpreter runs the
// resolved handler at the call, so without the turn it would never reach here.
func (st *releaseTemps) catchReleasing(body []ir.Stmt, temps []temp, raising []*ir.Call) ir.Stmt {
	mode := ir.ErrorBubble
	var resolved *ir.EventHandler
	for _, c := range raising {
		if c.ErrorMode == ir.ErrorInvokeAndTerminate && c.ResolvedHandler != nil {
			mode, resolved = ir.ErrorInvokeAndTerminate, c.ResolvedHandler
			c.ErrorMode = ir.ErrorBubble
		}
	}
	evt := st.eventType()
	param := &ir.Param{Name: "__relErr", Type: evt}
	read := func(field string) ir.Expr {
		return &ir.Select{Operand: &ir.Ident{Name: param.Name, Sym: param, Type: evt}, Field: field, Type: ir.TypString}
	}
	again := raiseStmt("", "", nil).(*ir.CallStmt)
	again.Call.Args[0].Value = read("message")
	again.Call.Args[1].Value = read("kind")
	again.Call.ErrorMode = mode
	again.Call.ResolvedHandler = resolved
	h := &ir.EventHandler{Name: "error", Func: &ir.Func{
		Name:   "__relCatch",
		Params: []*ir.Param{param},
		Return: ir.TypVoid,
		Block:  append(st.releases(temps), again),
	}}
	return catchBlock(body, h)
}

// eventType is what an @error handler is handed, read off the
// #[builtin("errorBoundary")] component the way the checker reads it.
func (st *releaseTemps) eventType() *ir.Type {
	if st.event != nil {
		return st.event
	}
	seen := map[*ir.Package]bool{}
	var visit func(p *ir.Package)
	visit = func(p *ir.Package) {
		if p == nil || seen[p] || st.event != nil {
			return
		}
		seen[p] = true
		for _, c := range p.Components {
			if c.Builtin != ir.BuiltinErrorBoundary {
				continue
			}
			for _, e := range c.Events {
				if e.Name == "error" && len(e.Params) == 1 {
					st.event = e.Params[0].Type
					return
				}
			}
		}
		for _, imp := range p.Imports {
			visit(imp.Pkg)
		}
	}
	visit(st.pkg)
	return st.event
}

// raisingCalls is every call in body a raise can leave other than through a
// per-call handler, which ends the call and not the statement.
func raisingCalls(body []ir.Stmt) []*ir.Call {
	var out []*ir.Call
	visitCoveredCalls(body, func(c *ir.Call) {
		if c.ErrorMode != ir.ErrorNone && c.ErrorMode != ir.ErrorPerCall {
			out = append(out, c)
		}
	})
	return out
}

// isAllocation reports whether e makes an owned value, rather than reading
// one something else holds.
func isAllocation(e ir.Expr) bool {
	if e == nil || ir.ReleaseOf(e.ExprType()) == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		return x.Func != nil
	case *ir.StructLit:
		return true
	}
	return false
}

type foundTemp struct {
	slot *ir.Expr
	expr ir.Expr
}

// tempCollector finds a statement's temps in the order they are evaluated,
// an allocation's own arguments before it.
type tempCollector struct {
	found []foundTemp
}

// visit looks for temps under *slot, and takes *slot itself when borrowed
// says its value is only read where it stands.
func (c *tempCollector) visit(slot *ir.Expr, borrowed bool) {
	switch x := (*slot).(type) {
	case *ir.Call:
		if x.Receiver != nil {
			c.visit(&x.Receiver, true)
		}
		off := len(x.Args)
		if x.Func != nil {
			off -= len(x.Func.Params)
		}
		for i := range x.Args {
			c.visit(&x.Args[i].Value, borrows(x, paramIndex(x, i, off)))
		}
	case *ir.Select:
		c.visit(&x.Operand, true)
	case *ir.Index:
		c.visit(&x.Operand, true)
		c.visit(&x.Idx, false)
	case *ir.Binary:
		c.visit(&x.Left, true)
		if x.Op != ast.BinAnd && x.Op != ast.BinOr {
			c.visit(&x.Right, true)
		}
	case *ir.Unary:
		c.visit(&x.Operand, true)
	case *ir.Ternary:
		c.visit(&x.Cond, false)
	case *ir.StructLit:
		for i := range x.Fields {
			c.visit(&x.Fields[i].Value, false)
		}
	case *ir.ListLit:
		for i := range x.Elems {
			c.visit(&x.Elems[i], false)
		}
	}
	if borrowed && isAllocation(*slot) {
		c.found = append(c.found, foundTemp{slot: slot, expr: *slot})
	}
}

// borrows reports whether call's parameter i only reads its argument. Aligned
// from the end, as CMarshal aligns: a native in the receiver-first shape is
// called with the receiver as argument zero, which no parameter declares.
func borrows(call *ir.Call, i int) bool {
	fn := call.Func
	if fn == nil || ir.IsRelease(fn) {
		return false
	}
	if i < 0 || i >= len(fn.Params) {
		return true
	}
	return fn.Params[i].Transfer == ir.TransferBorrow
}

// paramIndex is the parameter call's argument i is for: by name where it is
// named, which the interpreter's unlowered calls may be in any order.
func paramIndex(call *ir.Call, i, off int) int {
	if name := call.Args[i].Name; name != "" && call.Func != nil {
		for j, p := range call.Func.Params {
			if p.Name == name {
				return j
			}
		}
	}
	return i - off
}
