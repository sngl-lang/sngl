package testrunner

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// maxCallDepth is the maximum allowed function call depth.
const maxCallDepth = 100

// lambdaValue is a closure captured by a lambda expression.
type lambdaValue struct {
	params []string
	body   ast.Expr      // expression body (arrow form)
	block  ast.StmtBlock // block body (func(params) { ... } form)
	env    *Env
}

// call invokes the lambda with the given arguments.
func (lv *lambdaValue) call(args []any) (any, error) {
	child := lv.env.Snapshot()
	for i, p := range lv.params {
		if i < len(args) {
			child.vars[p] = args[i]
		}
	}
	if lv.block.IsDefined() {
		for _, stmt := range lv.block.Stmts {
			if ret, ok := stmt.(*ast.ReturnStmt); ok {
				if ret.Value != nil {
					return child.Eval(ret.Value)
				}
				return nil, nil
			}
			if err := child.Exec(stmt); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if lv.body != nil {
		return child.Eval(lv.body)
	}
	return nil, nil
}

// unitValue is the runtime representation of a unit value (e.g. 10px).
// BaseAmount stores the value normalized to the base unit of the family.
// Suffix is the display suffix (the one used in the source literal).
type unitValue struct {
	BaseAmount float64 // amount in base unit
	Suffix     string  // display suffix
	Table      *unitTable
}

func (u unitValue) Equal(other unitValue) bool {
	if u.Table != other.Table {
		return false
	}
	return u.BaseAmount == other.BaseAmount
}

func (u unitValue) Add(other unitValue) unitValue {
	if u.Table != other.Table {
		return u // incompatible — caller should check
	}
	// Result uses left operand's suffix
	return unitValue{BaseAmount: u.BaseAmount + other.BaseAmount, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) Sub(other unitValue) unitValue {
	if u.Table != other.Table {
		return u
	}
	return unitValue{BaseAmount: u.BaseAmount - other.BaseAmount, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) Scale(factor float64) unitValue {
	return unitValue{BaseAmount: u.BaseAmount * factor, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) displayAmount() float64 {
	if u.Table != nil {
		if factor, ok := u.Table.Conversions[u.Suffix]; ok && factor != 0 {
			return u.BaseAmount / factor
		}
	}
	return u.BaseAmount
}

func (u unitValue) String() string {
	amt := u.displayAmount()
	if amt == math.Trunc(amt) && !math.IsInf(amt, 0) && !math.IsNaN(amt) {
		return fmt.Sprintf("%d%s", int(amt), u.Suffix)
	}
	return fmt.Sprintf("%g%s", amt, u.Suffix)
}

// sameFamily returns true if both unit values belong to the same unit table.
func (u unitValue) sameFamily(other unitValue) bool {
	return u.Table != nil && u.Table == other.Table
}

// unitTable maps suffixes to their conversion factors for a unit family.
// Conversions maps each suffix to its factor relative to the base unit.
// The base unit has factor 1.
type unitTable struct {
	Base        string
	Conversions map[string]float64 // suffix -> factor in base units
}

// Env holds the mutable state for test execution.
type Env struct {
	vars        map[string]any
	consts      map[string]any
	funcs       map[string]*ast.FuncDef
	units       map[string]*unitTable // suffix -> unit table
	doc         *ast.Document
	bodyStmts   []ast.Stmt // visual body statements (VisualNode, CallStmt, IfStmt, ForStmt)
	depth       int        // current call stack depth
	renderDepth int        // current component render depth
	Log         []string
}

func NewEnv() *Env {
	return &Env{
		vars:   map[string]any{},
		consts: map[string]any{},
		funcs:  map[string]*ast.FuncDef{},
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
		vars:        make(map[string]any, len(env.vars)),
		consts:      env.consts,
		funcs:       env.funcs,
		units:       env.units,
		doc:         env.doc,
		bodyStmts:   env.bodyStmts,
		depth:       env.depth,
		renderDepth: env.renderDepth,
	}
	maps.Copy(cp.vars, env.vars)
	return cp
}

// Eval evaluates an AST expression and returns its value.
func (env *Env) Eval(e ast.Expr) (any, error) {
	switch n := e.(type) {
	case *ast.UnitLiteral:
		return env.evalUnitLiteral(n)
	case *ast.LiteralExpr:
		return env.evalLiteral(n)
	case *ast.IdentExpr:
		return env.lookup(n.Name)
	case *ast.BinaryExpr:
		return env.evalBinary(n)
	case *ast.UnaryExpr:
		return env.evalUnary(n)
	case *ast.TernaryExpr:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.Eval(n.Then)
		}
		return env.Eval(n.Else)
	case *ast.SelectExpr:
		obj, err := env.Eval(n.Operand)
		if err != nil {
			return nil, err
		}
		if cv, ok := obj.(*componentValue); ok {
			switch n.Kind {
			case ast.SelectElemRef:
				return cv.env.resolveElementRef(n.Field)
			default:
				return cv.getField(n.Field)
			}
		}
		if m, ok := obj.(map[string]any); ok {
			switch n.Kind {
			case ast.SelectEvent:
				return m["@"+n.Field], nil
			default:
				return m[n.Field], nil
			}
		}
		return nil, fmt.Errorf("cannot select field %q on %T", n.Field, obj)
	case *ast.IndexExpr:
		obj, err := env.Eval(n.Operand)
		if err != nil {
			return nil, err
		}
		idx, err := env.Eval(n.Index)
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
		return env.resolveElementRef(n.Name)
	case *ast.CallExpr:
		return env.evalCall(n)
	case *ast.InterpolationExpr:
		var sb strings.Builder
		for _, p := range n.Parts {
			v, err := env.Eval(p)
			if err != nil {
				return nil, err
			}
			sb.WriteString(fmt.Sprintf("%v", v))
		}
		return sb.String(), nil
	case *ast.ListExpr:
		var list []any
		for _, el := range n.Elements {
			if spread, ok := el.(*ast.SpreadExpr); ok {
				v, err := env.Eval(spread.Operand)
				if err != nil {
					return nil, err
				}
				if items, ok := v.([]any); ok {
					list = append(list, items...)
				} else {
					list = append(list, v)
				}
			} else {
				v, err := env.Eval(el)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
		}
		return list, nil
	case *ast.StructExpr:
		m := make(map[string]any, len(n.Fields))
		for _, f := range n.Fields {
			if f.Spread {
				v, err := env.Eval(f.Value)
				if err != nil {
					return nil, err
				}
				if src, ok := v.(map[string]any); ok {
					maps.Copy(m, src)
				}
			} else {
				v, err := env.Eval(f.Value)
				if err != nil {
					return nil, err
				}
				m[f.Name] = v
			}
		}
		return m, nil
	case *ast.SpreadExpr:
		return env.Eval(n.Operand)
	case *ast.LambdaExpr:
		params := make([]string, len(n.Params.Params))
		for i, p := range n.Params.Params {
			params[i] = p.Name
		}
		return &lambdaValue{params: params, body: n.Body, block: n.Block, env: env}, nil
	case *ast.ParenExpr:
		return env.Eval(n.Inner)
	default:
		return nil, fmt.Errorf("cannot evaluate %T", e)
	}
}

func (env *Env) evalLiteral(e *ast.LiteralExpr) (any, error) {
	switch e.Kind {
	case ast.LiteralColor:
		return colorHexToStruct(e.Raw), nil
	case ast.LiteralBool:
		return e.Raw == "true", nil
	case ast.LiteralNull:
		return nil, nil
	case ast.LiteralInt:
		raw := strings.ReplaceAll(e.Raw, "_", "")
		// Handle hex (0x), octal (0o), binary (0b) prefixes
		n, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			return 0, nil
		}
		return int(n), nil
	case ast.LiteralFloat:
		raw := strings.ReplaceAll(e.Raw, "_", "")
		f, _ := strconv.ParseFloat(raw, 64)
		return f, nil
	case ast.LiteralStringQuoted:
		if s, ok := codegen.ExprLiteralString(e); ok {
			return s, nil
		}
		return e.Raw, nil
	case ast.LiteralStringBackticked:
		if s, ok := codegen.ExprLiteralString(e); ok {
			return s, nil
		}
		return e.Raw, nil
	case ast.LiteralStringTrippleQuoted:
		if s, ok := codegen.ExprLiteralString(e); ok {
			return s, nil
		}
		return e.Raw, nil
	default:
		return e.Raw, nil
	}
}

func (env *Env) lookup(name string) (any, error) {
	if v, ok := env.vars[name]; ok {
		return v, nil
	}
	if v, ok := env.consts[name]; ok {
		return v, nil
	}
	// Auto-invoke zero-arg expression-form functions (formerly computed)
	if fn, ok := env.funcs[name]; ok && len(fn.Params.Params) == 0 && fn.Body != nil {
		return env.Eval(fn.Body)
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
	lu, leftIsUnit := left.(unitValue)
	ru, rightIsUnit := right.(unitValue)

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
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot add %s and %s units", lu.Suffix, ru.Suffix)
				}
				return lu.Add(ru), nil
			}
		case ast.BinSub:
			if leftIsUnit && rightIsUnit {
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot subtract %s and %s units", lu.Suffix, ru.Suffix)
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
	// Check if this is a method call (SelectExpr as Func)
	if sel, ok := e.Func.(*ast.SelectExpr); ok {
		return env.evalMethodCall(e, sel)
	}

	funcName := codegen.CallFuncName(e)
	args := codegen.CallArgs(e)

	switch funcName {
	case "string":
		if len(args) != 1 {
			return nil, fmt.Errorf("string() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%v", v), nil
	case "int":
		if len(args) != 1 {
			return nil, fmt.Errorf("int() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		return toInt(v), nil
	case "float":
		if len(args) != 1 {
			return nil, fmt.Errorf("float() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		return toFloat(v), nil
	case "regex":
		if len(args) != 1 {
			return nil, fmt.Errorf("regex() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		pattern := fmt.Sprintf("%v", v)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %v", err)
		}
		return re, nil
	case "tick":
		// No-op: timers are handled at the component level in v2
		return nil, nil
	case "assert":
		if len(args) != 1 {
			return nil, fmt.Errorf("assert() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("assert() requires bool argument, got %T (%v)", v, v)
		}
		if !b {
			return nil, &AssertError{Expr: args[0], Got: v}
		}
		return nil, nil
	default:
		if fn, ok := env.funcs[funcName]; ok {
			return env.evalUserFunc(fn, args)
		}
		return nil, fmt.Errorf("unknown function %q", funcName)
	}
}

// evalMethodCall handles method calls: receiver.method(args).
func (env *Env) evalMethodCall(call *ast.CallExpr, sel *ast.SelectExpr) (any, error) {
	method := sel.Field
	args := codegen.CallArgs(call)

	// Type-namespace call: int.sqrt(x) where receiver is IdentExpr("int")
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if _, err := env.lookup(ident.Name); err != nil {
			// Not a variable — treat as type namespace
			qualName := ident.Name + "." + method
			if evalArgs, err := env.evalExprs(args); err == nil {
				// Log Alert/File calls for test visibility
				switch qualName {
				case "Alert.toast":
					env.Log = append(env.Log, fmt.Sprintf("[toast:%v] %v", evalArgs[1], evalArgs[0]))
					return nil, nil
				case "Alert.info":
					env.Log = append(env.Log, fmt.Sprintf("[info] %v", evalArgs[0]))
					return nil, nil
				case "Alert.warn":
					env.Log = append(env.Log, fmt.Sprintf("[warn] %v", evalArgs[0]))
					return nil, nil
				case "Alert.error":
					env.Log = append(env.Log, fmt.Sprintf("[error] %v", evalArgs[0]))
					return nil, nil
				case "Alert.confirm":
					env.Log = append(env.Log, fmt.Sprintf("[confirm] %v", evalArgs[0]))
					return true, nil
				case "File.pick":
					env.Log = append(env.Log, "[File.pick]")
					return "/mock/file.txt", nil
				case "File.pickFolder":
					env.Log = append(env.Log, "[File.pickFolder]")
					return "/mock/folder", nil
				}
				if result, handled, err := nativeMethod(qualName, evalArgs); handled {
					return result, err
				}
			}
			if fn, ok := env.funcs[qualName]; ok {
				return env.evalUserFunc(fn, args)
			}
		}
	}

	recv, err := env.Eval(sel.Operand)
	if err != nil {
		return nil, err
	}

	// testingT method dispatch
	if tv, ok := recv.(*testingT); ok {
		return tv.callMethod(env, method, args)
	}

	// componentValue method dispatch (for c.@event() calls)
	if cv, ok := recv.(*componentValue); ok {
		return cv.callMethod(env, method, args)
	}

	// Instance method call: try native overrides first
	typeName := runtimeTypeName(recv)
	qualName := typeName + "." + method
	if evalArgs, err := env.evalExprs(args); err == nil {
		allArgs := append([]any{recv}, evalArgs...)
		if result, handled, err := nativeMethod(qualName, allArgs); handled {
			return result, err
		}
	}

	// Built-in mutating methods (push, remove) and legacy methods
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(args) == 1 {
			pred, err := env.Eval(args[0])
			if err != nil {
				return nil, err
			}
			lv, ok := pred.(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", pred)
			}
			var out []any
			for _, item := range list {
				result, err := lv.call([]any{item})
				if err != nil {
					return nil, err
				}
				if b, ok := result.(bool); ok && b {
					out = append(out, item)
				}
			}
			if out == nil {
				out = []any{}
			}
			return out, nil
		}
	case "map":
		if list, ok := recv.([]any); ok && len(args) == 1 {
			fn, err := env.Eval(args[0])
			if err != nil {
				return nil, err
			}
			lv, ok := fn.(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", fn)
			}
			out := make([]any, len(list))
			for i, item := range list {
				result, err := lv.call([]any{item})
				if err != nil {
					return nil, err
				}
				out[i] = result
			}
			return out, nil
		}
	case "contains":
		if s, ok := recv.(string); ok && len(args) == 1 {
			arg, err := env.Eval(args[0])
			if err != nil {
				return nil, err
			}
			return strings.Contains(s, fmt.Sprintf("%v", arg)), nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(args) == 1 {
			arg, err := env.Eval(args[0])
			if err != nil {
				return nil, err
			}
			newList := append(list, arg)
			if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
				env.vars[ident.Name] = newList
			} else if parentSel, ok := sel.Operand.(*ast.SelectExpr); ok {
				if obj, err := env.Eval(parentSel.Operand); err == nil {
					if cv, ok := obj.(*componentValue); ok {
						cv.vars[parentSel.Field] = newList
						if !cv.testParams[parentSel.Field] {
							cv.env.vars[parentSel.Field] = newList
						}
					}
				}
			}
			return nil, nil
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(args) == 1 {
			arg, err := env.Eval(args[0])
			if err != nil {
				return nil, err
			}
			idx := toInt(arg)
			if idx >= 0 && idx < len(list) {
				newList := append(list[:idx], list[idx+1:]...)
				if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
					env.vars[ident.Name] = newList
				} else if parentSel, ok := sel.Operand.(*ast.SelectExpr); ok {
					if obj, err := env.Eval(parentSel.Operand); err == nil {
						if cv, ok := obj.(*componentValue); ok {
							cv.vars[parentSel.Field] = newList
							if !cv.testParams[parentSel.Field] {
								cv.env.vars[parentSel.Field] = newList
							}
						}
					}
				}
			}
			return nil, nil
		}
	default:
		// Handle event method calls (@click, @change, etc.)
		isEvent := sel.Kind == ast.SelectEvent || strings.HasPrefix(method, "@")
		if isEvent {
			eventName := method
			if !strings.HasPrefix(eventName, "@") {
				eventName = "@" + eventName
			}
			if len(args) != 0 {
				return nil, fmt.Errorf("%s() takes no arguments", eventName)
			}
			if m, ok := recv.(map[string]any); ok {
				handler, ok := m[eventName]
				if !ok {
					return nil, fmt.Errorf("no event %s on element", eventName)
				}
				if block, ok := handler.(*ast.StmtBlock); ok {
					return nil, env.ExecBlock(block)
				}
				if stmt, ok := handler.(ast.Stmt); ok {
					return nil, env.Exec(stmt)
				}
				if expr, ok := handler.(ast.Expr); ok {
					_, err := env.Eval(expr)
					return nil, err
				}
				return nil, fmt.Errorf("%s handler is not executable", eventName)
			}
			return nil, fmt.Errorf("%s() not supported on %T", eventName, recv)
		}
	}
	// Fallback: try user-defined type-attached function
	if fn, ok := env.funcs[qualName]; ok {
		allArgs := make([]ast.Expr, 0, 1+len(args))
		allArgs = append(allArgs, sel.Operand)
		allArgs = append(allArgs, args...)
		return env.evalUserFunc(fn, allArgs)
	}
	return nil, fmt.Errorf("unknown method %q on %T", method, recv)
}

// evalUserFunc evaluates a user-defined function call.
// Pure functions with tail-recursive self-calls are optimized via a trampoline loop.
func (env *Env) evalUserFunc(fn *ast.FuncDef, argExprs []ast.Expr) (any, error) {
	// Evaluate arguments eagerly
	args := make([]any, len(argExprs))
	for i, a := range argExprs {
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

	fnParams := fn.Params.Params

	// TCO only applies to pure functions (expression-form or block with ReturnType).
	canTCO := fn.Body != nil || fn.ReturnType != nil

	for { // trampoline loop (only iterates >1 for tail calls)
		// Void/action functions execute in the caller's env (they mutate state).
		// Pure functions (expression-form or block with ReturnType) execute in a snapshot.
		isPure := fn.Body != nil || fn.ReturnType != nil
		var execEnv *Env
		if !isPure {
			execEnv = env
		} else {
			execEnv = env.Snapshot()
		}

		// Bind params (save originals for cleanup in void case)
		savedVars := make(map[string]any)
		paramNames := make([]string, len(fnParams))
		for i, p := range fnParams {
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
			if !isPure {
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
		var tailExpr ast.Expr
		var localVars []string

		if fn.Body != nil {
			tailExpr = fn.Body
		} else if fn.Block.IsDefined() {
			// Execute block statements; find return value
			for _, stmt := range fn.Block.Stmts {
				if ret, ok := stmt.(*ast.ReturnStmt); ok {
					tailExpr = ret.Value
					break
				}
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
func (env *Env) evalTailAware(e ast.Expr, funcName string) (any, []any, bool, error) {
	switch n := e.(type) {
	case *ast.CallExpr:
		name := codegen.CallFuncName(n)
		if name == funcName {
			// Tail call — evaluate args and signal trampoline
			newArgs, err := env.evalExprs(codegen.CallArgs(n))
			if err != nil {
				return nil, nil, false, err
			}
			return nil, newArgs, true, nil
		}
		// Not a self-call — evaluate normally
		result, err := env.Eval(e)
		return result, nil, false, err
	case *ast.TernaryExpr:
		// Unwrap ternary: evaluate condition, then check chosen branch
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, nil, false, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, nil, false, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.evalTailAware(n.Then, funcName)
		}
		return env.evalTailAware(n.Else, funcName)
	case *ast.ParenExpr:
		return env.evalTailAware(n.Inner, funcName)
	default:
		result, err := env.Eval(e)
		return result, nil, false, err
	}
}

// evalUnitLiteral converts a parsed unit literal to a unitValue using the env's unit tables.
func (env *Env) evalUnitLiteral(ul *ast.UnitLiteral) (unitValue, error) {
	// The parser may include underscores in the suffix (e.g., "_000ms" for "1_000ms").
	// Extract the true suffix by finding the first alpha character.
	suffix := cleanUnitSuffix(ul.Suffix)

	num, err := parseUnitNumber(ul.Raw, suffix)
	if err != nil {
		return unitValue{}, fmt.Errorf("invalid unit literal %q: %w", ul.Raw, err)
	}
	table := env.units[suffix]
	baseAmount := num
	if table != nil {
		if factor, ok := table.Conversions[suffix]; ok {
			baseAmount = num * factor
		}
	}
	return unitValue{BaseAmount: baseAmount, Suffix: suffix, Table: table}, nil
}

// cleanUnitSuffix strips leading digits and underscores from a potentially
// malformed unit suffix (parser may include them for underscored numbers like 1_000ms).
func cleanUnitSuffix(s string) string {
	for i, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return s[i:]
		}
	}
	return s
}

// parseUnitNumber extracts the numeric part from a unit literal raw text.
// Handles underscored numbers like "1_000ms".
func parseUnitNumber(raw, suffix string) (float64, error) {
	// Strip the suffix from the end of raw. The raw might contain the full
	// parser suffix (with leading digits/underscores) so strip from the right.
	numStr := raw
	if suffix != "" {
		numStr = strings.TrimSuffix(numStr, suffix)
	}
	// If the suffix wasn't fully removed (e.g., raw="1_000ms" suffix="ms" -> "1_000"),
	// trim any remaining alpha characters from the right
	for len(numStr) > 0 {
		c := numStr[len(numStr)-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			numStr = numStr[:len(numStr)-1]
		} else {
			break
		}
	}
	numStr = strings.ReplaceAll(numStr, "_", "")
	if numStr == "" {
		return 0, fmt.Errorf("empty number in unit literal %q", raw)
	}
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// --- helpers ---

func equals(a, b any) bool {
	if au, ok := a.(unitValue); ok {
		if bu, ok := b.(unitValue); ok {
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
	case unitValue:
		return val.BaseAmount
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

// evalExprs evaluates a list of AST expressions into values.
func (env *Env) evalExprs(exprs []ast.Expr) ([]any, error) {
	args := make([]any, len(exprs))
	for i, e := range exprs {
		v, err := env.Eval(e)
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
	case *regexp.Regexp:
		return "regex"
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
