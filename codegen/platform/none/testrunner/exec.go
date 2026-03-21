package testrunner

import (
	"fmt"
	"math"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// AssertError is returned when an assertion fails.
type AssertError struct {
	Expr ast.Node // the expression that was asserted
	Got  any      // the value it evaluated to
}

func (e *AssertError) Error() string {
	return fmt.Sprintf("assert(%s) failed — got %v", snglparser.FormatNode(e.Expr), e.Got)
}

// Exec executes a statement node, mutating the environment.
func (env *Env) Exec(n ast.Node) error {
	switch s := n.(type) {
	case *ast.AssignStmt:
		return env.execAssign(s)
	case *ast.ToggleStmt:
		return env.execToggle(s)
	case *ast.EmitStmt:
		// In headless mode, emissions are no-ops
		return nil
	case *ast.CallExpr:
		_, err := env.evalCall(s)
		return err
	case *ast.CallStmt:
		_, err := env.evalCall(s.Call)
		return err
	case *ast.VarStmt:
		v, err := env.Eval(s.Init)
		if err != nil {
			return err
		}
		env.vars[s.Name] = v
		return nil
	case *ast.StmtBlock:
		for _, stmt := range s.Stmts {
			if err := env.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	default:
		// Expression statement — evaluate and discard
		_, err := env.Eval(n)
		return err
	}
}

func (env *Env) execAssign(s *ast.AssignStmt) error {
	val, err := env.Eval(s.Value)
	if err != nil {
		return err
	}

	switch target := s.Target.(type) {
	case *ast.IdentExpr:
		cur, exists := env.vars[target.Name]
		if !exists {
			return fmt.Errorf("cannot assign to undefined variable %q", target.Name)
		}
		env.vars[target.Name] = applyOp(s.Op, cur, val)
		return nil
	case *ast.SelectExpr:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		if m, ok := obj.(map[string]any); ok {
			m[target.Field] = applyOp(s.Op, m[target.Field], val)
			return nil
		}
		return fmt.Errorf("cannot assign to field on %T", obj)
	case *ast.IndexExpr:
		obj, err := env.Eval(target.Operand)
		if err != nil {
			return err
		}
		idx, err := env.Eval(target.Index)
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
	default:
		return fmt.Errorf("invalid assignment target %T", s.Target)
	}
}

func (env *Env) execToggle(s *ast.ToggleStmt) error {
	if ident, ok := s.Target.(*ast.IdentExpr); ok {
		cur, exists := env.vars[ident.Name]
		if !exists {
			return fmt.Errorf("cannot toggle undefined variable %q", ident.Name)
		}
		if b, ok := cur.(bool); ok {
			env.vars[ident.Name] = !b
			return nil
		}
		return fmt.Errorf("cannot toggle non-bool variable %q", ident.Name)
	}
	return fmt.Errorf("invalid toggle target %T", s.Target)
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
