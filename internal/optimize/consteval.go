package optimize

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// isConstExpr reports whether the SNGL expression references only compile-time
// constant identifiers (PLATFORM, LANGUAGE) and literal values.
func isConstExpr(n ast.Node, vars map[string]any) bool {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return true
	case *ast.IdentExpr:
		_, ok := vars[e.Name]
		return ok
	case *ast.BinaryExpr:
		return isConstExpr(e.Left, vars) && isConstExpr(e.Right, vars)
	case *ast.UnaryExpr:
		return isConstExpr(e.Operand, vars)
	case *ast.TernaryExpr:
		return isConstExpr(e.Cond, vars) && isConstExpr(e.Then, vars) && isConstExpr(e.Else, vars)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if !isConstExpr(arg, vars) {
				return false
			}
		}
		return true
	case *ast.MethodExpr:
		if !isConstExpr(e.Receiver, vars) {
			return false
		}
		for _, arg := range e.Args {
			if !isConstExpr(arg, vars) {
				return false
			}
		}
		return true
	case *ast.InterpolationExpr:
		for _, p := range e.Parts {
			if !isConstExpr(p, vars) {
				return false
			}
		}
		return true
	case *ast.ListExpr:
		for _, el := range e.Elements {
			if !isConstExpr(el, vars) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// evalConst evaluates a constant SNGL expression and returns the result.
// It returns (nil, false) if the expression cannot be evaluated.
func evalConst(n ast.Node, vars map[string]any) (any, bool) {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return e.Value, true
	case *ast.IdentExpr:
		v, ok := vars[e.Name]
		return v, ok
	case *ast.BinaryExpr:
		left, lok := evalConst(e.Left, vars)
		right, rok := evalConst(e.Right, vars)
		if !lok || !rok {
			return nil, false
		}
		return evalBinaryOp(e.Op, left, right)
	case *ast.UnaryExpr:
		operand, ok := evalConst(e.Operand, vars)
		if !ok {
			return nil, false
		}
		return evalUnaryOp(e.Op, operand)
	case *ast.TernaryExpr:
		cond, ok := evalConst(e.Cond, vars)
		if !ok {
			return nil, false
		}
		if b, ok := cond.(bool); ok {
			if b {
				return evalConst(e.Then, vars)
			}
			return evalConst(e.Else, vars)
		}
		return nil, false
	case *ast.CallExpr:
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			v, ok := evalConst(a, vars)
			if !ok {
				return nil, false
			}
			args[i] = v
		}
		return evalCallFunc(e.Func, args)
	case *ast.MethodExpr:
		recv, ok := evalConst(e.Receiver, vars)
		if !ok {
			return nil, false
		}
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			v, ok := evalConst(a, vars)
			if !ok {
				return nil, false
			}
			args[i] = v
		}
		return evalMethod(e.Method, recv, args)
	case *ast.InterpolationExpr:
		var result string
		for _, p := range e.Parts {
			v, ok := evalConst(p, vars)
			if !ok {
				return nil, false
			}
			result += toStr(v)
		}
		return result, true
	default:
		return nil, false
	}
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
	switch n := v.(type) {
	case int:
		return n, true
	default:
		return 0, false
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
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
	case "size":
		switch v := arg.(type) {
		case string:
			return len(v), true
		case []any:
			return len(v), true
		}
	}
	return nil, false
}

func evalMethod(method string, recv any, args []any) (any, bool) {
	switch method {
	case "length":
		if len(args) != 0 {
			return nil, false
		}
		switch v := recv.(type) {
		case string:
			return len(v), true
		case []any:
			return len(v), true
		}
	case "contains":
		if len(args) != 1 {
			return nil, false
		}
		s, ok := recv.(string)
		if !ok {
			return nil, false
		}
		sub, ok := args[0].(string)
		if !ok {
			return nil, false
		}
		return strings.Contains(s, sub), true
	case "startsWith":
		if len(args) != 1 {
			return nil, false
		}
		s, ok := recv.(string)
		if !ok {
			return nil, false
		}
		prefix, ok := args[0].(string)
		if !ok {
			return nil, false
		}
		return strings.HasPrefix(s, prefix), true
	case "endsWith":
		if len(args) != 1 {
			return nil, false
		}
		s, ok := recv.(string)
		if !ok {
			return nil, false
		}
		suffix, ok := args[0].(string)
		if !ok {
			return nil, false
		}
		return strings.HasSuffix(s, suffix), true
	}
	return nil, false
}
