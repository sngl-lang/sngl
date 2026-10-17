package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passTestInstanceReach makes every reach a test makes into its instance a
// reach through the test's own parameter: a local holding the instance is
// replaced by what it holds, and a function the test hands the instance to is
// inlined where it is called, `held.k.click()` and `clickK(c)` each becoming
// `c.k.click()`.
//
// Every target's test emitter spells a member of the instance off the
// parameter it declared -- a raw field on the Go targets, a presence check
// beside an invoker -- and has no spelling for one held anywhere else: emitted
// as a function of the program, a helper's parameter is a Model it was never
// handed. The interpreter answers either form, so this is what makes every
// target answer as it does.
//
// First, so each pass after it that reads a test's reach (TestRefOwner, the
// inliners' test_reach, NodePropReads) reads the one form.
var passTestInstanceReach = pass{
	name:    "TestInstanceReach",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if pkg == nil {
			return nil
		}
		params := testInstances(pkg, opts)
		if len(params) == 0 {
			return nil
		}
		ti := &testInstance{params: params, names: namesOrOwn(opts.names)}
		for _, fn := range pkg.Funcs {
			if fn == nil || !fn.IsTest {
				continue
			}
			block, err := ti.through(fn.Block, map[ir.Symbol]ir.Expr{}, nil)
			if err != nil {
				return err
			}
			fn.Block = block
		}
		return nil
	},
}

type testInstance struct {
	params map[*ir.Param]bool
	names  renamer
}

// rooted reports whether e names the test's instance or something reached
// through it: a select, an index or a narrowing of one.
func (ti *testInstance) rooted(e ir.Expr) bool {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Conversion:
			e = x.Operand
		case *ir.Ident:
			p, ok := x.Sym.(*ir.Param)
			return ok && ti.params[p]
		default:
			return false
		}
	}
}

// instanceValued reports whether e is a component value reached through the
// test's instance.
func (ti *testInstance) instanceValued(e ir.Expr) bool {
	if e == nil {
		return false
	}
	t := e.ExprType()
	// A ref read behind a conditional or a loop is an option or a list of
	// the component, and a local holding one holds the same ref.
	for t != nil && (t.Kind == ir.TypeOption || t.Kind == ir.TypeList) && len(t.Elems) == 1 {
		t = t.Elems[0]
	}
	return t != nil && t.Kind == ir.TypeComponent && ti.rooted(e)
}

// through is stmts with env substituted for the symbols it names, each local
// holding an instance replaced by what it holds, and each call handing one to
// a function inlined. inlining is the functions being inlined, outermost first.
func (ti *testInstance) through(stmts []ir.Stmt, env map[ir.Symbol]ir.Expr, inlining []*ir.Func) ([]ir.Stmt, error) {
	var bad error
	fail := func(err error) {
		if bad == nil {
			bad = err
		}
	}
	subst := func(e ir.Expr) ir.Expr {
		out, err := ir.RewriteExpr(e, func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok && id.Sym != nil {
				if to, ok := env[id.Sym]; ok {
					return ir.CloneExprSharingDecls(to), ir.SkipDir
				}
			}
			return n, nil
		})
		if err != nil {
			fail(err)
		}
		return out
	}
	aliases := map[*ir.LocalVar]bool{}
	aliasSyms := map[*ir.Var]bool{}
	for _, s := range stmts {
		_ = ir.WalkStmts([]ir.Stmt{s}, func(s ir.Stmt) error {
			lv, ok := s.(*ir.LocalVar)
			if !ok || lv.Sym == nil || lv.Init == nil {
				return nil
			}
			init := subst(lv.Init)
			if !ti.instanceValued(init) {
				return nil
			}
			// The local stands for its initializer at every read, so an
			// index that may change between the declaration and a read would
			// name another copy than the one the local was handed.
			if ix := variableIndex(init); ix != nil {
				fail(fmt.Errorf("%s: `%s` holds a copy of the test's instance chosen by an index that is not a constant; a test reaches one copy by indexing where it reads it", localPos(lv, ix), lv.Name))
				return nil
			}
			env[lv.Sym] = init
			aliases[lv] = true
			aliasSyms[lv.Sym] = true
			return nil
		})
	}
	if err := ti.refuseMovedRefLocals(stmts, aliases); err != nil {
		fail(err)
	}
	if len(aliases) > 0 {
		_ = ir.Walk(stmts, func(n ir.Node) error {
			var target ir.Expr
			switch x := n.(type) {
			case *ir.Assign:
				target = x.Target
			case *ir.Toggle:
				target = x.Target
			default:
				return nil
			}
			if id, ok := target.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok && aliasSyms[v] {
					fail(fmt.Errorf("%s: `%s` holds the test's instance and is assigned again; a test reaches its instance through one name", identPos(id), id.Name))
				}
			}
			return nil
		})
	}
	if len(env) > 0 {
		if err := ir.RewriteExprs(stmts, func(e ir.Expr) (ir.Expr, error) {
			if id, ok := e.(*ir.Ident); ok && id.Sym != nil {
				if to, ok := env[id.Sym]; ok {
					return ir.CloneExprSharingDecls(to), ir.SkipDir
				}
			}
			return e, nil
		}); err != nil {
			return nil, err
		}
	}
	out := ir.RewriteBlocks(stmts, func(list []ir.Stmt) []ir.Stmt {
		var res []ir.Stmt
		for _, s := range list {
			switch x := s.(type) {
			case *ir.LocalVar:
				if aliases[x] {
					continue
				}
			case *ir.CallStmt:
				if ti.handsInstance(x.Call) {
					body, err := ti.inlineStmt(x.Call, inlining)
					if err != nil {
						fail(err)
						break
					}
					res = append(res, &ir.If{Cond: &ir.Literal{Type: ir.TypBool, Value: "true"}, Body: body})
					continue
				}
			}
			res = append(res, s)
		}
		return res
	})
	if err := ir.RewriteExprs(out, func(e ir.Expr) (ir.Expr, error) {
		call, ok := e.(*ir.Call)
		if !ok || !ti.handsInstance(call) {
			return e, nil
		}
		v, err := ti.inlineValue(call, inlining)
		if err != nil {
			fail(err)
			return e, nil
		}
		return v, ir.SkipDir
	}); err != nil {
		return nil, err
	}
	return out, bad
}

