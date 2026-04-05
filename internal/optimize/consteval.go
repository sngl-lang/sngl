package optimize

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// isConstExpr reports whether the SNGL expression references only compile-time
// constant identifiers (PLATFORM, LANGUAGE) and literal values.
func isConstExpr(n ast.Node, ctx *foldCtx) bool {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return true
	case *ast.IdentExpr:
		_, ok := ctx.vars[e.Name]
		return ok
	case *ast.BinaryExpr:
		return isConstExpr(e.Left, ctx) && isConstExpr(e.Right, ctx)
	case *ast.UnaryExpr:
		return isConstExpr(e.Operand, ctx)
	case *ast.TernaryExpr:
		return isConstExpr(e.Cond, ctx) && isConstExpr(e.Then, ctx) && isConstExpr(e.Else, ctx)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if !isConstExpr(arg, ctx) {
				return false
			}
		}
		return true
	case *ast.MethodExpr:
		// Type-namespace calls like string.length("hi") — receiver is a type name, not a variable
		isTypeNS := false
		isPureNS := false
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
			switch ident.Name {
			case "int", "float", "string", "bool", "list", "color":
				isTypeNS = true
			}
			// Check if it's a namespace with a pure Go function
			if !isTypeNS && ctx.nativeImports != nil {
				if ns, exists := ctx.nativeImports[ident.Name]; exists {
					for _, d := range ns.Data {
						if d.Name == e.Method && d.Purity == ast.PurityPure {
							isPureNS = true
							break
						}
					}
					if !isPureNS {
						return false // namespace function exists but is not pure
					}
				}
			}
		}
		if !isTypeNS && !isPureNS && !isConstExpr(e.Receiver, ctx) {
			return false
		}
		for _, arg := range e.Args {
			if !isConstExpr(arg, ctx) {
				return false
			}
		}
		return true
	case *ast.InterpolationExpr:
		for _, p := range e.Parts {
			if !isConstExpr(p, ctx) {
				return false
			}
		}
		return true
	case *ast.ListExpr:
		for _, el := range e.Elements {
			if !isConstExpr(el, ctx) {
				return false
			}
		}
		return true
	case *ast.SelectExpr:
		return isConstExpr(e.Operand, ctx)
	case *ast.LambdaExpr:
		return false
	case *ast.ParenExpr:
		return isConstExpr(e.Inner, ctx)
	default:
		return false
	}
}

// evalConst evaluates a constant SNGL expression and returns the result.
// It returns (nil, false) if the expression cannot be evaluated.
func evalConst(n ast.Node, ctx *foldCtx) (any, bool) {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return e.Value, true
	case *ast.IdentExpr:
		v, ok := ctx.vars[e.Name]
		return v, ok
	case *ast.BinaryExpr:
		left, lok := evalConst(e.Left, ctx)
		right, rok := evalConst(e.Right, ctx)
		if !lok || !rok {
			return nil, false
		}
		return evalBinaryOp(e.Op, left, right)
	case *ast.UnaryExpr:
		operand, ok := evalConst(e.Operand, ctx)
		if !ok {
			return nil, false
		}
		return evalUnaryOp(e.Op, operand)
	case *ast.TernaryExpr:
		cond, ok := evalConst(e.Cond, ctx)
		if !ok {
			return nil, false
		}
		if b, ok := cond.(bool); ok {
			if b {
				return evalConst(e.Then, ctx)
			}
			return evalConst(e.Else, ctx)
		}
		return nil, false
	case *ast.CallExpr:
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			v, ok := evalConst(a, ctx)
			if !ok {
				return nil, false
			}
			args[i] = v
		}
		return evalCallFunc(e.Func, args)
	case *ast.MethodExpr:
		// Type-namespace call: int.min(3, 7) — receiver is IdentExpr("int")
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
			qualName := ident.Name + "." + e.Method
			args := make([]any, len(e.Args))
			for i, a := range e.Args {
				v, ok := evalConst(a, ctx)
				if !ok {
					return nil, false
				}
				args[i] = v
			}
			if v, ok := evalQualifiedMethod(qualName, args); ok {
				return v, true
			}
			// Try file:// scheme functions (path, contents)
			if ctx.nativeImports != nil {
				if ns, exists := ctx.nativeImports[ident.Name]; exists {
					for _, d := range ns.Data {
						if d.Name == e.Method && d.Resolved != nil && d.Resolved.NativePkg == "file" {
							if len(args) == 1 {
								if filename, ok := args[0].(string); ok {
									return evalFileFunc(d.Resolved.NativeType, ns.ImportPath, filename, ctx)
								}
							}
						}
					}
				}
			}
			// Try pure Go function execution
			if ctx.nativeImports != nil && ctx.dir != "" {
				if ns, exists := ctx.nativeImports[ident.Name]; exists {
					for _, d := range ns.Data {
						if d.Name == e.Method && d.Purity == ast.PurityPure && d.Resolved != nil && d.Resolved.NativePkg != "file" {
							result, err := execPureGoFunc(ctx.dir, ns.ImportPath, d.Resolved.NativeType, d.ParamTypes, d.ReturnType, args)
							if err != nil {
								return nil, false
							}
							return result, true
						}
					}
				}
			}
		}
		recv, ok := evalConst(e.Receiver, ctx)
		if !ok {
			return nil, false
		}
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			v, ok := evalConst(a, ctx)
			if !ok {
				return nil, false
			}
			args[i] = v
		}
		return evalMethod(e.Method, recv, args)
	case *ast.InterpolationExpr:
		var result strings.Builder
		for _, p := range e.Parts {
			v, ok := evalConst(p, ctx)
			if !ok {
				return nil, false
			}
			result.WriteString(toStr(v))
		}
		return result.String(), true
	case *ast.SelectExpr:
		recv, ok := evalConst(e.Operand, ctx)
		if !ok {
			return nil, false
		}
		if m, ok := recv.(map[string]any); ok {
			v, exists := m[e.Field]
			if exists {
				return v, true
			}
			// Try with uppercase first letter (JSON from Go structs uses Go field names)
			upper := strings.ToUpper(e.Field[:1]) + e.Field[1:]
			if v, exists = m[upper]; exists {
				return v, true
			}
		}
		return nil, false
	case *ast.ListExpr:
		result := make([]any, len(e.Elements))
		for i, el := range e.Elements {
			v, ok := evalConst(el, ctx)
			if !ok {
				return nil, false
			}
			result[i] = v
		}
		return result, true
	case *ast.ParenExpr:
		return evalConst(e.Inner, ctx)
	default:
		return nil, false
	}
}

