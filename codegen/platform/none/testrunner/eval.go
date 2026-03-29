package testrunner

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// maxCallDepth is the maximum allowed function call depth.
const maxCallDepth = 100

// Env holds the mutable state for test execution.
type Env struct {
	vars      map[string]any
	computeds map[string]ast.Expr
	consts    map[string]any
	funcs     map[string]*ast.FuncDef
	units     map[string]*ast.UnitTable // suffix → unit table
	timers    []*ast.Timer
	doc       *ast.Document
	body      []*ast.VisualNode
	depth     int // current call stack depth
}

func NewEnv() *Env {
	return &Env{
		vars:      map[string]any{},
		computeds: map[string]ast.Expr{},
		consts:    map[string]any{},
		funcs:     map[string]*ast.FuncDef{},
	}
}

// SetFunc registers a user-defined function in the environment.
func (env *Env) SetFunc(fn *ast.FuncDef) {
	env.funcs[fn.Name] = fn
}

// SetVar sets a variable in the environment.
func (env *Env) SetVar(name string, val any) {
	env.vars[name] = val
}

// Snapshot returns a shallow copy of the env for subtest isolation.
func (env *Env) Snapshot() *Env {
	cp := &Env{
		vars:      make(map[string]any, len(env.vars)),
		computeds: env.computeds,
		consts:    env.consts,
		funcs:     env.funcs,
		units:     env.units,
		doc:       env.doc,
		body:      env.body,
		depth:     env.depth,
	}
	maps.Copy(cp.vars, env.vars)
	return cp
}

// Eval evaluates an AST node and returns its value.
func (env *Env) Eval(n ast.Node) (any, error) {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		if e.Kind == ast.LiteralUnit {
			return env.evalUnitLiteral(e.Value.(ast.UnitLiteral))
		}
		if e.Kind == ast.LiteralColor {
			return colorHexToStruct(fmt.Sprintf("%v", e.Value)), nil
		}
		return e.Value, nil
	case *ast.IdentExpr:
		return env.lookup(e.Name)
	case *ast.BinaryExpr:
		return env.evalBinary(e)
	case *ast.UnaryExpr:
		return env.evalUnary(e)
	case *ast.TernaryExpr:
		cond, err := env.Eval(e.Cond)
		if err != nil {
			return nil, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.Eval(e.Then)
		}
		return env.Eval(e.Else)
	case *ast.SelectExpr:
		obj, err := env.Eval(e.Operand)
		if err != nil {
			return nil, err
		}
		if m, ok := obj.(map[string]any); ok {
			return m[e.Field], nil
		}
		return nil, fmt.Errorf("cannot select field %q on %T", e.Field, obj)
	case *ast.IndexExpr:
		obj, err := env.Eval(e.Operand)
		if err != nil {
			return nil, err
		}
		idx, err := env.Eval(e.Index)
		if err != nil {
			return nil, err
		}
		if list, ok := obj.([]any); ok {
			i := toInt(idx)
			if i < 0 || i >= len(list) {
				return nil, fmt.Errorf("index %d out of range (len %d)", i, len(list))
			}
			return list[i], nil
		}
		return nil, fmt.Errorf("cannot index %T", obj)
	case *ast.ElementRefExpr:
		return env.resolveElementRef(e.Name)
	case *ast.CallExpr:
		return env.evalCall(e)
	case *ast.MethodExpr:
		return env.evalMethod(e)
	case *ast.InterpolationExpr:
		var sb strings.Builder
		for _, p := range e.Parts {
			v, err := env.Eval(p)
			if err != nil {
				return nil, err
			}
			sb.WriteString(fmt.Sprintf("%v", v))
		}
		return sb.String(), nil
	case *ast.ListExpr:
		list := make([]any, len(e.Elements))
		for i, el := range e.Elements {
			v, err := env.Eval(el)
			if err != nil {
				return nil, err
			}
			list[i] = v
		}
		return list, nil
	case *ast.StructExpr:
		m := make(map[string]any, len(e.Fields))
		for _, f := range e.Fields {
			v, err := env.Eval(f.Value)
			if err != nil {
				return nil, err
			}
			m[f.Name] = v
		}
		return m, nil
	default:
		return nil, fmt.Errorf("cannot evaluate %T", n)
	}
}

func (env *Env) lookup(name string) (any, error) {
	if v, ok := env.vars[name]; ok {
		return v, nil
	}
	if expr, ok := env.computeds[name]; ok {
		return env.Eval(expr.SNGL)
	}
	if v, ok := env.consts[name]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("undefined variable %q", name)
}

