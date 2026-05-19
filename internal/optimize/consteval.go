package optimize

import (
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/asset"
	"git.duckfam.us/jonathan/sngl/ir"
)

// isConstExpr reports whether the expression can be evaluated at compile time.
func isConstExpr(e ir.Expr, ctx *evalCtx) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		switch x.Name {
		case "PLATFORM", "LANGUAGE":
			return true
		}
		if v, ok := x.Sym.(*ir.Var); ok && v.IsConst {
			return true
		}
		if _, ok := x.Sym.(*ir.Namespace); ok {
			return true // namespace refs are compile-time resolvable
		}
		// Any symbol bound by a parent context (loop vars during expansion,
		// component params during call-site inlining) becomes a const for
		// the duration of that scope.
		if x.Sym != nil {
			if _, found := ctx.values[x.Sym]; found {
				return true
			}
		}
		return false
	case *ir.Binary:
		return isConstExpr(x.Left, ctx) && isConstExpr(x.Right, ctx)
	case *ir.Unary:
		// Reference operations (&x, *p) are never const: the underlying
		// storage is mutable and may be aliased, so the value at any
		// given moment is not knowable at compile time.
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return false
		}
		return isConstExpr(x.Operand, ctx)
	case *ir.Ternary:
		return isConstExpr(x.Cond, ctx) && isConstExpr(x.Then, ctx) && isConstExpr(x.Else, ctx)
	case *ir.Call:
		for _, a := range x.Args {
			if !isConstExpr(a.Value, ctx) {
				return false
			}
		}
		if x.Receiver != nil && !isConstExpr(x.Receiver, ctx) {
			return false
		}
		if x.Func != nil && x.Func.Purity == ir.PurityPure {
			return true
		}
		// Check native import pure functions.
		return isNativePureCall(x, ctx)
	case *ir.Conversion:
		return isConstExpr(x.Operand, ctx)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if !isConstExpr(el, ctx) {
				return false
			}
		}
		return true
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Value == nil || !isConstExpr(f.Value, ctx) {
				return false
			}
		}
		return true
	case *ir.Select:
		return isConstExpr(x.Operand, ctx)
	case *ir.Index:
		return isConstExpr(x.Operand, ctx) && isConstExpr(x.Idx, ctx)
	default:
		return false
	}
}

