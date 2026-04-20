package golang

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for Go.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string          { return "go" }
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("golang: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("golang: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("golang: WriteType not yet implemented")
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return ExportName(name.Name)
}

func (t *Translator) Eval(expr ir.Expr) string {
	return fmt.Sprintf("/* eval not implemented: %T */", expr)
}

func (t *Translator) TranslateExpr(e ast.Expr, scope *codegen.ExprScope) string {
	return translateExpr(e, scope)
}

func (t *Translator) TranslateMutation(e ast.Stmt, scope *codegen.ExprScope) []string {
	return translateMutation(e, scope)
}

func (t *Translator) TranslateLiteral(expr ast.Expr) string {
	if lit, ok := expr.(*ast.LiteralExpr); ok {
		return translateLiteral(lit)
	}
	return `""`
}

func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	return translateIRExpr(e, scope)
}

func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return translateIRMutation(s, scope)
}

func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	if lit, ok := e.(*ir.Literal); ok {
		return translateIRLiteral(lit)
	}
	return `""`
}

func (t *Translator) TypeToNative(hint string) string {
	if strings.HasPrefix(hint, "option:") {
		return "*" + t.TypeToNative(hint[7:])
	}
	switch hint {
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "string"
	default:
		return "any"
	}
}

func (t *Translator) ExportName(name string) string {
	return ExportName(name)
}

func translateExpr(e ast.Expr, scope *codegen.ExprScope) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return translateLiteral(n)
	case *ast.UnitLiteral:
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	case *ast.IdentExpr:
		return translateIdent(n, scope)
	case *ast.BinaryExpr:
		left := translateExpr(n.Left, scope)
		right := translateExpr(n.Right, scope)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ast.UnaryExpr:
		operand := translateExpr(n.Operand, scope)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		cond := translateExpr(n.Cond, scope)
		a := translateExpr(n.Then, scope)
		b := translateExpr(n.Else, scope)
		return "ternary(" + cond + ", " + a + ", " + b + ")"
	case *ast.SelectExpr:
		operand := translateExpr(n.Operand, scope)
		if n.Field == "length" {
			return "len(" + operand + ")"
		}
		return operand + "." + ExportName(n.Field)
	case *ast.IndexExpr:
		operand := translateExpr(n.Operand, scope)
		index := translateExpr(n.Index, scope)
		return operand + "[" + index + "]"
	case *ast.CallExpr:
		return translateCall(n, scope)
	case *ast.StructExpr:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				// Go doesn't have spread; handled at a higher level
				parts = append(parts, "/* ..."+translateExpr(f.Value, scope)+" */")
			} else {
				parts = append(parts, ExportName(f.Name)+": "+translateExpr(f.Value, scope))
			}
		}
		return ExportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = translateExpr(el, scope)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case *ast.SpreadExpr:
		return translateExpr(n.Operand, scope) + "..."
	case *ast.InterpolationExpr:
		// Interpolation in Go uses fmt.Sprintf
		var fmtStr strings.Builder
		var fmtArgs []string
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralStringQuoted {
				fmtStr.WriteString(lit.Raw)
			} else {
				fmtStr.WriteString("%v")
				fmtArgs = append(fmtArgs, translateExpr(p, scope))
			}
		}
		if len(fmtArgs) == 0 {
			return fmt.Sprintf("%q", fmtStr.String())
		}
		return "fmt.Sprintf(" + fmt.Sprintf("%q", fmtStr.String()) + ", " + strings.Join(fmtArgs, ", ") + ")"
	case *ast.ElementRefExpr:
		return fmt.Sprintf("elementRef(%q)", n.Name)
	case *ast.LambdaExpr:
		params := make([]string, len(n.Params.Params))
		for i, p := range n.Params.Params {
			params[i] = p.Name + " any"
		}
		body := translateExpr(n.Body, scope)
		return "func(" + strings.Join(params, ", ") + ") any { return " + body + " }"
	case *ast.ParenExpr:
		return "(" + translateExpr(n.Inner, scope) + ")"
	default:
		return fmt.Sprintf("/* unsupported node %T */nil", e)
	}
}

func translateLiteral(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		return fmt.Sprintf("%q", n.Raw)
	case ast.LiteralInt:
		return n.Raw
	case ast.LiteralFloat:
		return n.Raw
	case ast.LiteralBool:
		return n.Raw
	case ast.LiteralNull:
		return "nil"
	case ast.LiteralColor:
		return fmt.Sprintf("%q", n.Raw)
	case ast.LiteralUnit:
		return fmt.Sprintf("%q", n.Raw)
	default:
		return n.Raw
	}
}

