package build

import (
	"errors"
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A family whose override's gen.emit gives a `render` is generated as code the
// program runs, so its content may read state. The walk here keeps what the
// template walk decides -- an `if`, a `for` -- and hands it on, and a runner
// turns the result into something the target compiles.
//
// The walked tree is data: which member, where, and handles to the
// expressions and handlers it was written with. The handles index a table the
// walk owns, which is what a runner asks for the expressions behind them. The
// runner here is the one that answers in SNGL, rewriting the host into
// ordinary IR; one that writes host code through a direct API, or hands the
// tree to another process, reads the same tree and resolves the same handles
// its own way.

// codeStmt is one statement of a host's content: a member, or the `if` or
// `for` it was written under. Exactly one of the three is set.
type codeStmt struct {
	at     ast.Pos
	member *codeMember
	branch *codeBranch
	loop   *codeLoop
}

type codeMember struct {
	decl declRef
	// props holds every prop the member declares: the expression written at
	// the call site, else the declared default. A prop with neither is absent.
	props map[string]exprRef
	// events holds the handler written at the call site for each event that
	// has one.
	events map[string]handlerRef
}

type codeBranch struct {
	cond      exprRef
	then, els []codeStmt
}

type codeLoop struct {
	loop      loopRef
	iter      exprRef
	body, els []codeStmt
}

type (
	exprRef    int
	handlerRef int
	declRef    int
	loopRef    int
)

// codeTable is what the handles in a walked tree name. Every expression in it
// has had the props of each composed member it sits inside substituted
// already, so an expression means what it says wherever a runner puts it.
type codeTable struct {
	exprs    []ir.Expr
	handlers []*ir.EventHandler
	decls    []*ir.Component
	// loops are the `for`s the tree's loops were written as, for the
	// variables they bind.
	loops []*ir.For
}

func (t *codeTable) expr(e ir.Expr) exprRef {
	t.exprs = append(t.exprs, e)
	return exprRef(len(t.exprs) - 1)
}

func (t *codeTable) handler(h *ir.EventHandler) handlerRef {
	t.handlers = append(t.handlers, h)
	return handlerRef(len(t.handlers) - 1)
}

func (t *codeTable) decl(c *ir.Component) declRef {
	if i := slices.Index(t.decls, c); i >= 0 {
		return declRef(i)
	}
	t.decls = append(t.decls, c)
	return declRef(len(t.decls) - 1)
}

func (t *codeTable) loop(f *ir.For) loopRef {
	t.loops = append(t.loops, f)
	return loopRef(len(t.loops) - 1)
}

// codeMode reports whether a family override's gen.emit generates code.
func codeMode(emit *ir.NodeInst) bool {
	return argNamed(emit.Props, ir.GenEmitRender) != nil
}

// codeWalker reads a host's content for a family generated as code.
type codeWalker struct {
	t     Target
	table *codeTable
	// binds holds, innermost last, the props of each composed member the
	// walk is inside, bound to what they were written with.
	binds []map[ir.Symbol]ir.Expr
	// slots holds the children each composed member was written with, and
	// how many binding frames were live where they were written.
	slots []codeSlot
	// composing is the composed members the walk is inside, which is what
	// stops a recursive one: every `if` is kept, so nothing ends it.
	composing []*ir.Component
}

type codeSlot struct {
	stmts []ir.Stmt
	depth int
}

func (w *codeWalker) walk(stmts []ir.Stmt) ([]codeStmt, error) {
	var out []codeStmt
	for _, st := range stmts {
		var (
			nodes []codeStmt
			err   error
		)
		switch s := st.(type) {
		case *ir.NodeInst:
			nodes, err = w.walkMember(s)
		case *ir.If:
			var then, els []codeStmt
			if then, err = w.walk(s.Body); err == nil {
				if els, err = w.walk(s.Else); err == nil {
					nodes = []codeStmt{{at: stmtAt(s.AST), branch: &codeBranch{cond: w.table.expr(w.subst(s.Cond)), then: then, els: els}}}
				}
			}
		case *ir.For:
			nodes, err = w.walkFor(s)
		case *ir.SlotInst:
			nodes, err = w.walkSlot(s)
		default:
			err = posErr(stmtAt(stmtAST(st)), "an emitted family's content is its members, and the ifs and fors around them")
		}
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}

func (w *codeWalker) walkFor(s *ir.For) ([]codeStmt, error) {
	if s.Iter == nil || s.KeySym == nil {
		return nil, posErr(stmtAt(s.AST), "an emitted family's loop walks a list")
	}
	body, err := w.walk(s.Body)
	if err != nil {
		return nil, err
	}
	els, err := w.walk(s.Else)
	if err != nil {
		return nil, err
	}
	return []codeStmt{{at: stmtAt(s.AST), loop: &codeLoop{loop: w.table.loop(s), iter: w.table.expr(w.subst(s.Iter)), body: body, els: els}}}, nil
}

// walkSlot reads a composed member's insertion of its rest slot: the children
// it was written with, read where they were written.
func (w *codeWalker) walkSlot(s *ir.SlotInst) ([]codeStmt, error) {
	if !s.Rest || len(w.slots) == 0 {
		return nil, posErr(stmtAt(s.AST), "an emitted family's member inserts the children it was written with, and nothing else")
	}
	top := w.slots[len(w.slots)-1]
	saved, savedBinds := w.slots, w.binds
	w.slots, w.binds = w.slots[:len(w.slots)-1], w.binds[:top.depth]
	defer func() { w.slots, w.binds = saved, savedBinds }()
	if len(top.stmts) == 0 {
		return w.walk(s.Children)
	}
	return w.walk(top.stmts)
}

func (w *codeWalker) walkMember(ni *ir.NodeInst) ([]codeStmt, error) {
	at := nodePos(ni)
	comp := ni.Component
	if len(ni.Slots) > 0 {
		return nil, posErr(at, "%s is a member of a generated family, which has no slot to fill", comp.Name)
	}
	body, ok := override(comp, w.t)
	if ok && soleGenNode(body, ir.BuiltinGenNode) != nil {
		if len(ni.Children) > 0 {
			return nil, posErr(at, "%s contributes a value to its family's code, which has nowhere to put children", comp.Name)
		}
		m := &codeMember{decl: w.table.decl(comp), props: map[string]exprRef{}, events: map[string]handlerRef{}}
		for _, p := range comp.Props {
			switch arg := argNamed(ni.Props, p.Name); {
			case arg != nil:
				m.props[p.Name] = w.table.expr(w.subst(arg))
			case p.Default != nil:
				m.props[p.Name] = w.table.expr(ir.CloneExprSharingDecls(p.Default))
			}
		}
		for i := range ni.Handlers {
			h := w.substHandler(&ni.Handlers[i])
			m.events[h.Name] = w.table.handler(h)
		}
		return []codeStmt{{at: at, member: m}}, nil
	}
	if comp.Bodyless {
		return nil, posErr(at, "the %s family emits for %s, and %s says nothing about what it contributes: give it an override whose body is a gen.node, or a body of its own",
			comp.Tree.Name, describe(w.t), comp.Name)
	}
	if len(ni.Handlers) > 0 {
		return nil, posErr(at, "%s composes the members its body renders, and a handler written on it reaches none of them", comp.Name)
	}
	if slices.Contains(w.composing, comp) {
		return nil, posErr(at, "%s renders itself, which a generated family's content cannot: nothing in it is decided before the program runs", comp.Name)
	}
	frame := map[ir.Symbol]ir.Expr{}
	for _, p := range comp.Props {
		var e ir.Expr
		switch arg := argNamed(ni.Props, p.Name); {
		case arg != nil:
			e = w.subst(arg)
		case p.Default != nil:
			e = ir.CloneExprSharingDecls(p.Default)
		}
		if e != nil && p.Sym != nil {
			frame[p.Sym] = e
		}
	}
	w.slots = append(w.slots, codeSlot{stmts: ni.Children, depth: len(w.binds)})
	w.binds = append(w.binds, frame)
	w.composing = append(w.composing, comp)
	defer func() {
		w.slots = w.slots[:len(w.slots)-1]
		w.binds = w.binds[:len(w.binds)-1]
		w.composing = w.composing[:len(w.composing)-1]
	}()
	return w.walk(comp.Body)
}

// subst is e as written, with the props of each composed member the walk is
// inside replaced by what they were bound to.
func (w *codeWalker) subst(e ir.Expr) ir.Expr {
	return substitute(e, w.lookup)
}

func (w *codeWalker) lookup(sym ir.Symbol) (ir.Expr, bool) {
	for _, v := range slices.Backward(w.binds) {
		if e, ok := v[sym]; ok {
			return e, true
		}
	}
	return nil, false
}

func (w *codeWalker) substHandler(h *ir.EventHandler) *ir.EventHandler {
	if len(w.binds) == 0 || h.Func == nil {
		return h
	}
	fn := *h.Func
	fn.Block = substituteStmts(h.Func.Block, w.lookup)
	cp := *h
	cp.Func = &fn
	return &cp
}

// substitute copies e, replacing each identifier whose symbol bind answers
// with a copy of what it answers.
func substitute(e ir.Expr, bind func(ir.Symbol) (ir.Expr, bool)) ir.Expr {
	if e == nil {
		return nil
	}
	holder := []ir.Stmt{&ir.LocalVar{Init: ir.CloneExprSharingDecls(e)}}
	rewriteIdents(holder, bind)
	return holder[0].(*ir.LocalVar).Init
}

func substituteStmts(stmts []ir.Stmt, bind func(ir.Symbol) (ir.Expr, bool)) []ir.Stmt {
	out := ir.CloneStmtsSharingDecls(stmts)
	rewriteIdents(out, bind)
	return out
}

func rewriteIdents(root []ir.Stmt, bind func(ir.Symbol) (ir.Expr, bool)) {
	_ = ir.RewriteExprs(root, func(x ir.Expr) (ir.Expr, error) {
		if id, ok := x.(*ir.Ident); ok && id.Sym != nil {
			if v, ok := bind(id.Sym); ok {
				return ir.CloneExprSharingDecls(v), ir.SkipDir
			}
		}
		return x, nil
	})
}

// runSNGL is the runner that answers in SNGL. It writes one function
// returning the members' values -- the tree's `if`s and `for`s as they were,
// each member's gen.node value with its props bound and each of its events a
// call of the handler written for it -- and returns the family's gen.emit,
// handed that function as `members`, to stand where the host stood.
func runSNGL(pkg *ir.Package, h *codeHost, t Target) (ir.Stmt, error) {
	// The members are whatever `render` takes a list of: the one thing the
	// override wrote that says what T is.
	render := argNamed(h.emit.Props, ir.GenEmitRender)
	rt := render.ExprType()
	if rt == nil || rt.Kind != ir.TypeFunc || rt.Sig == nil || len(rt.Sig.Params) != 1 {
		return nil, posErr(nodePos(h.emit), "gen.emit's render takes the list of members")
	}
	listType := rt.Sig.Params[0].Type
	if listType == nil || listType.Kind != ir.TypeList || len(listType.Elems) != 1 {
		return nil, posErr(nodePos(h.emit), "gen.emit's render takes the list of members")
	}
	elem := listType.Elems[0]

	r := &snglRunner{table: h.table, t: t, family: h.family, elem: elem}
	r.out = &ir.Var{Name: "__members", Type: listType, Synthesized: true}
	body, err := r.stmts(h.content)
	if err != nil {
		return nil, err
	}
	block := make([]ir.Stmt, 0, len(body)+2)
	block = append(block, &ir.LocalVar{Name: r.out.Name, Type: listType, Init: &ir.ListLit{Type: listType}, Sym: r.out})
	block = append(block, body...)
	block = append(block, &ir.Return{Value: r.outRef()})

	// Not marked Synthesized: that says a lowering pass wrote it, and the
	// targets emit such a func as one of their own -- a slot's render, an
	// effect's settle -- which returns nothing. This is the program's, written
	// by the build in its place, and is emitted as any function would be.
	fn := &ir.Func{
		Name:   uniqueFuncName(pkg, "__"+h.host.Component.Name+"_members"),
		Return: listType,
		Block:  block,
	}
	checker.AnalyzeSynthesizedFunc(pkg, h.owner, fn)
	if h.owner != nil {
		// The component's own, so the inliner renames what it reads with
		// the rest of what the component declares when it splices the body.
		h.owner.Funcs = append(h.owner.Funcs, fn)
	} else {
		pkg.Funcs = append(pkg.Funcs, fn)
	}

	inst := ir.CloneStmtsSharingDecls([]ir.Stmt{h.emit})[0].(*ir.NodeInst)
	inst.Props = append(slices.DeleteFunc(inst.Props, func(a ir.Arg) bool { return a.Name == ir.GenEmitMembers }), ir.Arg{
		Name:  ir.GenEmitMembers,
		Value: &ir.Ident{Name: fn.Name, Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Sym: fn},
	})
	return inst, nil
}

type snglRunner struct {
	table  *codeTable
	t      Target
	family *ir.Component
	elem   *ir.Type
	out    *ir.Var
}

func (r *snglRunner) outRef() ir.Expr {
	return &ir.Ident{Name: r.out.Name, Type: r.out.Type, Sym: r.out, Synthesized: true}
}

func (r *snglRunner) stmts(tree []codeStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	var errs []error
	for _, s := range tree {
		switch {
		case s.member != nil:
			st, err := r.member(s.at, s.member)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, st)
		case s.branch != nil:
			then, err1 := r.stmts(s.branch.then)
			els, err2 := r.stmts(s.branch.els)
			if err := errors.Join(err1, err2); err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, &ir.If{Cond: r.table.exprs[s.branch.cond], Body: then, Else: els})
		case s.loop != nil:
			body, err1 := r.stmts(s.loop.body)
			els, err2 := r.stmts(s.loop.els)
			if err := errors.Join(err1, err2); err != nil {
				errs = append(errs, err)
				continue
			}
			orig := r.table.loops[s.loop.loop]
			out = append(out, &ir.For{
				Key: orig.Key, Value: orig.Value, KeySym: orig.KeySym, ValueSym: orig.ValueSym,
				Iter: r.table.exprs[s.loop.iter], ElemType: orig.ElemType, IterKind: orig.IterKind,
				Body: body, Else: els,
			})
		}
	}
	return out, errors.Join(errs...)
}

