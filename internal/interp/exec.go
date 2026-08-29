package interp

import (
	"fmt"

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

// dispatchRaise routes a RaisedError through the error-handling modes set
// by the checker's effect analysis. Returns nil to consume the error
// (handler was invoked); returns the error to propagate upward.
func (env *Env) dispatchRaise(call *ir.Call, raised *RaisedError) error {
	if raised == nil || call == nil {
		return nil
	}
	switch call.ErrorMode {
	case ir.ErrorPerCall:
		if call.ErrorHandler != nil {
			return env.invokeHandler(call.ErrorHandler, raised.Event)
		}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler != nil {
			return env.invokeHandler(call.ResolvedHandler, raised.Event)
		}
	}
	return raised
}

// invokeHandler executes the handler body with the ErrorEvent bound to the
// handler's param. Propagates any error raised by the handler body itself to
// the caller.
func (env *Env) invokeHandler(handler *ir.EventHandler, event map[string]any) error {
	if handler == nil || handler.Func == nil {
		return nil
	}
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
		if err := env.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Exec executes an IR statement, mutating the environment.
func (env *Env) Exec(s ir.Stmt) error {
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
		if raised, ok := err.(*RaisedError); ok {
			return env.dispatchRaise(n.Call, raised)
		}
		return err
	case *ir.Emit:
		return nil // no-op in headless tests
	case *ir.LocalVar:
		if n.Init == nil {
			env.Set(n.Sym, nil)
			return nil
		}
		v, err := env.Eval(n.Init)
		if err != nil {
			return err
		}
		env.Set(n.Sym, v)
		return nil
	case *ir.If:
		return env.execIf(n)
	case *ir.For:
		return env.execFor(n)
	case *ir.Return:
		return nil // caller handles return bodies
	case *ir.NodeInst:
		// Visual nodes don't execute in statement position in the headless
		// interpreter (they're rendered elsewhere). Skipping preserves
		// forward-compat with boundary/window structures appearing as
		// statements inside handler-adjacent scopes.
		return nil
	case *ir.ErrorBoundary:
		return nil
	case *ir.SlotInst:
		return nil
	case *ir.Window:
		// Window is a top-level construct; reaching it inside a
		// statement stream means something nested it incorrectly.
		panic(fmt.Sprintf("testrunner.Exec: unexpected nested Window: %#v", n))
	case *ir.ContextProvider:
		panic(fmt.Sprintf("testrunner.Exec: ContextProvider should be lowered before exec: %#v", n))
	case *ir.CanvasRedrawStmt:
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
		return fmt.Errorf("cannot index-assign to %T", obj)
	case *ir.Unary:
		// `*n = val` — whole-element write through an &-bound loop element
		// (`for &n = list { n = … }`). The operand is a listRef; write back.
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
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(ComponentValue); ok {
			return cv.Toggle(target.Field)
		}
	}
	return fmt.Errorf("invalid toggle target %T", s.Target)
}

func (env *Env) execIf(s *ir.If) error {
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

func (env *Env) execFor(s *ir.For) error {
	iter, err := env.Eval(s.Iter)
	if err != nil {
		return err
	}
	switch v := iter.(type) {
	case map[string]any:
		if len(v) == 0 {
			for _, st := range s.Else {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
			return nil
		}
		for k, val := range v {
			env.Set(s.KeySym, k)
			env.Set(s.ValueSym, val)
			for _, st := range s.Body {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
		}
		env.unbindLoopVars(s)
	case []any:
		// iter<T> at runtime is also []any (list passed as iter has no runtime wrapper).
		if len(v) == 0 {
			for _, st := range s.Else {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
			return nil
		}
		for i, item := range v {
			if s.RefElem {
				// &-bound element: bind a listRef so field/whole-element writes
				// (through the checker's Unary{Deref}) update the list in place.
				env.Set(s.KeySym, &listRef{list: v, idx: i})
			} else {
				env.Set(s.KeySym, item)
			}
			env.Set(s.ValueSym, i)
			for _, st := range s.Body {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
		}
		env.unbindLoopVars(s)
	default:
		return fmt.Errorf("for iterator must be list or map, got %T", iter)
	}
	return nil
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