func translateIdent(n *ast.IdentExpr, scope *codegen.ExprScope) string {
	name := n.Name
	if name == "event" && scope.EventVar != "" {
		return scope.EventVar
	}
	if scope.LocalVars[name] {
		if scope.Renames != nil {
			if renamed, ok := scope.Renames[name]; ok {
				return renamed
			}
		}
		return name
	}
	if scope.ComputedFields[name] {
		return "m." + name + "()"
	}
	if scope.ModelFields[name] {
		return "m." + ExportName(name)
	}
	return name
}

func translateCall(n *ast.CallExpr, scope *codegen.ExprScope) string {
	// Method call: receiver.method(args) — Func is a *ast.SelectExpr
	if sel, ok := n.Func.(*ast.SelectExpr); ok {
		return translateMethodCall(sel, n.Args, scope)
	}

	// Plain function call: Func is *ast.IdentExpr
	fn := ""
	if ident, ok := n.Func.(*ast.IdentExpr); ok {
		fn = ident.Name
	} else {
		fn = translateExpr(n.Func, scope)
	}

	args := extractArgs(n.Args)

	if fn == "string" && len(args) == 1 {
		return "fmt.Sprint(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "int(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "float64(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "regex" && len(args) == 1 {
		return "regexp.MustCompile(" + translateExpr(args[0], scope) + ")"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = translateExpr(a, scope)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

// translateMethodCall handles a call where Func is a SelectExpr (receiver.method).
func translateMethodCall(sel *ast.SelectExpr, argList ast.ArgList, scope *codegen.ExprScope) string {
	method := sel.Field
	args := extractArgs(argList)

	// Stdlib native override
	if goCode := goBuiltinMethodFromCall(sel, args, scope); goCode != "" {
		return goCode
	}
	// Type-qualified call: int.double(5) → IntDouble(5)
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName := ident.Name + "." + method
		if scope.FuncNames[qualName] {
			goName := ExportName(ident.Name) + ExportName(method)
			argStrs := make([]string, len(args))
			for i, a := range args {
				argStrs[i] = translateExpr(a, scope)
			}
			return goName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}
	// Instance method: x.double() → look for TypeMethod(x, ...)
	if scope.FuncNames != nil {
		for qualName := range scope.FuncNames {
			if typeName, m, ok := ast.SplitMethodName(qualName); ok && m == method {
				goName := ExportName(typeName) + ExportName(m)
				argStrs := []string{translateExpr(sel.Operand, scope)}
				for _, a := range args {
					argStrs = append(argStrs, translateExpr(a, scope))
				}
				return goName + "(" + strings.Join(argStrs, ", ") + ")"
			}
		}
	}
	target := translateExpr(sel.Operand, scope)
	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = translateExpr(a, scope)
	}
	return target + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
}

// extractArgs extracts expression values from an ArgList.
func extractArgs(al ast.ArgList) []ast.Expr {
	var out []ast.Expr
	for _, a := range al.Args {
		if arg, ok := a.(ast.Arg); ok {
			out = append(out, arg.Value)
		}
	}
	return out
}

func translateMutation(e ast.Stmt, scope *codegen.ExprScope) []string {
	switch n := e.(type) {
	case *ast.AssignStmt:
		target := translateMutationTarget(n.Target, scope)
		value := translateExpr(n.Value, scope)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ast.ToggleStmt:
		target := translateMutationTarget(n.Target, scope)
		return []string{target + " = !" + target}
	case *ast.EmitStmt:
		args := extractArgs(n.Args)
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = translateExpr(a, scope)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ast.CallStmt:
		return []string{translateCall(n.Call, scope)}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

func translateMutationTarget(e ast.Expr, scope *codegen.ExprScope) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if scope.ModelFields[n.Name] {
			return "m." + ExportName(n.Name)
		}
		return n.Name
	case *ast.SelectExpr:
		operand := translateMutationTarget(n.Operand, scope)
		return operand + "." + n.Field
	case *ast.IndexExpr:
		operand := translateMutationTarget(n.Operand, scope)
		index := translateExpr(n.Index, scope)
		return operand + "[" + index + "]"
	default:
		return translateExpr(e, scope)
	}
}

func binaryOpStr(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
	case ast.BinEq:
		return "=="
	case ast.BinNeq:
		return "!="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	default:
		return "?"
	}
}

func assignOpStr(op ast.AssignOp) string {
	switch op {
	case ast.AssignSet:
		return "="
	case ast.AssignAdd:
		return "+="
	case ast.AssignSub:
		return "-="
	case ast.AssignMul:
		return "*="
	case ast.AssignDiv:
		return "/="
	case ast.AssignMod:
		return "%="
	default:
		return "="
	}
}

// goBuiltinMethodFromCall returns native Go code for stdlib methods, or "" if not a stdlib method.
// sel is the SelectExpr (receiver.method), args are the already-extracted argument expressions.
func goBuiltinMethodFromCall(sel *ast.SelectExpr, args []ast.Expr, scope *codegen.ExprScope) string {
	method := sel.Field
	var qualName string
	var argExprs []string

	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName = ident.Name + "." + method
		for _, a := range args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
	} else {
		argExprs = append(argExprs, translateExpr(sel.Operand, scope))
		for _, a := range args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
		qualName = "*." + method
	}

	return goBuiltinMethodFromArgs(qualName, argExprs)
}