// member is the push of one member's value: its gen.node's `value`, with each
// of its props read as what the call site bound it to and each of its events
// a call of the handler written for it.
func (r *snglRunner) member(at ast.Pos, m *codeMember) (ir.Stmt, error) {
	comp := r.table.decls[m.decl]
	body, _ := override(comp, r.t)
	node := soleGenNode(body, ir.BuiltinGenNode)
	value := argNamed(node.Props, ir.GenNodeValue)
	if value == nil {
		return nil, posErr(at, "the %s family generates code for %s, and %s's gen.node gives no value to contribute", r.family.Name, describe(r.t), comp.Name)
	}
	props := map[ir.Symbol]ir.Expr{}
	for _, p := range comp.Props {
		if ref, ok := m.props[p.Name]; ok && p.Sym != nil {
			props[p.Sym] = r.table.exprs[ref]
		}
	}
	v := substitute(value, func(s ir.Symbol) (ir.Expr, bool) {
		e, ok := props[s]
		return e, ok
	})
	holder := []ir.Stmt{&ir.LocalVar{Init: v}}
	_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
		// An event read as a value is a lambda emitting it; the lambda is the
		// value's own copy, so its body is rewritten in place.
		if lam, ok := x.(*ir.Lambda); ok && lam.Func != nil {
			lam.Func.Block = r.emits(lam.Func.Block, m)
		}
		return x, nil
	})
	return &ir.CallStmt{Call: listPush(r.outRef(), holder[0].(*ir.LocalVar).Init, r.elem)}, nil
}