// refuseMovedRefLocals refuses a local holding a ref a conditional or a loop
// wraps -- `var v = c.t`, an option or a list of the node -- read after
// something that may change what the ref names: a trigger, an event fired,
// a method called or a write. The local stands for the ref at every read,
// so it reads the nodes rendered then, where the interpreter's local keeps
// what it was handed.
func (ti *testInstance) refuseMovedRefLocals(stmts []ir.Stmt, aliases map[*ir.LocalVar]bool) error {
	wrapped := map[ir.Symbol]*ir.LocalVar{}
	for lv := range aliases {
		if t := lv.Init.ExprType(); t != nil && (t.Kind == ir.TypeOption || t.Kind == ir.TypeList) {
			wrapped[lv.Sym] = lv
		}
	}
	if len(wrapped) == 0 {
		return nil
	}
	m := &movedRefs{ti: ti, wrapped: wrapped, declared: map[ir.Symbol]bool{}, moved: map[ir.Symbol]bool{}}
	m.stmts(stmts)
	return m.bad
}

// movedRefs follows a test body in the order it runs: what a statement
// reads is read before what it does moves anything, and a loop whose body
// moves anything has moved it for every read in the body, the first
// iteration's excepted only by the order nothing tracks.
type movedRefs struct {
	ti       *testInstance
	wrapped  map[ir.Symbol]*ir.LocalVar
	declared map[ir.Symbol]bool
	moved    map[ir.Symbol]bool
	bad      error
}

func (m *movedRefs) stmts(list []ir.Stmt) {
	for _, s := range list {
		if m.bad != nil {
			return
		}
		switch x := s.(type) {
		case *ir.LocalVar:
			m.reads(x.Init)
			if m.wrapped[x.Sym] != nil {
				m.declared[x.Sym] = true
				m.moved[x.Sym] = false
			}
		case *ir.If:
			m.reads(x.Cond)
			m.stmts(x.Body)
			m.stmts(x.Else)
		case *ir.For:
			m.reads(x.Iter)
			if m.moves(x.Body) {
				m.moveAll()
			}
			m.stmts(x.Body)
			m.stmts(x.Else)
		default:
			m.reads(s)
			if m.moves(s) {
				m.moveAll()
			}
		}
	}
}

// reads refuses a read of a moved local in root.
func (m *movedRefs) reads(root any) {
	if root == nil || m.bad != nil {
		return
	}
	if e, ok := root.(ir.Expr); ok && e == nil {
		return
	}
	_ = ir.Walk(root, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if lv := m.wrapped[id.Sym]; lv != nil && m.moved[id.Sym] && m.bad == nil {
				m.bad = fmt.Errorf("%s: `%s` holds a ref read when it was declared, and a trigger, a call or a write since may have changed which nodes it names; read the ref again where it is wanted", identPos(id), lv.Name)
			}
		}
		return nil
	})
}

