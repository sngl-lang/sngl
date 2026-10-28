package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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
		blocks := append(imperativeBlocks(pkg), drawBlocks(pkg)...)
		// Flattened by passDeclarative into the statements that build the
		// view, a view body makes temps as any block does: a widget's
		// constructor hands C a string (#[gen.protocol(create)]).
		if !feats.Declarative {
			blocks = append(blocks, viewBlocks(pkg)...)
		}
		for _, block := range blocks {
			st.block(block)
		}
		st.packageVars()
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
	st.packageVars()
	return nil
}

// packageVars releases what each owned package var holds at teardown, after
// the application-lifetime values: a package var's initializer ran before any
// of them was made. OwnedState releases its overwrites, and leaves the last
// value to this pass because the effect lowering writes Teardown afresh
// between the two. A var something already releases is not the
// application's: an inlined instance's var is a package var by now, and its
// node's `@unmount` releases it.
func (st *releaseTemps) packageVars() {
	if st.pkg == nil {
		return
	}
	released := map[*ir.Var]bool{}
	_ = ir.Walk(st.pkg, func(n ir.Node) error {
		if call, ok := n.(*ir.Call); ok && call.Func != nil && ir.IsRelease(call.Func) && len(call.Args) == 1 {
			if id, ok := call.Args[0].Value.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok {
					released[v] = true
				}
			}
		}
		return nil
	})
	// An imported package's vars after the program's own: its initializers
	// ran first. A build has already merged them into the program's, so the
	// walk reaches each var once whichever list it is still in.
	vars := slices.Clone(st.pkg.Vars)
	for _, p := range importedPackages(st.pkg) {
		if !p.Library {
			vars = append(vars, p.Vars...)
		}
	}
	for _, v := range vars {
		if v.IsConst || v.NodeHandle || v.Synthesized || released[v] || !ir.Owned(v.Type) {
			continue
		}
		released[v] = true
		st.teardown().Block = append(st.teardown().Block, st.releaseCall(st.pkg.ReleaseFor(v.Type), identOf(v)))
	}
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
	// released is each bound argument already given a release, since a
	// lambda's body is reached again as part of the block it is written in.
	released map[*ir.Var]bool
	// promote says a library release was called after LibFuncs ran.
	promote bool
	event   *ir.Type
}

func newReleaseTemps(pkg *ir.Package) *releaseTemps {
	return &releaseTemps{pkg: pkg, app: map[string]*ir.Var{}, released: map[*ir.Var]bool{}}
}

type temp struct {
	sym     *ir.Var
	release *ir.Func
}

func (st *releaseTemps) block(block *[]ir.Stmt) {
	*block = ir.RewriteBlocks(*block, st.flat)
}

// flat is one statement list with each statement's temps bound before it and
// released after it; the lists it holds have already been done.
func (st *releaseTemps) flat(stmts []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for i := 0; i < len(stmts); i++ {
		bound, n := st.boundArgs(stmts[i:])
		out = append(out, stmts[i:i+n]...)
		i += n
		out = append(out, st.stmt(stmts[i])...)
		out = append(out, st.releases(bound)...)
	}
	return out
}

// boundArgs is the temps among the `__argN` ErrorCatch bound ahead of a call
// with a per-call handler, released once that call has been made, and how
// many statements the bindings are. The shape is what says so -- a run of
// synthesized locals the call after them reads -- since HandleParams and
// AsyncOffload bind a synthesized local the statements after it read too.
func (st *releaseTemps) boundArgs(stmts []ir.Stmt) ([]temp, int) {
	n := 0
	for n < len(stmts) {
		lv, ok := stmts[n].(*ir.LocalVar)
		if !ok || lv.Sym == nil || !lv.Sym.Synthesized || lv.Init == nil {
			break
		}
		n++
	}
	if n == 0 || n == len(stmts) {
		return nil, 0
	}
	cs, ok := stmts[n].(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.ErrorMode != ir.ErrorPerCall {
		return nil, 0
	}
	reads := map[*ir.Var]bool{}
	for _, a := range cs.Call.Args {
		if id, ok := a.Value.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok {
				reads[v] = true
			}
		}
	}
	var out []temp
	for _, s := range stmts[:n] {
		lv := s.(*ir.LocalVar)
		if !reads[lv.Sym] {
			return nil, 0
		}
		if isAllocation(lv.Init) && !st.released[lv.Sym] {
			st.released[lv.Sym] = true
			out = append(out, temp{sym: lv.Sym, release: st.pkg.ReleaseFor(lv.Init.ExprType())})
		}
	}
	return out, n
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
	case *ir.For:
		// An iterable head is evaluated once and read by every iteration, so
		// what it makes lives across the loop. A condition head is evaluated
		// per iteration, and ConditionalTemps has made it a call whose body
		// is reached as a block of its own.
		if k := ir.DeriveIterKind(n); k != ir.IterCondition && k != ir.IterForever {
			c.visit(&n.Iter, true)
		}
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
		pre = append(pre, st.bind(&ir.LocalVar{Name: name, Type: typ, Init: f.expr, Sym: sym}, temps)...)
		*f.slot = &ir.Ident{Name: name, Sym: sym, Type: typ}
		temps = append(temps, temp{sym: sym, release: st.pkg.ReleaseFor(typ)})
	}
	if discarded {
		return append(pre, st.releases(temps)...)
	}
	if len(temps) == 0 {
		return append(pre, s)
	}
	if loop, ok := s.(*ir.For); ok {
		st.releaseBeforeReturns(&loop.Body, temps)
		st.releaseBeforeReturns(&loop.Else, temps)
	}
	body, after := st.split(s)
	if raising := raisingCalls(body); len(raising) > 0 {
		body = []ir.Stmt{st.catchReleasing(body, temps, raising)}
	}
	out := append(pre, body...)
	out = append(out, st.releases(temps)...)
	return append(out, after...)
}