// emits replaces each emit of the member's own events in stmts with a call of
// the handler the call site wrote for it, or with nothing where it wrote none.
func (r *snglRunner) emits(stmts []ir.Stmt, m *codeMember) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, st := range stmts {
		emit, ok := st.(*ir.Emit)
		if !ok {
			out = append(out, st)
			continue
		}
		if ref, ok := m.events[emit.Name]; ok {
			out = append(out, callHandler(r.table.handlers[ref], emit.Args))
		}
	}
	return out
}

// callHandler is a call of h with the event's arguments: a handler binds them
// by position and may stop short, so it is handed as many as it names.
func callHandler(h *ir.EventHandler, args []ir.CallArg) ir.Stmt {
	fn := h.Func
	n := min(len(fn.Params), len(args))
	return &ir.CallStmt{Call: &ir.Call{
		Type:   ir.TypVoid,
		Callee: &ir.Lambda{Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Func: fn},
		Args:   slices.Clone(args[:n]),
	}}
}

func listPush(dst, v ir.Expr, elem *ir.Type) *ir.Call {
	var params []*ir.Param
	var ret *ir.Type
	if def := ir.LookupIntrinsic("list.push"); def != nil {
		params, ret = def.Instantiate(elem)
	}
	return &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Name: "push", Receiver: "list", Intrinsic: "list.push", Params: params, Return: ret},
		Args: []ir.CallArg{{Value: dst}, {Value: v}},
	}
}

