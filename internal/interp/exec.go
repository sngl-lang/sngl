package interp

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/opeval"
	"git.duckfam.us/jonathan/sngl/ir"
)

// AssertError is returned when an assertion fails. Msg is the structured
// failure message produced by RunAssert (named operands and their values
// for binary comparisons, list/string contains, etc.).
type AssertError struct {
	Expr ir.Expr // the expression that was asserted
	Got  any
	Msg  string
}

func (e *AssertError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("assert failed — got %v", e.Got)
}

// RaisedError is returned up the Go error chain by evalTypeMethodCall when
// a user calls error.raise. Callers that find a RaisedError in the return
// chain consult the originating ir.Call's ErrorMode to decide whether to
// invoke a resolved handler or propagate further up.
type RaisedError struct {
	Event map[string]any
	// from is the handler whose body let this raise out, so the render-tree
	// frames it is offered to start outside that handler rather than at it.
	from *ir.EventHandler
}

func (e *RaisedError) Error() string {
	if e == nil {
		return "raised error"
	}
	msg, _ := e.Event["message"].(string)
	kind, _ := e.Event["kind"].(string)
	if kind != "" {
		return fmt.Sprintf("raised: %s (%s)", msg, kind)
	}
	return fmt.Sprintf("raised: %s", msg)
}

// dispatchRaise routes a raise that came out of call through the mode the
// checker's effect analysis resolved for it. A per-call handler runs and the
// statement holding the call is over; a boundary's or window's handler runs
// and the event handler that made the call is over, which is a return. Any
// other mode passes the raise to the caller.
func (env *Env) dispatchRaise(call *ir.Call, raised *RaisedError) error {
	switch call.ErrorMode {
	case ir.ErrorPerCall:
		if call.ErrorHandler != nil {
			return env.invokeHandler(call.ErrorHandler, raised.Event)
		}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler != nil {
			if err := env.invokeHandler(call.ResolvedHandler, raised.Event); err != nil {
				return err
			}
			return &returnSignal{raised: true}
		}
	}
	return raised
}

