package golang

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for Go.
type Translator struct{}

func (t *Translator) Lang() string { return "go" }

func (t *Translator) TranslateExpr(e ast.Node, scope *codegen.ExprScope) string {
	return translateExpr(e, scope)
}

func (t *Translator) TranslateMutation(e ast.Node, scope *codegen.ExprScope) []string {
	return translateMutation(e, scope)
}

func (t *Translator) TranslateLiteral(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return `""`
}

func (t *Translator) TypeToNative(hint string) string {
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
	if name == "" {
		return name
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func translateExpr(e ast.Node, scope *codegen.ExprScope) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return translateLiteral(n)
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
		return operand + "." + n.Field
	case *ast.IndexExpr:
		operand := translateExpr(n.Operand, scope)
		index := translateExpr(n.Index, scope)
		return operand + "[" + index + "]"
	case *ast.CallExpr:
		return translateCall(n, scope)
	case *ast.MethodExpr:
		// Stdlib native override
		if goCode := goBuiltinMethod(n, scope); goCode != "" {
			return goCode
		}
		// Type-qualified call: int.double(5) → IntDouble(5)
		if ident, ok := n.Receiver.(*ast.IdentExpr); ok {
			qualName := ident.Name + "." + n.Method
			if scope.FuncNames[qualName] {
				goName := exportName(ident.Name) + exportName(n.Method)
				argStrs := make([]string, len(n.Args))
				for i, a := range n.Args {
					argStrs[i] = translateExpr(a, scope)
				}
				return goName + "(" + strings.Join(argStrs, ", ") + ")"
			}
		}
		// Instance method: x.double() → look for TypeMethod(x, ...)
		if scope.FuncNames != nil {
			for qualName := range scope.FuncNames {
				if typeName, method, ok := ast.SplitMethodName(qualName); ok && method == n.Method {
					goName := exportName(typeName) + exportName(method)
					argStrs := []string{translateExpr(n.Receiver, scope)}
					for _, a := range n.Args {
						argStrs = append(argStrs, translateExpr(a, scope))
					}
					return goName + "(" + strings.Join(argStrs, ", ") + ")"
				}
			}
		}
		target := translateExpr(n.Receiver, scope)
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateExpr(a, scope)
		}
		return target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"
	case *ast.StructExpr:
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, exportName(f.Name)+": "+translateExpr(f.Value, scope))
		}
		return exportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = translateExpr(el, scope)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case *ast.InterpolationExpr:
		// Interpolation in Go uses fmt.Sprintf
		var fmtStr strings.Builder
		var fmtArgs []string
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				fmtStr.WriteString(fmt.Sprintf("%v", lit.Value))
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
	case *ast.StmtBlock:
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	case *ast.AssignStmt:
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	case *ast.ToggleStmt:
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	default:
		return fmt.Sprintf("/* unsupported node %T */nil", e)
	}
}

func translateLiteral(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralString:
		return fmt.Sprintf("%q", n.Value)
	case ast.LiteralInt:
		return fmt.Sprintf("%d", n.Value)
	case ast.LiteralFloat:
		return fmt.Sprintf("%v", n.Value)
	case ast.LiteralBool:
		if n.Value.(bool) {
			return "true"
		}
		return "false"
	case ast.LiteralNull:
		return "nil"
	case ast.LiteralColor:
		return fmt.Sprintf("%q", n.Value)
	case ast.LiteralUnit:
		return fmt.Sprintf("%q", n.Value)
	default:
		return fmt.Sprintf("%v", n.Value)
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
		return "m." + exportName(name)
	}
	return name
}

func translateCall(n *ast.CallExpr, scope *codegen.ExprScope) string {
	fn := n.Func
	args := n.Args

	if fn == "string" && len(args) == 1 {
		return "fmt.Sprint(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "size" && len(args) == 1 {
		return "len(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "int(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "float64(" + translateExpr(args[0], scope) + ")"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = translateExpr(a, scope)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateMutation(e ast.Node, scope *codegen.ExprScope) []string {
	switch n := e.(type) {
	case *ast.StmtBlock:
		var stmts []string
		for _, s := range n.Stmts {
			stmts = append(stmts, translateMutation(s, scope)...)
		}
		return stmts
	case *ast.AssignStmt:
		target := translateMutationTarget(n.Target, scope)
		value := translateExpr(n.Value, scope)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ast.ToggleStmt:
		target := translateMutationTarget(n.Target, scope)
		return []string{target + " = !" + target}
	case *ast.MethodExpr:
		target := translateMutationTarget(n.Receiver, scope)
		switch n.Method {
		case "push":
			if len(n.Args) == 1 {
				value := translateExpr(n.Args[0], scope)
				return []string{target + " = append(" + target + ", " + value + ")"}
			}
		case "remove":
			if len(n.Args) == 1 {
				idx := translateExpr(n.Args[0], scope)
				return []string{target + " = append(" + target + "[:" + idx + "], " + target + "[" + idx + "+1:]...)"}
			}
		}
		// Generic method call
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateExpr(a, scope)
		}
		return []string{target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"}
	case *ast.EmitStmt:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateExpr(a, scope)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ast.CallStmt:
		return []string{translateCall(n.Call, scope)}
	case *ast.CallExpr:
		return []string{translateCall(n, scope)}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

func translateMutationTarget(e ast.Node, scope *codegen.ExprScope) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if scope.ModelFields[n.Name] {
			return "m." + exportName(n.Name)
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

func exportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// goBuiltinMethod returns native Go code for stdlib methods, or "" if not a stdlib method.
func goBuiltinMethod(n *ast.MethodExpr, scope *codegen.ExprScope) string {
	var qualName string
	var argExprs []string

	if ident, ok := n.Receiver.(*ast.IdentExpr); ok {
		qualName = ident.Name + "." + n.Method
		for _, a := range n.Args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
	} else {
		argExprs = append(argExprs, translateExpr(n.Receiver, scope))
		for _, a := range n.Args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
		qualName = "*." + n.Method
	}

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
	case "list.join", "*.join":
		return "strings.Join(" + a(0) + ", " + a(1) + ")"
	}
	return ""
}