func (env *Env) evalBinary(e *ast.BinaryExpr) (any, error) {
	left, err := env.Eval(e.Left)
	if err != nil {
		return nil, err
	}
	right, err := env.Eval(e.Right)
	if err != nil {
		return nil, err
	}

	// Unit-aware arithmetic
	lu, leftIsUnit := left.(ast.UnitValue)
	ru, rightIsUnit := right.(ast.UnitValue)

	if leftIsUnit || rightIsUnit {
		switch e.Op {
		case ast.BinEq:
			if leftIsUnit && rightIsUnit {
				return lu.Equal(ru), nil
			}
			return false, nil
		case ast.BinNeq:
			if leftIsUnit && rightIsUnit {
				return !lu.Equal(ru), nil
			}
			return true, nil
		case ast.BinAdd:
			if leftIsUnit && rightIsUnit {
				if lu.Unit != ru.Unit {
					return nil, fmt.Errorf("cannot add %s and %s units", lu.Unit, ru.Unit)
				}
				return lu.Add(ru), nil
			}
		case ast.BinSub:
			if leftIsUnit && rightIsUnit {
				if lu.Unit != ru.Unit {
					return nil, fmt.Errorf("cannot subtract %s and %s units", lu.Unit, ru.Unit)
				}
				return lu.Sub(ru), nil
			}
		case ast.BinMul:
			if leftIsUnit && !rightIsUnit {
				return lu.Scale(toFloat(right)), nil
			}
			if !leftIsUnit && rightIsUnit {
				return ru.Scale(toFloat(left)), nil
			}
			return nil, fmt.Errorf("cannot multiply two unit values")
		case ast.BinDiv:
			if leftIsUnit && !rightIsUnit {
				r := toFloat(right)
				if r == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return lu.Scale(1 / r), nil
			}
			return nil, fmt.Errorf("cannot divide by a unit value")
		}
	}

	switch e.Op {
	case ast.BinEq:
		return equals(left, right), nil
	case ast.BinNeq:
		return !equals(left, right), nil
	case ast.BinAnd:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("&& requires bool operands, got %T and %T", left, right)
		}
		return lb && rb, nil
	case ast.BinOr:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("|| requires bool operands, got %T and %T", left, right)
		}
		return lb || rb, nil
	case ast.BinLt:
		return compareNum(left, right) < 0, nil
	case ast.BinLte:
		return compareNum(left, right) <= 0, nil
	case ast.BinGt:
		return compareNum(left, right) > 0, nil
	case ast.BinGte:
		return compareNum(left, right) >= 0, nil
	case ast.BinAdd:
		if ls, ok := left.(string); ok {
			return ls + fmt.Sprintf("%v", right), nil
		}
		return numericResult(toFloat(left) + toFloat(right)), nil
	case ast.BinSub:
		return numericResult(toFloat(left) - toFloat(right)), nil
	case ast.BinMul:
		return numericResult(toFloat(left) * toFloat(right)), nil
	case ast.BinDiv:
		r := toFloat(right)
		if r == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return numericResult(toFloat(left) / r), nil
	case ast.BinMod:
		return numericResult(math.Mod(toFloat(left), toFloat(right))), nil
	}
	return nil, fmt.Errorf("unknown binary op %d", e.Op)
}

func (env *Env) evalUnary(e *ast.UnaryExpr) (any, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case ast.UnaryNot:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("! requires bool operand, got %T", v)
		}
		return !b, nil
	case ast.UnaryNeg:
		return -toFloat(v), nil
	}
	return nil, fmt.Errorf("unknown unary op %d", e.Op)
}

func (env *Env) evalCall(e *ast.CallExpr) (any, error) {
	switch e.Func {
	case "string":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("string() requires 1 argument")
		}
		v, err := env.Eval(e.Args[0])
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%v", v), nil
	case "int":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("int() requires 1 argument")
		}
		v, err := env.Eval(e.Args[0])
		if err != nil {
			return nil, err
		}
		return toInt(v), nil
	case "float":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("float() requires 1 argument")
		}
		v, err := env.Eval(e.Args[0])
		if err != nil {
			return nil, err
		}
		return toFloat(v), nil
	case "tick":
		// Execute all active timers' body statements once
		for _, t := range env.timers {
			active, ok := env.vars[t.Active]
			if !ok {
				continue
			}
			if b, ok := active.(bool); ok && b {
				if err := env.Exec(t.Body); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	case "assert":
		// Handled by exec, but if called as expression just evaluate
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("assert() requires 1 argument")
		}
		v, err := env.Eval(e.Args[0])
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("assert() requires bool argument, got %T (%v)", v, v)
		}
		if !b {
			return nil, &AssertError{Expr: e.Args[0], Got: v}
		}
		return nil, nil
	default:
		if fn, ok := env.funcs[e.Func]; ok {
			return env.evalUserFunc(fn, e.Args)
		}
		return nil, fmt.Errorf("unknown function %q", e.Func)
	}
}

