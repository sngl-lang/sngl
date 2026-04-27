package testrunner

import (
	"fmt"
	"math"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// AssertError is returned when an assertion fails. Msg is the structured
// failure message produced by runAssert (named operands and their values
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
	saved, existed := env.vars[paramName]
	env.vars[paramName] = event
	defer func() {
		if existed {
			env.vars[paramName] = saved
		} else {
			delete(env.vars, paramName)
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
			env.vars[n.Name] = nil
			return nil
		}
		v, err := env.Eval(n.Init)
		if err != nil {
			return err
		}
		env.vars[n.Name] = v
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
	}
	return fmt.Errorf("cannot execute %T", s)
}

func (env *Env) execAssign(s *ir.Assign) error {
	val, err := env.Eval(s.Value)
	if err != nil {
		return err
	}
	switch target := s.Target.(type) {
	case *ir.Ident:
		cur, exists := env.vars[target.Name]
		if !exists {
			return fmt.Errorf("cannot assign to undefined variable %q", target.Name)
		}
		env.vars[target.Name] = applyOp(s.Op, cur, val)
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(*componentValue); ok {
			return cv.setField(s.Op, target.Field, val)
		}
		if m, ok := obj.(map[string]any); ok {
			m[target.Field] = applyOp(s.Op, m[target.Field], val)
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
			i := toInt(idx)
			if i < 0 || i >= len(list) {
				return fmt.Errorf("index %d out of range (len %d)", i, len(list))
			}
			list[i] = applyOp(s.Op, list[i], val)
			return nil
		}
		return fmt.Errorf("cannot index-assign to %T", obj)
	}
	return fmt.Errorf("invalid assignment target %T", s.Target)
}

func (env *Env) execToggle(s *ir.Toggle) error {
	switch target := s.Target.(type) {
	case *ir.Ident:
		cur, exists := env.vars[target.Name]
		if !exists {
			return fmt.Errorf("cannot toggle undefined variable %q", target.Name)
		}
		b, ok := cur.(bool)
		if !ok {
			return fmt.Errorf("cannot toggle non-bool variable %q", target.Name)
		}
		env.vars[target.Name] = !b
		return nil
	case *ir.Select:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(*componentValue); ok {
			cur, exists := cv.vars[target.Field]
			if !exists {
				return fmt.Errorf("cannot toggle undefined field %q", target.Field)
			}
			b, ok := cur.(bool)
			if !ok {
				return fmt.Errorf("cannot toggle non-bool field %q", target.Field)
			}
			cv.vars[target.Field] = !b
			if !cv.testParams[target.Field] {
				cv.env.vars[target.Field] = !b
			}
			return nil
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
	list, ok := iter.([]any)
	if !ok {
		return nil
	}
	if len(list) == 0 {
		for _, st := range s.Else {
			if err := env.Exec(st); err != nil {
				return err
			}
		}
		return nil
	}
	for i, item := range list {
		env.vars[s.Key] = item
		if s.Value != "" {
			env.vars[s.Value] = i
		}
		for _, st := range s.Body {
			if err := env.Exec(st); err != nil {
				return err
			}
		}
	}
	delete(env.vars, s.Key)
	if s.Value != "" {
		delete(env.vars, s.Value)
	}
	return nil
}

func applyOp(op ast.AssignOp, cur, val any) any {
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