// moves reports whether root holds a trigger, an event fired, a method
// called or a write through the test's instance.
func (m *movedRefs) moves(root any) bool {
	found := false
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Assign:
			found = found || m.ti.rooted(x.Target)
		case *ir.Toggle:
			found = found || m.ti.rooted(x.Target)
		case *ir.Call:
			recv := x.Receiver
			if recv == nil && x.Func != nil && len(x.Func.Params) > 0 && x.Func.Params[0].Receiver && len(x.Args) > 0 {
				recv = x.Args[0].Value
			}
			found = found || recv != nil && m.ti.rooted(recv)
		}
		if found {
			return ir.SkipAll
		}
		return nil
	})
	return found
}

func (m *movedRefs) moveAll() {
	for sym := range m.declared {
		m.moved[sym] = true
	}
}

// handsInstance reports whether call hands the test's instance, or a
// component reached through it, to a function of the program.
func (ti *testInstance) handsInstance(call *ir.Call) bool {
	fn := call.Func
	if call.Event != "" || fn == nil || fn.IsTest || len(fn.Block) == 0 || fn.Intrinsic != "" || fn.Foreign.Name != "" || fn.Receiver != nil {
		return false
	}
	// An omitted argument is the parameter's default, which passCallDefaults
	// writes in later; the binding needs every argument now.
	fillDefaults(call)
	params, args := bindings(call)
	for i := range params {
		if ti.instanceValued(args[i]) {
			return true
		}
	}
	return false
}

// bind is the env a callee's body is read under: a parameter handed the
// instance is that reach. Inlined as statements, any other parameter is a
// temp the returned statements declare, holding its argument as the call
// evaluated it -- a body may change what its argument reads before it reads
// the parameter -- unless nothing can change it: a literal or a parameter of
// the caller's, where the body writes no parameter of that name. One the body
// never reads is dropped where its argument calls nothing, since a temp no
// statement reads does not compile on Go. Inlined as a value, the body is one
// expression that writes nothing, so an argument that calls nothing may stand
// for its parameter.
func (ti *testInstance) bind(call *ir.Call, inlining []*ir.Func, temps bool) (map[ir.Symbol]ir.Expr, []ir.Stmt, error) {
	fn := call.Func
	if slices.Contains(inlining, fn) {
		return nil, nil, fmt.Errorf("%s: `%s` is handed the test's instance and calls itself; a test inlines what it hands the instance to", calleePos(call), fn.Name)
	}
	params, args := bindings(call)
	read, written := paramUses(fn)
	env := map[ir.Symbol]ir.Expr{}
	var decls []ir.Stmt
	for i, p := range params {
		arg := args[i]
		switch {
		case ti.instanceValued(arg), !temps && duplicable(arg):
			env[p] = arg
		case temps && !written[p] && (isLiteral(arg) || isParamRead(arg)):
			env[p] = arg
		case temps && !read[p] && duplicable(arg):
		case temps:
			tmp := &ir.Var{Name: ti.names.next(p.Name, "arg"), Type: p.Type, Init: arg, Synthesized: true}
			decls = append(decls, &ir.LocalVar{Name: tmp.Name, Type: tmp.Type, Init: tmp.Init, Sym: tmp})
			env[p] = &ir.Ident{Name: tmp.Name, Type: tmp.Type, Sym: tmp}
		default:
			return nil, nil, fmt.Errorf("%s: `%s` is handed the test's instance, so it is inlined into the test, and its argument for %s may call nothing where its value is used", calleePos(call), fn.Name, p.Name)
		}
	}
	return env, decls, nil
}

// inlineStmt is fn's body where a statement calls it.
func (ti *testInstance) inlineStmt(call *ir.Call, inlining []*ir.Func) ([]ir.Stmt, error) {
	fn := call.Func
	env, decls, err := ti.bind(call, inlining, true)
	if err != nil {
		return nil, err
	}
	body := ir.CloneStmtsSharingDecls(fn.Block)
	if n := len(body); n > 0 {
		if r, ok := body[n-1].(*ir.Return); ok {
			body = body[:n-1]
			if c, ok := r.Value.(*ir.Call); ok {
				body = append(body, &ir.CallStmt{Call: c})
			}
		}
	}
	var returns bool
	_ = ir.Walk(body, func(n ir.Node) error {
		switch n.(type) {
		case *ir.Lambda:
			return ir.SkipDir
		case *ir.Return:
			returns = true
		}
		return nil
	})
	if returns {
		return nil, fmt.Errorf("%s: `%s` is handed the test's instance, so it is inlined into the test, and cannot return from the middle", calleePos(call), fn.Name)
	}
	ti.freshLocals(body, env)
	body, err = ti.through(body, env, append(inlining, fn))
	if err != nil {
		return nil, err
	}
	return append(decls, body...), nil
}

