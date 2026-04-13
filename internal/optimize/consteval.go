package optimize

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// isConstExpr reports whether the expression references only compile-time
// constant identifiers (PLATFORM, LANGUAGE) and literal values.
func isConstExpr(e ast.Expr, ctx *foldCtx) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ast.LiteralExpr, *ast.UnitLiteral:
		return true
	case *ast.IdentExpr:
		_, ok := ctx.vars[x.Name]
		return ok
	case *ast.BinaryExpr:
		return isConstExpr(x.Left, ctx) && isConstExpr(x.Right, ctx)
	case *ast.UnaryExpr:
		return isConstExpr(x.Operand, ctx)
	case *ast.TernaryExpr:
		return isConstExpr(x.Cond, ctx) && isConstExpr(x.Then, ctx) && isConstExpr(x.Else, ctx)
	case *ast.CallExpr:
		for _, a := range x.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				if !isConstExpr(arg.Value, ctx) {
					return false
				}
			}
		}
		return true
	case *ast.InterpolationExpr:
		for _, p := range x.Parts {
			if !isConstExpr(p, ctx) {
				return false
			}
		}
		return true
	case *ast.ListExpr:
		for _, el := range x.Elements {
			if !isConstExpr(el, ctx) {
				return false
			}
		}
		return true
	case *ast.SelectExpr:
		return isConstExpr(x.Operand, ctx)
	case *ast.ParenExpr:
		return isConstExpr(x.Inner, ctx)
	default:
		return false
	}
}

// evalConst evaluates a constant expression and returns the result.
func evalConst(e ast.Expr, ctx *foldCtx) (any, bool) {
	if e == nil {
		return nil, false
	}
	if !isConstExpr(e, ctx) {
		return nil, false
	}
	switch x := e.(type) {
	case *ast.LiteralExpr:
		return parseLiteralValue(x), true
	case *ast.IdentExpr:
		v, ok := ctx.vars[x.Name]
		return v, ok
	case *ast.BinaryExpr:
		left, lok := evalConst(x.Left, ctx)
		right, rok := evalConst(x.Right, ctx)
		if !lok || !rok {
			return nil, false
		}
		return evalBinaryOp(x.Op, left, right)
	case *ast.UnaryExpr:
		operand, ok := evalConst(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		return evalUnaryOp(x.Op, operand)
	case *ast.TernaryExpr:
		cond, ok := evalConst(x.Cond, ctx)
		if !ok {
			return nil, false
		}
		if b, ok := cond.(bool); ok {
			if b {
				return evalConst(x.Then, ctx)
			}
			return evalConst(x.Else, ctx)
		}
		return nil, false
	case *ast.CallExpr:
		args := collectArgs(x, ctx)
		if args == nil {
			return nil, false
		}
		// Check for method-style call (SelectExpr func)
		if sel, ok := x.Func.(*ast.SelectExpr); ok {
			if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
				qualName := ident.Name + "." + sel.Field
				return evalQualifiedMethod(qualName, args)
			}
			recv, ok := evalConst(sel.Operand, ctx)
			if !ok {
				return nil, false
			}
			return evalMethod(sel.Field, recv, args)
		}
		// Simple function call
		if ident, ok := x.Func.(*ast.IdentExpr); ok {
			return evalCallFunc(ident.Name, args)
		}
		return nil, false
	case *ast.InterpolationExpr:
		var result strings.Builder
		for _, p := range x.Parts {
			v, ok := evalConst(p, ctx)
			if !ok {
				return nil, false
			}
			result.WriteString(toStr(v))
		}
		return result.String(), true
	case *ast.SelectExpr:
		recv, ok := evalConst(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		if m, ok := recv.(map[string]any); ok {
			v, exists := m[x.Field]
			if exists {
				return v, true
			}
		}
		return nil, false
	case *ast.ListExpr:
		result := make([]any, 0, len(x.Elements))
		for _, el := range x.Elements {
			v, ok := evalConst(el, ctx)
			if !ok {
				return nil, false
			}
			result = append(result, v)
		}
		return result, true
	case *ast.ParenExpr:
		return evalConst(x.Inner, ctx)
	default:
		return nil, false
	}
}

func collectArgs(call *ast.CallExpr, ctx *foldCtx) []any {
	var args []any
	for _, a := range call.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			v, ok := evalConst(arg.Value, ctx)
			if !ok {
				return nil
			}
			args = append(args, v)
		}
	}
	return args
}

func evalBinaryOp(op ast.BinaryOp, left, right any) (any, bool) {
	switch op {
	case ast.BinEq:
		return left == right, true
	case ast.BinNeq:
		return left != right, true
	case ast.BinAnd:
		lb, lok := left.(bool)
		rb, rok := right.(bool)
		if lok && rok {
			return lb && rb, true
		}
	case ast.BinOr:
		lb, lok := left.(bool)
		rb, rok := right.(bool)
		if lok && rok {
			return lb || rb, true
		}
	case ast.BinAdd:
		if ls, ok := left.(string); ok {
			if rs, ok := right.(string); ok {
				return ls + rs, true
			}
		}
		return numericOp(op, left, right)
	case ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
		return numericOp(op, left, right)
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		return compareOp(op, left, right)
	}
	return nil, false
}

