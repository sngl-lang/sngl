package testrunner

import (
	"fmt"
	"math"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// AssertError is returned when an assertion fails.
type AssertError struct {
	Expr ast.Expr // the expression that was asserted
	Got  any      // the value it evaluated to
}

func (e *AssertError) Error() string {
	return fmt.Sprintf("assert(%s) failed — got %v", parser.FormatExpr(e.Expr), e.Got)
}

// Exec executes a statement node, mutating the environment.
func (env *Env) Exec(s ast.Stmt) error {
	switch n := s.(type) {
	case *ast.AssignStmt:
		return env.execAssign(n)
	case *ast.ToggleStmt:
		return env.execToggle(n)
	case *ast.IncDecStmt:
		op := ast.AssignAdd
		if n.IsDec {
			op = ast.AssignSub
		}
		return env.execAssign(&ast.AssignStmt{
			Pos:    n.Pos,
			Target: n.Target,
			Op:     op,
			Value:  &ast.LiteralExpr{Pos: n.Pos, Kind: ast.LiteralInt, Raw: "1"},
		})
	case *ast.EmitStmt:
		// In headless mode, emissions are no-ops
		return nil
	case *ast.CallStmt:
		_, err := env.evalCall(n.Call)
		return err
	case *ast.VarStmt:
		v, err := env.Eval(n.Init)
		if err != nil {
			return err
		}
		env.vars[n.Name] = v
		return nil
	case *ast.VarDecl:
		for _, spec := range n.Specs {
			val := evalInit(env, spec.Default)
			for _, name := range spec.Names {
				env.vars[name] = val
			}
		}
		return nil
	case *ast.ReturnStmt:
		// ReturnStmt in exec context — should be handled by caller
		return nil
	default:
		// Try as expression
		if expr, ok := s.(ast.Expr); ok {
			_, err := env.Eval(expr)
			return err
		}
		return fmt.Errorf("cannot execute %T", s)
	}
}

// ExecBlock executes all statements in a StmtBlock.
func (env *Env) ExecBlock(b *ast.StmtBlock) error {
	for _, stmt := range b.Stmts {
		if err := env.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
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
		if cv, ok := obj.(*componentValue); ok {
			return cv.setField(s.Op, target.Field, val)
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
	if sel, ok := s.Target.(*ast.SelectExpr); ok {
		obj, err := env.Eval(sel.Operand)
		if err != nil {
			return err
		}
		if cv, ok := obj.(*componentValue); ok {
			cur, exists := cv.vars[sel.Field]
			if !exists {
				return fmt.Errorf("cannot toggle undefined field %q", sel.Field)
			}
			if b, ok := cur.(bool); ok {
				cv.vars[sel.Field] = !b
				if !cv.testParams[sel.Field] {
					cv.env.vars[sel.Field] = !b
				}
				return nil
			}
			return fmt.Errorf("cannot toggle non-bool field %q", sel.Field)
		}
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