// bind is decl, run so that a raise while its value is made releases the
// temps bound before it: declared ahead and assigned in a catch block of its
// own. A binding that cannot raise, or has nothing bound before it, is the
// declaration alone.
func (st *releaseTemps) bind(decl *ir.LocalVar, before []temp) []ir.Stmt {
	assign := &ir.Assign{Target: identOf(decl.Sym), Op: ast.AssignSet, Value: decl.Init}
	raising := raisingCalls([]ir.Stmt{assign})
	if len(before) == 0 || len(raising) == 0 {
		return []ir.Stmt{decl}
	}
	decl.Init = nil
	return []ir.Stmt{decl, st.catchReleasing([]ir.Stmt{assign}, before, raising)}
}

// releaseBeforeReturns releases temps ahead of each `return` in a loop's
// body, which leaves the loop the releases after it would have ended. The
// result is bound first, as split binds a statement's own.
func (st *releaseTemps) releaseBeforeReturns(block *[]ir.Stmt, temps []temp) {
	var out []ir.Stmt
	for _, s := range *block {
		ret, ok := s.(*ir.Return)
		if !ok {
			for _, b := range ir.TransparentBlocks(s) {
				st.releaseBeforeReturns(b, temps)
			}
			out = append(out, s)
			continue
		}
		if ret.Value != nil {
			if _, plain := ret.Value.(*ir.Ident); !plain {
				body, _ := st.split(ret)
				out = append(out, body...)
			}
		}
		out = append(out, st.releases(temps)...)
		out = append(out, ret)
	}
	*block = out
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
		out = append(out, st.releaseCall(t.release, identOf(t.sym)))
	}
	return out
}

func (st *releaseTemps) releaseCall(release *ir.Func, arg ir.Expr) ir.Stmt {
	if callsLibrary(release) {
		st.promote = true
	}
	return releaseWith(release, arg)
}