// isNativePureCall checks if an unresolved call is to a pure native function.
func isNativePureCall(call *ir.Call, ctx *evalCtx) bool {
	if call.Func != nil {
		return false // already resolved
	}
	// Unresolved calls may be namespace.func() pattern — check via AST fallback.
	if call.AST == nil {
		return false
	}
	sel, ok := call.AST.Func.(*ast.SelectExpr)
	if !ok {
		return false
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	if !ok {
		return false
	}
	nativeImports := ctx.getNativeImports()
	ns, exists := nativeImports[ident.Name]
	if !exists {
		return false
	}
	for _, f := range ns.Funcs {
		if f.Name == sel.Field && f.Purity == ir.PurityPure {
			return true
		}
	}
	return false
}

// evalExpr evaluates a constant expression and returns the Go value.
func evalExpr(e ir.Expr, ctx *evalCtx) (any, bool) {
	if e == nil {
		return nil, false
	}
	if !isConstExpr(e, ctx) {
		return nil, false
	}
	switch x := e.(type) {
	case *ir.Literal:
		val := parseLiteral(x)
		// parseLiteral returns nil for kinds it can't represent (units, etc.);
		// signal non-foldable so the original literal is preserved.
		if val == nil && x.Type != nil && x.Type.Kind != ir.TypeNull {
			return nil, false
		}
		return val, true
	case *ir.Ident:
		return evalIdent(x, ctx)
	case *ir.Binary:
		left, lok := evalExpr(x.Left, ctx)
		right, rok := evalExpr(x.Right, ctx)
		if !lok || !rok {
			return nil, false
		}
		return evalBinaryOp(x.Op, left, right)
	case *ir.Unary:
		operand, ok := evalExpr(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		return evalUnaryOp(x.Op, operand)
	case *ir.Ternary:
		cond, ok := evalExpr(x.Cond, ctx)
		if !ok {
			return nil, false
		}
		if b, ok := cond.(bool); ok {
			if b {
				return evalExpr(x.Then, ctx)
			}
			return evalExpr(x.Else, ctx)
		}
		return nil, false
	case *ir.Call:
		return evalCall(x, ctx)
	case *ir.Conversion:
		return evalConversion(x, ctx)
	case *ir.ListLit:
		result := make([]any, 0, len(x.Elems))
		for _, el := range x.Elems {
			v, ok := evalExpr(el, ctx)
			if !ok {
				return nil, false
			}
			result = append(result, v)
		}
		return result, true
	case *ir.StructLit:
		result := make(map[string]any, len(x.Fields))
		for _, f := range x.Fields {
			if f.Spread || f.Name == "" || f.Value == nil {
				return nil, false
			}
			v, ok := evalExpr(f.Value, ctx)
			if !ok {
				return nil, false
			}
			result[f.Name] = v
		}
		return result, true
	case *ir.Select:
		recv, ok := evalExpr(x.Operand, ctx)
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
	case *ir.Index:
		operand, ok := evalExpr(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		idx, ok := evalExpr(x.Idx, ctx)
		if !ok {
			return nil, false
		}
		if list, ok := operand.([]any); ok {
			if i, ok := toInt(idx); ok && i >= 0 && i < len(list) {
				return list[i], true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

func evalIdent(x *ir.Ident, ctx *evalCtx) (any, bool) {
	switch x.Name {
	case "PLATFORM":
		return ctx.platform, true
	case "LANGUAGE":
		return ctx.language, true
	}
	// Const variable — evaluate its initializer.
	if v, ok := x.Sym.(*ir.Var); ok && v.IsConst {
		if val, found := ctx.values[v]; found {
			return val, true
		}
		if v.Init != nil {
			val, ok := evalExpr(v.Init, ctx)
			if ok {
				ctx.values[v] = val
			}
			return val, ok
		}
	}
	// Loop variable during expansion.
	if val, found := ctx.values[x.Sym]; found {
		return val, true
	}
	return nil, false
}

func evalCall(call *ir.Call, ctx *evalCtx) (any, bool) {
	// Collect argument values.
	args := make([]any, 0, len(call.Args))
	for _, a := range call.Args {
		v, ok := evalExpr(a.Value, ctx)
		if !ok {
			return nil, false
		}
		args = append(args, v)
	}

	// Try the generic SNGL-body interpreter for pure user/stdlib funcs.
	if call.Func != nil && len(call.Func.Block) > 0 && call.Func.Purity == ir.PurityPure {
		ctx.interpDepth++
		v, ok := interpretFunc(call.Func, args, ctx, ctx.interpDepth)
		ctx.interpDepth--
		if ok {
			return v, true
		}
	}

	// Resolved function call. Type-attached methods (including both
	// "x.upper()" and "string.upper(x)" syntaxes) are normalized so that
	// Func.Receiver is set and the receiver value is Args[0].
	if call.Func != nil {
		if call.Func.Receiver != "" {
			qualName := call.Func.Receiver + "." + call.Func.Name
			if v, ok := evalQualifiedMethod(qualName, args); ok {
				return v, true
			}
		}
		// Try builtin function.
		if v, ok := evalCallFunc(call.Func.Name, args); ok {
			return v, true
		}
	}

	// Native import pure function.
	return evalNativeCall(call, args, ctx)
}

func evalNativeCall(call *ir.Call, args []any, ctx *evalCtx) (any, bool) {
	if call.AST == nil {
		return nil, false
	}
	sel, ok := call.AST.Func.(*ast.SelectExpr)
	if !ok {
		return nil, false
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	if !ok {
		return nil, false
	}
	nativeImports := ctx.getNativeImports()
	ns, exists := nativeImports[ident.Name]
	if !exists {
		return nil, false
	}
	qualName := ident.Name + "." + sel.Field

	// Try file:// scheme functions.
	for _, f := range ns.Funcs {
		if f.Name == sel.Field && f.NativePkg == "file" {
			if len(args) == 1 {
				if filename, ok := args[0].(string); ok {
					return evalFileFunc(f.NativeName, ns.ImportPath, filename, ctx)
				}
			}
		}
	}

	if ctx.dir != "" {
		for _, f := range ns.Funcs {
			if f.Name == sel.Field && f.Purity == ir.PurityPure && f.NativePkg != "file" {
				paramTypes := make([]string, len(f.Params))
				for i, p := range f.Params {
					paramTypes[i] = goTypeKindString(p.Type)
				}
				result, err := execPureGoFunc(ctx.dir, ns.ImportPath, f.NativeName, paramTypes, goTypeKindString(f.Return), args)
				if err != nil {
					slog.Debug("pure func eval failed", "func", qualName, "err", err)
					return nil, false
				}
				slog.Debug("pure func eval", "func", qualName, "result_type", fmt.Sprintf("%T", result))
				return result, true
			}
		}
	}
	return nil, false
}

// goTypeKindString reduces an IR type to the primitive hint strings
// execPureGoFunc understands for marshalling args and return values.
func goTypeKindString(t *ir.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeString:
		return "string"
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float"
	case ir.TypeBool:
		return "bool"
	}
	return ""
}

func evalConversion(conv *ir.Conversion, ctx *evalCtx) (any, bool) {
	operand, ok := evalExpr(conv.Operand, ctx)
	if !ok {
		return nil, false
	}
	switch conv.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%v", operand), true
	case ir.TypeInt:
		switch v := operand.(type) {
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
	case ir.TypeFloat:
		switch v := operand.(type) {
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
	case ir.TypeBool:
		if b, ok := operand.(bool); ok {
			return b, true
		}
	case ir.TypeList, ir.TypeStruct, ir.TypeDyn, ir.TypeOption, ir.TypeColor:
		// Compound-type conversions are widening or narrowing within a
		// compatible Go shape ([]any / map[string]any). Pass through.
		return operand, true
	}
	return nil, false
}

// parseLiteral converts an IR literal to a Go value.
func parseLiteral(lit *ir.Literal) any {
	if lit.Type == nil {
		return nil
	}
	switch lit.Type.Kind {
	case ir.TypeBool:
		return lit.Raw == "true"
	case ir.TypeNull:
		return nil
	case ir.TypeInt:
		n, err := strconv.Atoi(lit.Raw)
		if err != nil {
			return nil
		}
		return n
	case ir.TypeFloat:
		f, err := strconv.ParseFloat(lit.Raw, 64)
		if err != nil {
			return 0.0
		}
		return f
	case ir.TypeString:
		return lit.Raw
	case ir.TypeColor:
		return lit.Raw
	}
	return nil
}

// irLiteral converts a Go value back to an IR Literal.
// For strings, Raw stores the unquoted content (the formatter adds %q quoting).
func irLiteral(val any, typ *ir.Type) *ir.Literal {
	switch v := val.(type) {
	case string:
		return &ir.Literal{Type: ir.TypString, Raw: v}
	case int:
		return &ir.Literal{Type: ir.TypInt, Raw: intToStr(v)}
	case float64:
		return &ir.Literal{Type: ir.TypFloat, Raw: floatToStr(v)}
	case bool:
		raw := "false"
		if v {
			raw = "true"
		}
		return &ir.Literal{Type: ir.TypBool, Raw: raw}
	case nil:
		return &ir.Literal{Type: ir.TypNull, Raw: "null"}
	}
	return nil
}

// numericOrNativeEq compares two folded constants. When both sides are
// numeric, comparison happens after promotion to float so that
// `1 == 1.0` folds to true (matching the language's mixed-numeric == rule).
// For non-numeric pairs, it falls back to Go's native ==.
func numericOrNativeEq(left, right any) bool {
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if lok && rok {
		return lf == rf
	}
	return left == right
}

// --- Arithmetic and comparison helpers (operate on Go values) ---

func evalBinaryOp(op ast.BinaryOp, left, right any) (any, bool) {
	switch op {
	case ast.BinEq:
		return numericOrNativeEq(left, right), true
	case ast.BinNeq:
		return !numericOrNativeEq(left, right), true
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
	case []any:
		typeName = "list"
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
	case "float.pow":
		x, xok := toFloat(args[0])
		y, yok := toFloat(args[1])
		if xok && yok {
			return math.Pow(x, y), true
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
	case "string.indexOf":
		s, sok := args[0].(string)
		sub, subok := args[1].(string)
		if sok && subok {
			return strings.Index(s, sub), true
		}
	case "string.substring":
		s, sok := args[0].(string)
		start, stok := toInt(args[1])
		end, eok := toInt(args[2])
		if sok && stok && eok && start >= 0 && end <= len(s) && start <= end {
			return s[start:end], true
		}
	case "string.startsWith":
		s, sok := args[0].(string)
		pre, pok := args[1].(string)
		if sok && pok {
			return strings.HasPrefix(s, pre), true
		}
	case "string.endsWith":
		s, sok := args[0].(string)
		suf, sufok := args[1].(string)
		if sok && sufok {
			return strings.HasSuffix(s, suf), true
		}
	}
	return nil, false
}

// evalFileFunc evaluates file:// scheme functions (path, contents).
func evalFileFunc(funcName, dirPath, filename string, ctx *evalCtx) (any, bool) {
	fsys := os.DirFS(dirPath)

	switch funcName {
	case "path":
		data, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return nil, false
		}
		outRel := filename
		if !ctx.noCacheBust {
			dir, base := path.Split(filename)
			outRel = dir + asset.HashedName(base, data)
		}
		outPath := "assets/" + outRel
		url := "/" + outPath
		ctx.fileAssets = append(ctx.fileAssets, FileAsset{
			SrcPath: filepath.Join(dirPath, filename),
			OutPath: outPath,
			Data:    data,
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

// --- Helper functions ---

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

func intToStr(v int) string {
	return strconv.Itoa(v)
}

func floatToStr(v float64) string {
	return fmt.Sprintf("%v", v)
}