// evalUserFunc evaluates a user-defined function call.
// Pure functions with tail-recursive self-calls are optimized via a trampoline loop.
func (env *Env) evalUserFunc(fn *ast.FuncDef, argNodes []ast.Node) (any, error) {
	// Evaluate arguments eagerly
	args := make([]any, len(argNodes))
	for i, a := range argNodes {
		v, err := env.Eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}

	// Stack depth check — one frame for this call (TCO reuses it).
	env.depth++
	if env.depth > maxCallDepth {
		env.depth--
		return nil, fmt.Errorf("stack overflow: call depth exceeded %d", maxCallDepth)
	}
	defer func() { env.depth-- }()

	// TCO only applies to pure functions (have ReturnType).
	canTCO := fn.ReturnType != ""

	for { // trampoline loop (only iterates >1 for tail calls)
		// Void/action functions execute in the caller's env (they mutate state).
		// Pure functions (have ReturnType) execute in a snapshot.
		var execEnv *Env
		if fn.ReturnType == "" {
			execEnv = env
		} else {
			execEnv = env.Snapshot()
		}

		// Bind params (save originals for cleanup in void case)
		savedVars := make(map[string]any)
		paramNames := make([]string, len(fn.Params))
		for i, p := range fn.Params {
			paramNames[i] = p.Name
			if v, ok := execEnv.vars[p.Name]; ok {
				savedVars[p.Name] = v
			}
			if i < len(args) {
				execEnv.vars[p.Name] = args[i]
			}
		}

		// restoreVoid cleans up params after void functions so they don't leak.
		restoreVoid := func() {
			if fn.ReturnType == "" {
				for _, name := range paramNames {
					if orig, ok := savedVars[name]; ok {
						execEnv.vars[name] = orig
					} else {
						delete(execEnv.vars, name)
					}
				}
			}
		}

		// Determine the tail expression
		var tailExpr ast.Node
		var localVars []string

		if fn.Body.SNGL != nil {
			tailExpr = fn.Body.SNGL
		} else if fn.Block != nil {
			// Execute block statements
			for _, stmt := range fn.Block.Stmts {
				switch s := stmt.(type) {
				case *ast.VarStmt:
					v, err := execEnv.Eval(s.Init)
					if err != nil {
						restoreVoid()
						return nil, err
					}
					execEnv.vars[s.Name] = v
					localVars = append(localVars, s.Name)
				default:
					if err := execEnv.Exec(stmt); err != nil {
						restoreVoid()
						return nil, err
					}
				}
			}
			tailExpr = fn.Block.Return
		}

		// If no tail expression, return nil
		if tailExpr == nil {
			restoreVoid()
			for _, name := range localVars {
				delete(execEnv.vars, name)
			}
			return nil, nil
		}

		// Try tail-call optimization on the tail expression
		if canTCO {
			result, newArgs, isTailCall, err := execEnv.evalTailAware(tailExpr, fn.Name)
			for _, name := range localVars {
				delete(execEnv.vars, name)
			}
			if err != nil {
				return nil, err
			}
			if isTailCall {
				args = newArgs
				continue // trampoline — reuse this frame
			}
			return result, nil
		}

		// No TCO — normal evaluation
		result, err := execEnv.Eval(tailExpr)
		restoreVoid()
		for _, name := range localVars {
			delete(execEnv.vars, name)
		}
		return result, err
	}
}

// evalTailAware evaluates a node, detecting tail calls to funcName.
// Returns (result, newArgs, isTailCall, error).
// When isTailCall is true, newArgs contains the evaluated arguments for the next iteration.
func (env *Env) evalTailAware(n ast.Node, funcName string) (any, []any, bool, error) {
	switch e := n.(type) {
	case *ast.CallExpr:
		if e.Func == funcName {
			// Tail call — evaluate args and signal trampoline
			newArgs, err := env.evalArgs(e.Args)
			if err != nil {
				return nil, nil, false, err
			}
			return nil, newArgs, true, nil
		}
		// Not a self-call — evaluate normally
		result, err := env.Eval(n)
		return result, nil, false, err
	case *ast.TernaryExpr:
		// Unwrap ternary: evaluate condition, then check chosen branch
		cond, err := env.Eval(e.Cond)
		if err != nil {
			return nil, nil, false, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, nil, false, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.evalTailAware(e.Then, funcName)
		}
		return env.evalTailAware(e.Else, funcName)
	default:
		result, err := env.Eval(n)
		return result, nil, false, err
	}
}

