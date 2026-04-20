package kotlin

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

// Translator implements codegen.LangTranslator for Kotlin.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string          { return "kotlin" }
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("kotlin: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("kotlin: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("kotlin: WriteType not yet implemented")
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
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

func (t *Translator) TypeToNative(hint string) string {
	if strings.HasPrefix(hint, "option:") {
		return t.TypeToNative(hint[7:]) + "?"
	}
	switch hint {
	case "int":
		return "Int"
	case "float":
		return "Double"
	case "bool":
		return "Boolean"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "String"
	default:
		return "Any"
	}
}

func (t *Translator) ExportName(name string) string {
	return name
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

func translateExpr(e ast.Expr, scope *codegen.ExprScope) string {
	if e == nil {
		return "null"
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
		return "(if (" + cond + ") " + a + " else " + b + ")"
	case *ast.SelectExpr:
		operand := translateExpr(n.Operand, scope)
		return operand + "." + n.Field
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
				// Kotlin: spread struct -> .copy() pattern (handled at higher level)
				parts = append(parts, "/* ..."+translateExpr(f.Value, scope)+" */")
			} else {
				parts = append(parts, f.Name+" = "+translateExpr(f.Value, scope))
			}
		}
		return exportName(n.Name) + "(" + strings.Join(parts, ", ") + ")"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = translateExpr(el, scope)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ast.SpreadExpr:
		return "*" + translateExpr(n.Operand, scope)
	case *ast.InterpolationExpr:
		var sb strings.Builder
		sb.WriteByte('"')
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && (lit.Kind == ast.LiteralStringQuoted || lit.Kind == ast.LiteralStringBackticked || lit.Kind == ast.LiteralStringTrippleQuoted) {
				// Raw includes surrounding quotes; strip them.
				s := lit.Raw
				if len(s) >= 2 {
					s = s[1 : len(s)-1]
				}
				s = strings.ReplaceAll(s, "\\", "\\\\")
				s = strings.ReplaceAll(s, "\"", "\\\"")
				s = strings.ReplaceAll(s, "$", "\\$")
				sb.WriteString(s)
			} else {
				sb.WriteString("${")
				sb.WriteString(translateExpr(p, scope))
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('"')
		return sb.String()
	case *ast.ElementRefExpr:
		return fmt.Sprintf("/* elementRef(%q) */null", n.Name)
	case *ast.LambdaExpr:
		params := make([]string, len(n.Params.Params))
		for i, p := range n.Params.Params {
			params[i] = p.Name
		}
		body := translateExpr(n.Body, scope)
		return "{ " + strings.Join(params, ", ") + " -> " + body + " }"
	case *ast.ParenExpr:
		return "(" + translateExpr(n.Inner, scope) + ")"
	default:
		return fmt.Sprintf("/* unsupported node %T */null", e)
	}
}

func translateLiteral(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		return fmt.Sprintf("%q", n.Raw)
	case ast.LiteralInt:
		return n.Raw
	case ast.LiteralFloat:
		s := n.Raw
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case ast.LiteralBool:
		return n.Raw
	case ast.LiteralNull:
		return "null"
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
		return name
	}
	if scope.ModelFields[name] {
		return name
	}
	return name
}