func numericOp(op ast.BinaryOp, left, right any) (any, bool) {
	li, lok := toInt(left)
	ri, rok := toInt(right)
	if lok && rok {
		switch op {
		case ast.BinAdd:
			return li + ri, true
		case ast.BinSub:
			return li - ri, true
		case ast.BinMul:
			return li * ri, true
		case ast.BinDiv:
			if ri == 0 {
				return nil, false
			}
			return li / ri, true
		case ast.BinMod:
			if ri == 0 {
				return nil, false
			}
			return li % ri, true
		}
	}
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if lok && rok {
		switch op {
		case ast.BinAdd:
			return lf + rf, true
		case ast.BinSub:
			return lf - rf, true
		case ast.BinMul:
			return lf * rf, true
		case ast.BinDiv:
			if rf == 0 {
				return nil, false
			}
			return lf / rf, true
		}
	}
	return nil, false
}

func compareOp(op ast.BinaryOp, left, right any) (any, bool) {
	li, lok := toInt(left)
	ri, rok := toInt(right)
	if lok && rok {
		switch op {
		case ast.BinLt:
			return li < ri, true
		case ast.BinLte:
			return li <= ri, true
		case ast.BinGt:
			return li > ri, true
		case ast.BinGte:
			return li >= ri, true
		}
	}
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if lok && rok {
		switch op {
		case ast.BinLt:
			return lf < rf, true
		case ast.BinLte:
			return lf <= rf, true
		case ast.BinGt:
			return lf > rf, true
		case ast.BinGte:
			return lf >= rf, true
		}
	}
	if ls, ok := left.(string); ok {
		if rs, ok := right.(string); ok {
			switch op {
			case ast.BinLt:
				return ls < rs, true
			case ast.BinLte:
				return ls <= rs, true
			case ast.BinGt:
				return ls > rs, true
			case ast.BinGte:
				return ls >= rs, true
			}
		}
	}
	return nil, false
}

func evalUnaryOp(op ast.UnaryOp, operand any) (any, bool) {
	switch op {
	case ast.UnaryNot:
		if b, ok := operand.(bool); ok {
			return !b, true
		}
	case ast.UnaryNeg:
		if i, ok := operand.(int); ok {
			return -i, true
		}
		if f, ok := operand.(float64); ok {
			return -f, true
		}
	}
	return nil, false
}

func toInt(v any) (int, bool) {
	if n, ok := v.(int); ok {
		return n, true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func evalCallFunc(name string, args []any) (any, bool) {
	if len(args) != 1 {
		return nil, false
	}
	arg := args[0]
	switch name {
	case "string":
		return fmt.Sprintf("%v", arg), true
	case "int":
		switch v := arg.(type) {
		case int:
			return v, true
		case float64:
			return int(v), true
		case string:
			i, err := strconv.Atoi(v)
			if err != nil {
				return nil, false
			}
			return i, true
		}
	case "float":
		switch v := arg.(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, false
			}
			return f, true
		}
	}
	return nil, false
}

func evalMethod(method string, recv any, args []any) (any, bool) {
	typeName := "dyn"
	switch recv.(type) {
	case int:
		typeName = "int"
	case float64:
		typeName = "float"
	case string:
		typeName = "string"
	case bool:
		typeName = "bool"
	}
	allArgs := append([]any{recv}, args...)
	return evalQualifiedMethod(typeName+"."+method, allArgs)
}

func evalQualifiedMethod(qualName string, args []any) (any, bool) {
	switch qualName {
	case "int.min":
		a, aok := toInt(args[0])
		b, bok := toInt(args[1])
		if aok && bok {
			if a < b {
				return a, true
			}
			return b, true
		}
	case "int.max":
		a, aok := toInt(args[0])
		b, bok := toInt(args[1])
		if aok && bok {
			if a > b {
				return a, true
			}
			return b, true
		}
	case "int.abs":
		x, ok := toInt(args[0])
		if ok {
			if x < 0 {
				return -x, true
			}
			return x, true
		}
	case "float.min":
		a, aok := toFloat(args[0])
		b, bok := toFloat(args[1])
		if aok && bok {
			return math.Min(a, b), true
		}
	case "float.max":
		a, aok := toFloat(args[0])
		b, bok := toFloat(args[1])
		if aok && bok {
			return math.Max(a, b), true
		}
	case "float.abs":
		x, ok := toFloat(args[0])
		if ok {
			return math.Abs(x), true
		}
	case "float.floor":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Floor(x)), true
		}
	case "float.ceil":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Ceil(x)), true
		}
	case "float.round":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Round(x)), true
		}
	case "float.sqrt":
		x, ok := toFloat(args[0])
		if ok {
			return math.Sqrt(x), true
		}
	case "string.length":
		if s, ok := args[0].(string); ok {
			return len(s), true
		}
	case "list.length":
		if l, ok := args[0].([]any); ok {
			return len(l), true
		}
	case "string.upper":
		if s, ok := args[0].(string); ok {
			return strings.ToUpper(s), true
		}
	case "string.lower":
		if s, ok := args[0].(string); ok {
			return strings.ToLower(s), true
		}
	case "string.trim":
		if s, ok := args[0].(string); ok {
			return strings.TrimSpace(s), true
		}
	case "string.contains":
		s, sok := args[0].(string)
		sub, subok := args[1].(string)
		if sok && subok {
			return strings.Contains(s, sub), true
		}
	case "string.replace":
		s, sok := args[0].(string)
		old, ook := args[1].(string)
		new_, nok := args[2].(string)
		if sok && ook && nok {
			return strings.ReplaceAll(s, old, new_), true
		}
	}
	return nil, false
}