func (env *Env) evalMethod(e *ast.MethodExpr) (any, error) {
	// Type-namespace call: int.sqrt(x) where receiver is IdentExpr("int")
	// Only if the identifier is NOT a variable (avoids conflict with e.g. var list)
	if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
		if _, err := env.lookup(ident.Name); err != nil {
			// Not a variable — treat as type namespace
			qualName := ident.Name + "." + e.Method
			if args, err := env.evalArgs(e.Args); err == nil {
				if result, handled, err := nativeMethod(qualName, args); handled {
					return result, err
				}
			}
			if fn, ok := env.funcs[qualName]; ok {
				return env.evalUserFunc(fn, e.Args)
			}
		}
	}

	recv, err := env.Eval(e.Receiver)
	if err != nil {
		return nil, err
	}

	// Instance method call: try native overrides first (for accurate math/string)
	typeName := runtimeTypeName(recv)
	qualName := typeName + "." + e.Method
	if args, err := env.evalArgs(e.Args); err == nil {
		allArgs := append([]any{recv}, args...)
		if result, handled, err := nativeMethod(qualName, allArgs); handled {
			return result, err
		}
	}

	// Built-in mutating methods (push, remove) and legacy methods
	switch e.Method {
	case "contains":
		if s, ok := recv.(string); ok && len(e.Args) == 1 {
			arg, err := env.Eval(e.Args[0])
			if err != nil {
				return nil, err
			}
			return strings.Contains(s, fmt.Sprintf("%v", arg)), nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(e.Args) == 1 {
			arg, err := env.Eval(e.Args[0])
			if err != nil {
				return nil, err
			}
			// Find the receiver var and update it
			if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
				env.vars[ident.Name] = append(list, arg)
			}
			return nil, nil
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(e.Args) == 1 {
			arg, err := env.Eval(e.Args[0])
			if err != nil {
				return nil, err
			}
			idx := toInt(arg)
			if idx >= 0 && idx < len(list) {
				newList := append(list[:idx], list[idx+1:]...)
				if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
					env.vars[ident.Name] = newList
				}
			}
			return nil, nil
		}
	default:
		if strings.HasPrefix(e.Method, "@") {
			if len(e.Args) != 0 {
				return nil, fmt.Errorf("%s() takes no arguments", e.Method)
			}
			if m, ok := recv.(map[string]any); ok {
				handler, ok := m[e.Method]
				if !ok {
					return nil, fmt.Errorf("no event %s on element", e.Method)
				}
				if node, ok := handler.(ast.Node); ok {
					return nil, env.Exec(node)
				}
				return nil, fmt.Errorf("%s handler is not executable", e.Method)
			}
			return nil, fmt.Errorf("%s() not supported on %T", e.Method, recv)
		}
	}
	// Fallback: try user-defined type-attached function
	if fn, ok := env.funcs[qualName]; ok {
		allArgs := make([]ast.Node, 0, 1+len(e.Args))
		allArgs = append(allArgs, e.Receiver)
		allArgs = append(allArgs, e.Args...)
		return env.evalUserFunc(fn, allArgs)
	}
	return nil, fmt.Errorf("unknown method %q on %T", e.Method, recv)
}

// evalUnitLiteral converts a parsed unit literal to a UnitValue using the env's unit tables.
func (env *Env) evalUnitLiteral(ul ast.UnitLiteral) (ast.UnitValue, error) {
	table, ok := env.units[ul.Suffix]
	if !ok {
		return ast.UnitValue{}, fmt.Errorf("unknown unit suffix %q", ul.Suffix)
	}
	num, err := strconv.ParseFloat(ul.Number, 64)
	if err != nil {
		return ast.UnitValue{}, fmt.Errorf("invalid unit number %q: %w", ul.Number, err)
	}
	return table.NewUnitValue(ul.Suffix, num), nil
}

// --- helpers ---

func equals(a, b any) bool {
	if au, ok := a.(ast.UnitValue); ok {
		if bu, ok := b.(ast.UnitValue); ok {
			return au.Equal(bu)
		}
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v any) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case float64:
		return val
	case bool:
		if val {
			return 1
		}
		return 0
	case ast.UnitValue:
		if _, amount, ok := val.IsSingleComponent(); ok {
			return amount
		}
		return 0
	}
	return 0
}

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	}
	return 0
}

func compareNum(a, b any) int {
	fa, fb := toFloat(a), toFloat(b)
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

// evalArgs evaluates a list of AST nodes into values.
func (env *Env) evalArgs(nodes []ast.Node) ([]any, error) {
	args := make([]any, len(nodes))
	for i, n := range nodes {
		v, err := env.Eval(n)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return args, nil
}

// runtimeTypeName returns the SNGL type name for a Go runtime value.
func runtimeTypeName(v any) string {
	switch v.(type) {
	case int:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "struct"
	default:
		return "dyn"
	}
}

// numericResult normalizes arithmetic results to int when possible.
func numericResult(f float64) any {
	if f == math.Trunc(f) && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return int(f)
	}
	return f
}