func translateCall(n *ast.CallExpr, scope *codegen.ExprScope) string {
	// Method call: receiver.method(args) -- Func is a *ast.SelectExpr
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
		return translateExpr(args[0], scope) + ".toString()"
	}
	if fn == "int" && len(args) == 1 {
		return translateExpr(args[0], scope) + ".toInt()"
	}
	if fn == "float" && len(args) == 1 {
		return translateExpr(args[0], scope) + ".toDouble()"
	}
	if fn == "regex" && len(args) == 1 {
		return "Regex(" + translateExpr(args[0], scope) + ")"
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
	if kt := kotlinBuiltinMethod(sel, args, scope); kt != "" {
		return kt
	}
	// Type-qualified call: int.double(5) -> intDouble(5)
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName := ident.Name + "." + method
		if scope.FuncNames[qualName] {
			ktName := ident.Name + strings.ToUpper(method[:1]) + method[1:]
			argStrs := make([]string, len(args))
			for i, a := range args {
				argStrs[i] = translateExpr(a, scope)
			}
			return ktName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}
	// Instance method: x.double() -> look for typeMethod(x, ...)
	if scope.FuncNames != nil {
		for qualName := range scope.FuncNames {
			if typeName, m, ok := ast.SplitMethodName(qualName); ok && m == method {
				ktName := typeName + strings.ToUpper(m[:1]) + m[1:]
				argStrs := []string{translateExpr(sel.Operand, scope)}
				for _, a := range args {
					argStrs = append(argStrs, translateExpr(a, scope))
				}
				return ktName + "(" + strings.Join(argStrs, ", ") + ")"
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
	case *ast.CallStmt:
		if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
			args := extractArgs(n.Call.Args)
			method := sel.Field
			// Stdlib native override for mutations
			if kt := kotlinBuiltinMethod(sel, args, scope); kt != "" {
				return []string{kt}
			}
			target := translateMutationTarget(sel.Operand, scope)
			switch method {
			case "push":
				if len(args) == 1 {
					value := translateExpr(args[0], scope)
					return []string{target + ".add(" + value + ")"}
				}
			case "remove":
				if len(args) == 1 {
					idx := translateExpr(args[0], scope)
					return []string{target + ".removeAt(" + idx + ")"}
				}
			}
			argStrs := make([]string, len(args))
			for i, a := range args {
				argStrs[i] = translateExpr(a, scope)
			}
			return []string{target + "." + method + "(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{translateCall(n.Call, scope)}
	case *ast.EmitStmt:
		args := extractArgs(n.Args)
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = translateExpr(a, scope)
		}
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

func translateMutationTarget(e ast.Expr, scope *codegen.ExprScope) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
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
	return strings.ToUpper(s[:1]) + s[1:]
}

// kotlinBuiltinMethod returns native Kotlin code for stdlib methods, or "" if not a stdlib method.
// sel is the SelectExpr (receiver.method), args are the already-extracted argument expressions.
func kotlinBuiltinMethod(sel *ast.SelectExpr, args []ast.Expr, scope *codegen.ExprScope) string {
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

	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "null"
	}

	switch qualName {
	// int
	case "int.min", "*.min":
		return "minOf(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "maxOf(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "kotlin.math.abs(" + a(0) + ")"
	case "int.clamp", "*.clamp":
		return a(0) + ".coerceIn(" + a(1) + ", " + a(2) + ")"
	// float
	case "float.min":
		return "minOf(" + a(0) + ", " + a(1) + ")"
	case "float.max":
		return "maxOf(" + a(0) + ", " + a(1) + ")"
	case "float.abs":
		return "kotlin.math.abs(" + a(0) + ")"
	case "float.clamp":
		return a(0) + ".coerceIn(" + a(1) + ", " + a(2) + ")"
	case "float.floor", "*.floor":
		return "kotlin.math.floor(" + a(0) + ").toInt()"
	case "float.ceil", "*.ceil":
		return "kotlin.math.ceil(" + a(0) + ").toInt()"
	case "float.round", "*.round":
		return "kotlin.math.round(" + a(0) + ").toInt()"
	case "float.sqrt", "*.sqrt":
		return "kotlin.math.sqrt(" + a(0) + ")"
	case "float.pow", "*.pow":
		return a(0) + ".pow(" + a(1) + ")"
	case "float.sin", "*.sin":
		return "kotlin.math.sin(" + a(0) + ")"
	case "float.cos", "*.cos":
		return "kotlin.math.cos(" + a(0) + ")"
	case "float.tan", "*.tan":
		return "kotlin.math.tan(" + a(0) + ")"
	case "float.asin", "*.asin":
		return "kotlin.math.asin(" + a(0) + ")"
	case "float.acos", "*.acos":
		return "kotlin.math.acos(" + a(0) + ")"
	case "float.atan", "*.atan":
		return "kotlin.math.atan(" + a(0) + ")"
	case "float.atan2", "*.atan2":
		return "kotlin.math.atan2(" + a(0) + ", " + a(1) + ")"
	// string
	case "string.length", "*.length":
		return a(0) + ".length"
	case "string.upper", "*.upper":
		return a(0) + ".uppercase()"
	case "string.lower", "*.lower":
		return a(0) + ".lowercase()"
	case "string.trim", "*.trim":
		return a(0) + ".trim()"
	case "string.replace", "*.replace":
		return a(0) + ".replace(" + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "string.substring", "*.substring":
		return a(0) + ".substring(" + a(1) + ", " + a(2) + ")"
	case "string.contains":
		return a(0) + ".contains(" + a(1) + ")"
	case "string.startsWith", "*.startsWith":
		return a(0) + ".startsWith(" + a(1) + ")"
	case "string.endsWith", "*.endsWith":
		return a(0) + ".endsWith(" + a(1) + ")"
	// list
	case "list.length":
		return a(0) + ".size"
	case "list.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "list.join", "*.join":
		return a(0) + ".joinToString(" + a(1) + ")"
	case "list.reverse", "*.reverse":
		return a(0) + ".reversed()"
	case "list.slice", "*.slice":
		return a(0) + ".subList(" + a(1) + ", " + a(2) + ")"
	case "list.contains":
		return a(0) + ".contains(" + a(1) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	// color
	case "color.rgb":
		return "String.format(\"#%02x%02x%02x\", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "color.rgba":
		return "String.format(\"#%02x%02x%02x%02x\", " + a(0) + ", " + a(1) + ", " + a(2) + ", (" + a(3) + " * 255).toInt())"
	// regex
	case "regex.matches":
		return a(0) + ".containsMatchIn(" + a(1) + ")"
	case "regex.find", "*.find":
		return "(" + a(0) + ".find(" + a(1) + ")?.value ?: \"\")"
	// Alert
	case "Alert.toast":
		return `Toast.makeText(context, ` + a(0) + `, Toast.LENGTH_SHORT).show()`
	case "Alert.info":
		return `android.app.AlertDialog.Builder(context).setMessage(` + a(0) + `).setPositiveButton("OK", null).show()`
	case "Alert.warn":
		return `android.app.AlertDialog.Builder(context).setTitle("Warning").setMessage(` + a(0) + `).setPositiveButton("OK", null).show()`
	case "Alert.error":
		return `android.app.AlertDialog.Builder(context).setTitle("Error").setMessage(` + a(0) + `).setPositiveButton("OK", null).show()`
	case "Alert.confirm":
		return `true`
	// File
	case "File.pick":
		return `""`
	case "File.pickFolder":
		return `""`
	}

	// For ident receivers (e.g., notes.length), retry with wildcard if no direct match.
	if _, ok := sel.Operand.(*ast.IdentExpr); ok && !strings.HasPrefix(qualName, "*.") {
		wildcard := "*." + method
		// Rebuild with receiver as a(0).
		recv := translateExpr(sel.Operand, scope)
		wa := func(i int) string {
			if i == 0 {
				return recv
			}
			if i-1 < len(argExprs) {
				return argExprs[i-1]
			}
			return "null"
		}
		switch wildcard {
		case "*.length":
			return wa(0) + ".length"
		case "*.upper":
			return wa(0) + ".uppercase()"
		case "*.lower":
			return wa(0) + ".lowercase()"
		case "*.trim":
			return wa(0) + ".trim()"
		case "*.replace":
			return wa(0) + ".replace(" + wa(1) + ", " + wa(2) + ")"
		case "*.indexOf":
			return wa(0) + ".indexOf(" + wa(1) + ")"
		case "*.startsWith":
			return wa(0) + ".startsWith(" + wa(1) + ")"
		case "*.endsWith":
			return wa(0) + ".endsWith(" + wa(1) + ")"
		case "*.join":
			return wa(0) + ".joinToString(" + wa(1) + ")"
		}
	}

	return ""
}