// inlineValue is the value of a call of fn in an expression, which fn's body
// has to be one `return` of.
func (ti *testInstance) inlineValue(call *ir.Call, inlining []*ir.Func) (ir.Expr, error) {
	fn := call.Func
	ret, ok := fn.Block[0].(*ir.Return)
	if len(fn.Block) != 1 || !ok || ret.Value == nil {
		return nil, fmt.Errorf("%s: `%s` is handed the test's instance, so it is inlined into the test, and is called for a value it does more than return", calleePos(call), fn.Name)
	}
	env, _, err := ti.bind(call, inlining, false)
	if err != nil {
		return nil, err
	}
	stmts, err := ti.through([]ir.Stmt{&ir.Return{Value: ir.CloneExprSharingDecls(ret.Value)}}, env, append(inlining, fn))
	if err != nil {
		return nil, err
	}
	return stmts[0].(*ir.Return).Value, nil
}

// duplicable reports whether e may be read once per use in place of a temp
// holding it: it calls nothing, so within the one expression a value-position
// call is inlined into, every read of it reads the same value.
func duplicable(e ir.Expr) bool {
	ok := true
	_ = ir.Walk(e, func(n ir.Node) error {
		switch n.(type) {
		case *ir.Call, *ir.Lambda:
			ok = false
			return ir.SkipAll
		}
		return nil
	})
	return ok
}

// calleePos is where the call's callee is written.
func calleePos(c *ir.Call) string {
	if c.AST != nil && c.AST.Func != nil {
		if p := c.AST.Func.ExprPos(); p != nil && p.IsValid() {
			return p.String()
		}
	}
	return handleCallPos(c)
}

// freshLocals gives each local and loop variable an inlined body declares a
// name of its own, and env the reads of it: the body lands in the test's
// scope, where a second inlining, or a local of the test's, may hold the
// name it was written with.
func (ti *testInstance) freshLocals(body []ir.Stmt, env map[ir.Symbol]ir.Expr) {
	fresh := func(old ir.Symbol, name string, typ *ir.Type, sym ir.Symbol) string {
		n := ti.names.next(name, "local")
		env[old] = &ir.Ident{Name: n, Type: typ, Sym: sym}
		return n
	}
	_ = ir.Walk(body, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Lambda:
			return ir.SkipDir
		case *ir.LocalVar:
			if x.Sym != nil {
				v := *x.Sym
				v.Name = fresh(x.Sym, x.Sym.Name, x.Sym.Type, &v)
				x.Sym, x.Name = &v, v.Name
			}
		case *ir.For:
			if x.KeySym != nil {
				k := *x.KeySym
				k.Name = fresh(x.KeySym, k.Name, k.Type, &k)
				x.KeySym, x.Key = &k, k.Name
			}
			if x.ValueSym != nil {
				v := *x.ValueSym
				v.Name = fresh(x.ValueSym, v.Name, v.Type, &v)
				x.ValueSym, x.Value = &v, v.Name
			}
		}
		return nil
	})
}

// variableIndex is the first index in a select chain that is not a literal.
func variableIndex(e ir.Expr) ir.Expr {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Conversion:
			e = x.Operand
		case *ir.Index:
			if !isLiteral(x.Idx) {
				return x.Idx
			}
			e = x.Operand
		default:
			return nil
		}
	}
}

func isLiteral(e ir.Expr) bool {
	_, ok := e.(*ir.Literal)
	return ok
}

func localPos(lv *ir.LocalVar, at ir.Expr) string {
	if lv.AST != nil {
		return lv.AST.Pos.String()
	}
	if lv.Sym != nil {
		if vs, ok := lv.Sym.AST.(*ast.VarStmt); ok {
			return vs.Pos.String()
		}
	}
	return exprPos(at)
}

// paramUses is each parameter of fn its body reads, and each it assigns.
func paramUses(fn *ir.Func) (read, written map[*ir.Param]bool) {
	read, written = map[*ir.Param]bool{}, map[*ir.Param]bool{}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		var target ir.Expr
		switch x := n.(type) {
		case *ir.Ident:
			if p, ok := x.Sym.(*ir.Param); ok {
				read[p] = true
			}
		case *ir.Assign:
			target = x.Target
		case *ir.Toggle:
			target = x.Target
		}
		if id, ok := target.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok {
				written[p] = true
			}
		}
		return nil
	})
	return read, written
}

func isParamRead(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	_, ok = id.Sym.(*ir.Param)
	return ok
}