// invokeHandler executes the handler body with the error bound to the
// handler's param. Propagates any error raised by the handler body itself to
// the caller.
//
// The body's own `return` ends the body alone. One that stands for a raise the
// body made and a handler further out caught is passed on, because that raise
// ends the event handler this one was invoked from too.
func (env *Env) invokeHandler(handler *ir.EventHandler, event map[string]any) error {
	if handler == nil || handler.Func == nil {
		return nil
	}
	// Before the body, so a handler that raises again leaves the fallback
	// showing rather than the content that failed. passBoundaryFailed orders
	// its flag the same way, for the same reason.
	env.markCaught(handler)
	// A handler with no declared parameter has nothing that can name the
	// event, so there is nothing to bind.
	if len(handler.Func.Params) > 0 {
		p := handler.Func.Params[0]
		saved, existed := env.vals[p]
		env.Set(p, event)
		defer func() {
			if existed {
				env.vals[p] = saved
			} else {
				delete(env.vals, p)
			}
		}()
	}
	for _, stmt := range handler.Func.Block {
		err := env.Exec(stmt)
		if ret, ok := err.(*returnSignal); ok && !ret.raised {
			return nil
		}
		if raised, ok := err.(*RaisedError); ok {
			raised.from = handler
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// raiseScope is the key, among the context values a node is mounted under,
// that holds the boundaries and windows around it, nearest first. The checker
// resolves a raise within the component it was written in; one that escapes
// that is the render tree's, and which instance of the component raised is
// only known here.
var raiseScope = &ir.Context{Name: "__raise"}

type raiseFrame struct {
	handler *ir.EventHandler
	// env is the scope that rendered the boundary, where its handler runs.
	env *Env
}

// pushRaiseScope makes h the nearest frame for what is mounted until the
// returned func restores the previous ones.
func (env *Env) pushRaiseScope(h *ir.EventHandler) func() {
	prev, had := env.ContextVals[raiseScope]
	frames, _ := prev.([]raiseFrame)
	env.SetContext(raiseScope, append([]raiseFrame{{handler: h, env: env}}, frames...))
	return func() {
		if had {
			env.ContextVals[raiseScope] = prev
		} else {
			delete(env.ContextVals, raiseScope)
		}
	}
}

// catchEscaped offers a raise that left an event handler to the frames it was
// mounted under, starting outside the handler that let it out.
func catchEscaped(vals map[*ir.Context]any, err error) error {
	raised, ok := err.(*RaisedError)
	if !ok {
		return err
	}
	frames, _ := vals[raiseScope].([]raiseFrame)
	i := 0
	if raised.from != nil {
		for j, f := range frames {
			if f.handler == raised.from {
				i = j + 1
				break
			}
		}
	}
	for ; i < len(frames); i++ {
		err := frames[i].env.invokeHandler(frames[i].handler, raised.Event)
		next, ok := err.(*RaisedError)
		if !ok {
			if IsReturn(err) {
				return nil
			}
			return err
		}
		raised = next
	}
	return raised
}

// returnSignal carries a `return` out of the blocks it was written inside.
// Exec reports what happened to a statement as an error, so a return travels
// that same channel: execIf and execFor pass one up without looking at it, and
// whoever is running the function body catches it. Reading a Return only where
// it sits at the top of a body — which is what the interpreter used to do — let
// a guard clause fall through to the statement after its `if`.
//
// raised marks the return a caught raise stands for; see dispatchRaise.
type returnSignal struct {
	value  any
	raised bool
}

func (*returnSignal) Error() string { return "return outside a function body" }

// IsReturn reports whether an Exec error is a `return` looking for its
// function body rather than a failure.
func IsReturn(err error) bool {
	_, ok := err.(*returnSignal)
	return ok
}

// breakSignal and continueSignal travel the same channel a return does, and
// for the same reason: the statement that has to act on them is the loop, and
// what is between the two is an arbitrary nest of ifs. runLoopBody catches
// both, for every loop shape; nothing else looks at them, so one reaching a
// function body is a loop escape the checker should have refused.
type breakSignal struct{}

func (*breakSignal) Error() string { return "break outside a loop" }

type continueSignal struct{}

func (*continueSignal) Error() string { return "continue outside a loop" }

// ExecBlock runs a statement stream that is not a function body — a test body,
// an event handler — stopping at a `return` rather than reporting one.
func (env *Env) ExecBlock(block []ir.Stmt) error {
	for _, stmt := range block {
		err := env.Exec(stmt)
		if _, ok := err.(*returnSignal); ok {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Exec executes an IR statement, mutating the environment.
func (env *Env) Exec(s ir.Stmt) error {
	if groups := ir.StatementSpreads(s); len(groups) > 0 {
		saved := env.spreadVals
		defer func() { env.spreadVals = saved }()
		env.spreadVals = make(map[int]any, len(groups))
		for _, g := range groups {
			v, err := env.Eval(g.Operand)
			if err != nil {
				return err
			}
			env.spreadVals[g.ID] = v
		}
	}
	return env.exec(s)
}

func (env *Env) exec(s ir.Stmt) error {
	switch n := s.(type) {
	case *ir.Assign:
		return env.execAssign(n)
	case *ir.Toggle:
		return env.execToggle(n)
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
		}
		_, err := env.evalCall(n.Call)
		return err
	case *ir.Emit:
		return env.emit(n)
	case *ir.LocalVar:
		if n.Init == nil {
			env.Set(n.Sym, nil)
			return nil
		}
		v, err := env.Eval(n.Init)
		if err != nil {
			return err
		}
		env.Set(n.Sym, CopyValue(v))
		return nil
	case *ir.If:
		return env.execIf(n)
	case *ir.For:
		return env.execFor(n)
	case *ir.Break:
		return &breakSignal{}
	case *ir.Continue:
		return &continueSignal{}
	case *ir.Return:
		if n.Value == nil {
			return &returnSignal{}
		}
		v, err := env.Eval(n.Value)
		if err != nil {
			return err
		}
		return &returnSignal{value: v}
	case *ir.NodeInst:
		// Visual nodes don't execute in statement position in the headless
		// interpreter (they're rendered elsewhere). Skipping preserves
		// forward-compat with boundary/window structures appearing as
		// statements inside handler-adjacent scopes.
		return nil
	case *ir.ErrorBoundary:
		return nil
	case *ir.ContextProvider:
		restore, err := env.pushContext(n.Ref, n.Value)
		defer restore()
		if err != nil {
			return err
		}
		for _, child := range n.Children {
			if err := env.Exec(child); err != nil {
				return err
			}
		}
		return nil
	default:
		panic(fmt.Sprintf("testrunner.Exec: unhandled ir.Stmt %T", s))
	}
}

func (env *Env) execAssign(s *ir.Assign) error {
	val, err := env.Eval(s.Value)
	if err != nil {
		return err
	}
	switch target := s.Target.(type) {
	case *ir.Ident:
		owner := env.findVarOwner(target.Sym)
		if owner == nil {
			return fmt.Errorf("cannot assign to undefined variable %q", target.Name)
		}
		nv, err := ApplyOp(s.Op, owner.vals[target.Sym], val, target.ExprType())
		if err != nil {
			return err
		}
		owner.vals[target.Sym] = nv
		owner.noteAssigned(target.Sym)
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(ComponentValue); ok {
			return cv.SetField(s.Op, target.Field, val)
		}
		if st, ok := obj.(*Struct); ok {
			old, _ := st.Get(target.Field)
			nv, err := ApplyOp(s.Op, old, val, target.ExprType())
			if err != nil {
				return err
			}
			st.Set(target.Field, nv)
			return nil
		}
		if m, ok := obj.(map[string]any); ok {
			nv, err := ApplyOp(s.Op, m[target.Field], val, target.ExprType())
			if err != nil {
				return err
			}
			if err := writeThroughHandle(m, target.Field, nv); err != nil {
				return err
			}
			m[target.Field] = nv
			return nil
		}
		return fmt.Errorf("cannot assign to field on %T", obj)
	case *ir.Index:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		idx, err := env.Eval(target.Idx)
		if err != nil {
			return err
		}
		if list, ok := obj.([]any); ok {
			i := ToInt(idx)
			if i < 0 || i >= len(list) {
				return fmt.Errorf("index %d out of range (len %d)", i, len(list))
			}
			nv, err := ApplyOp(s.Op, list[i], val, target.ExprType())
			if err != nil {
				return err
			}
			list[i] = nv
			return nil
		}
		if m, ok := obj.(map[string]any); ok {
			key := fmt.Sprintf("%v", idx)
			old, present := m[key]
			if !present {
				old = zeroValueFor(target.ExprType())
			}
			nv, err := ApplyOp(s.Op, old, val, target.ExprType())
			if err != nil {
				return err
			}
			m[key] = nv
			return nil
		}
		return fmt.Errorf("cannot index-assign to %T", obj)
	case *ir.Unary:
		// `*n = val` — whole-element write through an &-bound loop element
		// (`for var &n = list { n = … }`). The operand is a listRef; write back.
		if target.Op == ast.UnaryDeref {
			obj, err := env.Eval(target.Operand)
			if err != nil {
				return err
			}
			if ref, ok := obj.(*listRef); ok {
				nv, err := ApplyOp(s.Op, ref.get(), val, target.ExprType())
				if err != nil {
					return err
				}
				ref.set(nv)
				return nil
			}
			return fmt.Errorf("cannot deref-assign to %T", obj)
		}
	}
	return fmt.Errorf("invalid assignment target %T", s.Target)
}

func (env *Env) execToggle(s *ir.Toggle) error {
	switch target := s.Target.(type) {
	case *ir.Ident:
		owner := env.findVarOwner(target.Sym)
		if owner == nil {
			return fmt.Errorf("cannot toggle undefined variable %q", target.Name)
		}
		b, ok := owner.vals[target.Sym].(bool)
		if !ok {
			return fmt.Errorf("cannot toggle non-bool variable %q", target.Name)
		}
		owner.vals[target.Sym] = !b
		owner.noteAssigned(target.Sym)
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(ComponentValue); ok {
			return cv.Toggle(target.Field)
		}
		// A two-way prop through a node's handle: what it holds now, written
		// back negated the way an assignment through the handle is.
		if _, ok := obj.(map[string]any); ok {
			cur, err := env.Eval(target)
			if err != nil {
				return err
			}
			b, ok := cur.(bool)
			if !ok {
				return fmt.Errorf("cannot toggle non-bool %s", target.Field)
			}
			return env.execAssign(&ir.Assign{Target: target, Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(!b)}})
		}
	}
	return fmt.Errorf("invalid toggle target %T", s.Target)
}

func (env *Env) execIf(s *ir.If) error {
	if s.Catch != nil {
		return env.execCatch(s)
	}
	cond, err := env.Eval(s.Cond)
	if err != nil {
		return err
	}
	b, _ := cond.(bool)
	body := s.Body
	if !b {
		body = s.Else
	}
	for _, st := range body {
		if err := env.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

// maxLoopIterations bounds a condition or forever loop the interpreter runs.
//
// A compiled target has an operating system to stop it; a fixture does not.
// `sngl test` runs its fixtures in-process, so one loop that never terminates
// hangs the whole suite with no output saying which fixture did it -- which is
// why the bound is an error naming the loop rather than a silent stop. It is
// far above any loop a fixture has reason to run.
const maxLoopIterations = 10_000_000

func (env *Env) execFor(s *ir.For) error {
	// No iterable is the forever loop, and a bool one is a condition: both
	// walk nothing, so neither reaches the iteration below.
	if s.Iter == nil {
		return env.execLoop(s, nil)
	}
	if t := s.Iter.ExprType(); t != nil && t.Kind == ir.TypeBool {
		return env.execLoop(s, s.Iter)
	}
	iter, err := env.Eval(s.Iter)
	if err != nil {
		return err
	}
	switch v := iter.(type) {
	case map[string]any:
		if len(v) == 0 {
			return env.execStmts(s.Else)
		}
		for k, val := range v {
			env.Set(s.KeySym, k)
			env.Set(s.ValueSym, val)
			done, err := env.runLoopBody(s)
			if err != nil {
				return err
			}
			if done {
				break
			}
		}
		env.unbindLoopVars(s)
	case *Stream:
		return env.execStreamLoop(s, v)
	default:
		// A list, or the sequence sngl:seq computes -- an iter<T> is whichever
		// of the two produced it, and neither is walked by building the other.
		n, at, isIterable := asIterable(iter)
		if !isIterable {
			return fmt.Errorf("for iterator must be list or map, got %T", iter)
		}
		if n == 0 {
			return env.execStmts(s.Else)
		}
		list, _ := iter.([]any)
		for i := range n {
			bindLoopElem(env, s, i, at(i), list)
			done, err := env.runLoopBody(s)
			if err != nil {
				return err
			}
			if done {
				break
			}
		}
		env.unbindLoopVars(s)
	}
	return nil
}

// execLoop runs the two loops that iterate nothing: cond is the head to test
// before each iteration, or nil for a loop with no head, which runs until its
// body breaks or returns.
func (env *Env) execLoop(s *ir.For, cond ir.Expr) error {
	limit := env.maxIterations
	if limit == 0 {
		limit = maxLoopIterations
	}
	for i := 0; ; i++ {
		if i >= limit {
			pos := ""
			if s.AST != nil {
				pos = s.AST.Pos.String() + ": "
			}
			return fmt.Errorf("%sloop ran %d iterations without terminating", pos, i)
		}
		if cond != nil {
			v, err := env.Eval(cond)
			if err != nil {
				return err
			}
			ok, isBool := v.(bool)
			if !isBool {
				return fmt.Errorf("for condition must be bool, got %T", v)
			}
			if !ok {
				// The body never ran: that is what the else case is.
				if i == 0 {
					return env.execStmts(s.Else)
				}
				return nil
			}
		}
		done, err := env.runLoopBody(s)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// runLoopBody runs one iteration, reporting whether the loop should stop.
// A `continue` ends the iteration and a `break` ends the loop; anything else
// -- a failure, or a `return` looking for its function body -- travels on.
func (env *Env) runLoopBody(s *ir.For) (done bool, err error) {
	for _, st := range s.Body {
		switch err := env.Exec(st); err.(type) {
		case nil:
		case *continueSignal:
			return false, nil
		case *breakSignal:
			return true, nil
		default:
			return false, err
		}
	}
	return false, nil
}

// execStmts runs a statement list, passing every signal up: it is the
// interpreter's plain block, used where a block is not a loop body.
func (env *Env) execStmts(stmts []ir.Stmt) error {
	for _, st := range stmts {
		if err := env.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

// bindLoopElem binds one iteration's variables: the element, and the index
// beside it in the two-variable form.
//
// Which is which is the checker's answer -- Key is the index and Value the
// element once a second variable is written (`for var i, x = xs`), and Key is
// the element on its own. Both loop sites had it the other way round, so a
// two-variable loop bound the element to the index name and every compiled
// backend disagreed with the interpreter about the same program.
//
// list is the backing slice when the iterable is one, and nil otherwise; only
// a list can carry a &-bound element, since only a list has an element to
// write back to.
func bindLoopElem(env *Env, s *ir.For, i int, item any, list []any) {
	elemSym, idxSym := s.KeySym, s.ValueSym
	if s.Value != "" {
		elemSym, idxSym = s.ValueSym, s.KeySym
	}
	if s.RefElem && list != nil {
		// &-bound element: bind a listRef so field/whole-element writes
		// (through the checker's Unary{Deref}) update the list in place.
		env.Set(elemSym, &listRef{list: list, idx: i})
	} else {
		env.Set(elemSym, item)
	}
	env.Set(idxSym, i)
}

// unbindLoopVars drops the loop's bindings once the loop is done, so a read
// after the loop resolves the same way it did before it.
func (env *Env) unbindLoopVars(s *ir.For) {
	if s.KeySym != nil {
		delete(env.vals, s.KeySym)
	}
	if s.ValueSym != nil {
		delete(env.vals, s.ValueSym)
	}
}

func ApplyOp(op ast.AssignOp, cur, val any, targetType *ir.Type) (any, error) {
	// The declared target type drives the width so sized-integer compound
	// assignment wraps at the right width. When the target type is unavailable
	// or dyn (e.g. a dynamically-typed field in a test context), fall back to
	// inferring from the current runtime carrier.
	kind := numKindOf(targetType)
	if kind == (opeval.NumKind{}) {
		kind = numKindOfValue(cur)
	}
	switch op {
	case ast.AssignSet:
		return val, nil
	case ast.AssignAdd:
		if s, ok := cur.(string); ok {
			return s + fmt.Sprintf("%v", val), nil
		}
		return opeval.Arith(ast.BinAdd, cur, val, kind)
	case ast.AssignSub:
		return opeval.Arith(ast.BinSub, cur, val, kind)
	case ast.AssignMul:
		return opeval.Arith(ast.BinMul, cur, val, kind)
	case ast.AssignDiv:
		return opeval.Arith(ast.BinDiv, cur, val, kind)
	case ast.AssignMod:
		return opeval.Arith(ast.BinMod, cur, val, kind)
	}
	return val, nil
}

// numKindOfValue infers an opeval width descriptor from a runtime value's Go
// carrier type. Only used where the static IR type is unavailable (compound
// assignment).
func numKindOfValue(v any) opeval.NumKind {
	switch v.(type) {
	case uint64:
		return opeval.NumKind{Bits: 64, Unsigned: true}
	case float64:
		return opeval.NumKind{Float: true}
	}
	return opeval.NumKind{}
}

// execCatch runs a catch block. Only lowered IR holds one -- `sngl test` runs
// checked IR -- so this answers a caller that interprets after lowering, as the
// build-time Fold does (optimize.NewFold).
func (env *Env) execCatch(s *ir.If) error {
	for _, st := range s.Body {
		err := env.Exec(st)
		if raised, ok := err.(*RaisedError); ok {
			return env.invokeHandler(s.Catch, raised.Event)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// writeThroughHandle writes a two-way prop of the node m is a handle to --
// `w.visible = true` in `func window.open(w window)` -- where a write of the
// prop lands: the var the call site bound, in the scope the node was written
// in, or the instance's own cell where nothing is bound. A write of anything
// else stays the handle's.
func writeThroughHandle(m map[string]any, field string, val any) error {
	inst, _ := m["__inst"].(*ir.NodeInst)
	if inst == nil || inst.Component == nil {
		return nil
	}
	var prop *ir.Prop
	for _, p := range inst.Component.Props {
		if p.Name == field && p.Bidirectional {
			prop = p
		}
	}
	if prop == nil {
		return nil
	}
	for _, b := range inst.Bindings {
		if b.PropName != field {
			continue
		}
		owner, _ := m["__ownerEnv"].(*Env)
		if owner == nil {
			return nil
		}
		tmp := &ir.Param{Name: "__bound"}
		owner.Set(tmp, val)
		err := owner.execAssign(&ir.Assign{Target: b.Target, Op: ast.AssignSet, Value: &ir.Ident{Name: tmp.Name, Sym: tmp}})
		delete(owner.vals, tmp)
		return err
	}
	if ce, _ := m["__compEnv"].(*Env); ce != nil && prop.Sym != nil {
		ce.Set(prop.Sym, val)
	}
	return nil
}