// evalFileFunc evaluates file:// scheme functions (path, contents).
// Uses os.DirFS to root file access within the imported directory,
// preventing directory traversal. This pattern also works for future
// remote import schemes that provide an fs.FS.
func evalFileFunc(funcName, dirPath, filename string, ctx *foldCtx) (any, bool) {
	fsys := os.DirFS(dirPath)

	switch funcName {
	case "path":
		// Verify the file exists within the directory.
		if _, err := fs.Stat(fsys, filename); err != nil {
			return nil, false
		}
		outPath := "assets/" + filename
		url := "/" + outPath
		ctx.fileAssets = append(ctx.fileAssets, ast.FileAsset{
			SrcPath: filepath.Join(dirPath, filename),
			OutPath: outPath,
			URL:     url,
		})
		return url, true

	case "contents":
		data, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return nil, false
		}
		return string(data), true
	}
	return nil, false
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
	}
	return nil, false
}

func evalMethod(method string, recv any, args []any) (any, bool) {
	// Try as instance method with runtime type
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
	if v, ok := evalQualifiedMethod(typeName+"."+method, allArgs); ok {
		return v, true
	}

	// Legacy built-in methods
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
		if s, ok := recv.(string); ok && len(args) == 1 {
			if sub, ok := args[0].(string); ok {
				return strings.Contains(s, sub), true
			}
		}
	case "startsWith":
		if s, ok := recv.(string); ok && len(args) == 1 {
			if prefix, ok := args[0].(string); ok {
				return strings.HasPrefix(s, prefix), true
			}
		}
	case "endsWith":
		if s, ok := recv.(string); ok && len(args) == 1 {
			if suffix, ok := args[0].(string); ok {
				return strings.HasSuffix(s, suffix), true
			}
		}
	}
	return nil, false
}

func evalQualifiedMethod(qualName string, args []any) (any, bool) {
	switch qualName {
	// int
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
	// float
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
	case "float.pow":
		x, xok := toFloat(args[0])
		y, yok := toFloat(args[1])
		if xok && yok {
			return math.Pow(x, y), true
		}
	case "float.sin":
		x, ok := toFloat(args[0])
		if ok {
			return math.Sin(x), true
		}
	case "float.cos":
		x, ok := toFloat(args[0])
		if ok {
			return math.Cos(x), true
		}
	case "float.tan":
		x, ok := toFloat(args[0])
		if ok {
			return math.Tan(x), true
		}
	case "float.asin":
		x, ok := toFloat(args[0])
		if ok {
			return math.Asin(x), true
		}
	case "float.acos":
		x, ok := toFloat(args[0])
		if ok {
			return math.Acos(x), true
		}
	case "float.atan":
		x, ok := toFloat(args[0])
		if ok {
			return math.Atan(x), true
		}
	case "float.atan2":
		y, yok := toFloat(args[0])
		x, xok := toFloat(args[1])
		if yok && xok {
			return math.Atan2(y, x), true
		}
	// string
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
	case "string.replace":
		s, sok := args[0].(string)
		old, ook := args[1].(string)
		new_, nok := args[2].(string)
		if sok && ook && nok {
			return strings.ReplaceAll(s, old, new_), true
		}
	case "string.indexOf":
		s, sok := args[0].(string)
		sub, subok := args[1].(string)
		if sok && subok {
			return strings.Index(s, sub), true
		}
	case "string.substring":
		s, sok := args[0].(string)
		start, staok := toInt(args[1])
		end, endok := toInt(args[2])
		if sok && staok && endok {
			if start < 0 {
				start = 0
			}
			if end > len(s) {
				end = len(s)
			}
			if start > end {
				return "", true
			}
			return s[start:end], true
		}
	}
	return nil, false
}