// goBuiltinMethodFromArgs maps a qualified method name and pre-translated argument
// expressions to native Go code. Returns "" if the method is not a builtin.
// Used by both the standalone translator and GoContext.
func goBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "nil"
	}

	switch qualName {
	case "int.min", "*.min":
		return "min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "func(x int) int { if x < 0 { return -x }; return x }(" + a(0) + ")"
	case "int.clamp", "*.clamp":
		return "min(max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	case "float.min":
		return "math.Min(" + a(0) + ", " + a(1) + ")"
	case "float.max":
		return "math.Max(" + a(0) + ", " + a(1) + ")"
	case "float.abs":
		return "math.Abs(" + a(0) + ")"
	case "float.clamp":
		return "math.Min(math.Max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	case "float.floor", "*.floor":
		return "int(math.Floor(" + a(0) + "))"
	case "float.ceil", "*.ceil":
		return "int(math.Ceil(" + a(0) + "))"
	case "float.round", "*.round":
		return "int(math.Round(" + a(0) + "))"
	case "float.sqrt", "*.sqrt":
		return "math.Sqrt(" + a(0) + ")"
	case "float.pow", "*.pow":
		return "math.Pow(" + a(0) + ", " + a(1) + ")"
	case "float.sin", "*.sin":
		return "math.Sin(" + a(0) + ")"
	case "float.cos", "*.cos":
		return "math.Cos(" + a(0) + ")"
	case "float.tan", "*.tan":
		return "math.Tan(" + a(0) + ")"
	case "float.asin", "*.asin":
		return "math.Asin(" + a(0) + ")"
	case "float.acos", "*.acos":
		return "math.Acos(" + a(0) + ")"
	case "float.atan", "*.atan":
		return "math.Atan(" + a(0) + ")"
	case "float.atan2", "*.atan2":
		return "math.Atan2(" + a(0) + ", " + a(1) + ")"
	case "string.length", "*.length":
		return "len(" + a(0) + ")"
	case "string.upper", "*.upper":
		return "strings.ToUpper(" + a(0) + ")"
	case "string.lower", "*.lower":
		return "strings.ToLower(" + a(0) + ")"
	case "string.trim", "*.trim":
		return "strings.TrimSpace(" + a(0) + ")"
	case "string.replace", "*.replace":
		return "strings.ReplaceAll(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return "strings.Index(" + a(0) + ", " + a(1) + ")"
	case "string.substring", "*.substring":
		return a(0) + "[" + a(1) + ":" + a(2) + "]"
	case "list.length":
		return "len(" + a(0) + ")"
	case "list.join", "*.join":
		return "strings.Join(" + a(0) + ", " + a(1) + ")"
	case "list.filter", "*.filter":
		return "func() []any { var out []any; for _, item := range " + a(0) + " { if " + a(1) + ".(func(any) any)(item).(bool) { out = append(out, item) } }; return out }()"
	case "list.map", "*.map":
		return "func() []any { out := make([]any, len(" + a(0) + ")); for i, item := range " + a(0) + " { out[i] = " + a(1) + ".(func(any) any)(item) }; return out }()"
	// regex
	case "regex.matches":
		return a(0) + ".MatchString(" + a(1) + ")"
	case "regex.find", "*.find":
		return a(0) + ".FindString(" + a(1) + ")"
	// Alert
	case "Alert.toast":
		return `fmt.Println("[" + ` + a(1) + ` + "] " + ` + a(0) + `)`
	case "Alert.info":
		return `fmt.Println("[info] " + ` + a(0) + `)`
	case "Alert.warn":
		return `fmt.Println("[warn] " + ` + a(0) + `)`
	case "Alert.error":
		return `fmt.Println("[error] " + ` + a(0) + `)`
	case "Alert.confirm":
		return `true`
	// File
	case "File.pick":
		return `""`
	case "File.pickFolder":
		return `""`
	}
	return ""
}
