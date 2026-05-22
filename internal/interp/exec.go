package interp

import (
	"fmt"
	"math"

	"git.duckfam.us/jonathan/sngl/ast"
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
// handler's param (or "e" if unnamed). Propagates any error raised by the
// handler body itself to the caller.
func (env *Env) invokeHandler(handler *ir.EventHandler, event map[string]any) error {
	if handler == nil || handler.Func == nil {
		return nil
	}
	paramName := "e"
	if len(handler.Func.Params) > 0 {
		paramName = handler.Func.Params[0].Name
	}
	saved, existed := env.Vars[paramName]
	env.Vars[paramName] = event
	defer func() {
		if existed {
			env.Vars[paramName] = saved
		} else {
			delete(env.Vars, paramName)
		}
	}()
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
			env.Vars[n.Name] = nil
			return nil
		}
		v, err := env.Eval(n.Init)
		if err != nil {
			return err
		}
		env.Vars[n.Name] = v
		return nil
	case *ir.If:
		return env.execIf(n)
	case *ir.For:
		return env.execFor(n)
	case *ir.Return:
		return nil // caller handles return bodies
	case *ir.PlatformFilter:
		if n.Platform == "" || n.Platform == "none" {
			for _, st := range n.Body {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
		}
		return nil
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
		owner := env.findVarOwner(target.Name)
		if owner == nil {
			return fmt.Errorf("cannot assign to undefined variable %q", target.Name)
		}
		owner.Vars[target.Name] = ApplyOp(s.Op, owner.Vars[target.Name], val)
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(ComponentValue); ok {
			return cv.SetField(s.Op, target.Field, val)
		}
		if m, ok := obj.(map[string]any); ok {
			m[target.Field] = ApplyOp(s.Op, m[target.Field], val)
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
			list[i] = ApplyOp(s.Op, list[i], val)
			return nil
		}
		return fmt.Errorf("cannot index-assign to %T", obj)
	}
	return fmt.Errorf("invalid assignment target %T", s.Target)
}

func (env *Env) execToggle(s *ir.Toggle) error {
	switch target := s.Target.(type) {
	case *ir.Ident:
		owner := env.findVarOwner(target.Name)
		if owner == nil {
			return fmt.Errorf("cannot toggle undefined variable %q", target.Name)
		}
		b, ok := owner.Vars[target.Name].(bool)
		if !ok {
			return fmt.Errorf("cannot toggle non-bool variable %q", target.Name)
		}
		owner.Vars[target.Name] = !b
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
			env.Vars[s.Key] = k
			if s.Value != "" {
				env.Vars[s.Value] = val
			}
			for _, st := range s.Body {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
		}
		delete(env.Vars, s.Key)
		if s.Value != "" {
			delete(env.Vars, s.Value)
		}
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
			env.Vars[s.Key] = item
			if s.Value != "" {
				env.Vars[s.Value] = i
			}
			for _, st := range s.Body {
				if err := env.Exec(st); err != nil {
					return err
				}
			}
		}
		delete(env.Vars, s.Key)
		if s.Value != "" {
			delete(env.Vars, s.Value)
		}
	default:
		return fmt.Errorf("for iterator must be list or map, got %T", iter)
	}
	return nil
}

func ApplyOp(op ast.AssignOp, cur, val any) any {
	switch op {
	case ast.AssignSet:
		return val
	case ast.AssignAdd:
		if s, ok := cur.(string); ok {
			return s + fmt.Sprintf("%v", val)
		}
		return numericResult(toFloat(cur) + toFloat(val))
	case ast.AssignSub:
		return numericResult(toFloat(cur) - toFloat(val))
	case ast.AssignMul:
		return numericResult(toFloat(cur) * toFloat(val))
	case ast.AssignDiv:
		return numericResult(toFloat(cur) / toFloat(val))
	case ast.AssignMod:
		return numericResult(math.Mod(toFloat(cur), toFloat(val)))
	}
	return val
}