// callsLibrary reports whether release is a library's, or a synthesized one
// whose body reaches a library's, which LibFuncs has to promote.
func callsLibrary(release *ir.Func) bool {
	if release.PkgPath() != "" {
		return true
	}
	if release.Releases == nil {
		return false
	}
	found := false
	_ = ir.Walk(release.Block, func(n ir.Node) error {
		if call, ok := n.(*ir.Call); ok && call.Func != nil && ir.IsRelease(call.Func) && callsLibrary(call.Func) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// constant reports whether e is built from constants: a literal of an owned
// type whose fields are, or a call of a const function, or of sngl:x/c's
// `cstring`, whose arguments are; a list or map literal of such values is
// one too.
func (st *releaseTemps) constant(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Spread || !st.constantValue(f.Value) {
				return false
			}
		}
		return true
	case *ir.ListLit:
		return !slices.ContainsFunc(x.Elems, func(e ir.Expr) bool { return !st.constantValue(e) })
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			if !constantArg(en.Key) || !st.constantValue(en.Value) {
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

func (st *releaseTemps) constantValue(e ir.Expr) bool {
	return constantArg(e) || isAllocation(e) && st.constant(e)
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
	key, ok := applicationKey(e)
	v := st.app[key]
	if !ok || v == nil {
		v = &ir.Var{Name: "__rel" + strconv.Itoa(st.counter), Type: typ, Init: e, Synthesized: true}
		st.counter++
		st.pkg.Vars = append(st.pkg.Vars, v)
		if ok {
			st.app[key] = v
		}
		st.teardown().Block = append(st.teardown().Block, st.releaseCall(st.pkg.ReleaseFor(typ), identOf(v)))
	}
	return identOf(v)
}

// applicationKey is exprKey with a struct literal, which CSE's key leaves out
// since two writings of one are two values: here they are one, made once.
func applicationKey(e ir.Expr) (string, bool) {
	lit, ok := e.(*ir.StructLit)
	if !ok {
		return exprKey(e)
	}
	var key strings.Builder
	key.WriteString("struct(" + identityOf(lit.Def))
	for _, f := range lit.Fields {
		fk, ok := exprKey(f.Value)
		if !ok {
			return "", false
		}
		key.WriteString("," + f.Name + "=" + fk)
	}
	return key.String() + ")", true
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
// per-call handler, which ends the call and not the statement. A catch block
// nested in body -- a loop's statement released its own temps -- raises again
// from its handler, which the walk does not enter on its own, and the raise
// again is one the catch around it has to see.
func raisingCalls(body []ir.Stmt) []*ir.Call {
	var out []*ir.Call
	var visit func(block []ir.Stmt)
	visit = func(block []ir.Stmt) {
		for _, s := range block {
			_ = ir.Walk(s, func(n ir.Node) error {
				switch x := n.(type) {
				case *ir.Lambda:
					return ir.SkipDir
				case *ir.If:
					if x.Catch != nil && x.Catch.Func != nil {
						visit(x.Catch.Func.Block)
					}
				case *ir.Call:
					if x.ErrorMode != ir.ErrorNone && x.ErrorMode != ir.ErrorPerCall {
						out = append(out, x)
					}
				}
				return nil
			})
		}
	}
	visit(body)
	return out
}

func isAllocation(e ir.Expr) bool { return ir.Allocates(e) }

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
//
// Every expression kind ir.Rewrite walks is named, and an unknown one panics
// as that walk does: each kind says what its operands are to it -- read, so a
// temp; stored, so held; or not evaluated here -- and a kind left out was a
// temp nothing released (a conversion, which is what an interpolated string
// is made of).
func (c *tempCollector) visit(slot *ir.Expr, borrowed bool) {
	switch x := (*slot).(type) {
	case nil:
		return
	case *ir.Call:
		if x.Receiver != nil {
			c.visit(&x.Receiver, true)
		}
		if x.Callee != nil {
			c.visit(&x.Callee, true)
		}
		off := len(x.Args)
		if x.Func != nil {
			off -= len(x.Func.Params)
		}
		for i := range x.Args {
			c.visit(&x.Args[i].Value, borrows(x, paramIndex(x, i, off)))
		}
	case *ir.Conversion:
		// An owned value converted to a type that holds it -- an option of
		// it, or it out of one -- passes through; converted to anything else
		// it is read.
		c.visit(&x.Operand, borrowed && ir.Owned(x.Type) && ir.Owned(x.Operand.ExprType()) || !ir.Owned(x.Type))
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
	case *ir.MatchExpr:
		c.visit(&x.Subject, true)
	case *ir.StructLit:
		for i := range x.Fields {
			c.visit(&x.Fields[i].Value, false)
		}
	case *ir.ListLit:
		for i := range x.Elems {
			c.visit(&x.Elems[i], false)
		}
	case *ir.MapLitIR:
		for i := range x.Entries {
			c.visit(&x.Entries[i].Key, false)
			c.visit(&x.Entries[i].Value, false)
		}
	case *ir.Spread:
		c.visit(&x.Operand, false)
	case *ir.Lambda, *ir.Closure, *ir.Literal, *ir.Ident, *ir.ContextRead, *ir.SlotEmpty:
		// A lambda's body runs when it is called, and a closure's state is
		// stored; the rest are leaves.
	default:
		panic(fmt.Sprintf("lower.ReleaseTemps: unhandled ir.Expr %T", x))
	}
	if borrowed && isAllocation(*slot) {
		c.found = append(c.found, foundTemp{slot: slot, expr: *slot})
	}
}

// borrows reports whether call's parameter i only reads its argument. Aligned
// from the end, as CMarshal aligns: a native in the receiver-first shape is
// called with the receiver as argument zero, which no parameter declares. An
// intrinsic's parameter says nothing: `push` stores what it is handed, and
// released after the statement the list held a freed value. A call through a
// function value names no declaration, and a parameter of one is a plain
// borrow: only a bodiless native can say otherwise, and a mark is a
// declaration's, which a value does not carry.
func borrows(call *ir.Call, i int) bool {
	fn := call.Func
	if fn == nil {
		return call.Callee != nil
	}
	if fn.Intrinsic != "" || ir.IsRelease(fn) {
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