func uniqueFuncName(pkg *ir.Package, base string) string {
	taken := map[string]bool{}
	for _, f := range pkg.Funcs {
		taken[f.Name] = true
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			taken[f.Name] = true
		}
	}
	name := base
	for i := 1; taken[name]; i++ {
		name = base + strconv.Itoa(i)
	}
	return name
}

// codeHost is one place the program renders a family generated as code.
type codeHost struct {
	// owner is the root component whose body the host is written in, or nil
	// for the package body: the members function reads what the body can, so
	// it belongs to the same declaration.
	owner   *ir.Component
	host    *ir.NodeInst
	family  *ir.Component
	emit    *ir.NodeInst
	content []codeStmt
	table   *codeTable
}

// emitCode walks a host whose family generates code and returns what stands
// in its place.
func emitCode(pkg *ir.Package, owner *ir.Component, host *ir.NodeInst, family *ir.Component, emit *ir.NodeInst, t Target) (ir.Stmt, error) {
	if len(host.Slots) > 0 {
		return nil, posErr(nodePos(host), "%s's members are written as its children", host.Component.Name)
	}
	w := &codeWalker{t: t, table: &codeTable{}}
	content, err := w.walk(host.Children)
	if err != nil {
		return nil, err
	}
	return runSNGL(pkg, &codeHost{owner: owner, host: host, family: family, emit: emit, content: content, table: w.table}, t)
}
