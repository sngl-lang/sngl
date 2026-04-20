package javascript

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

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string          { return "js" }
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("javascript: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("javascript: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("javascript: WriteType not yet implemented")
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
		return t.TypeToNative(hint[7:]) // JS has no option types; everything is nullable
	}
	switch hint {
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
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
	return name
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
		// Integer division in JS needs Math.trunc
		if n.Op == ast.BinDiv && isIntNode(n.Left) && isIntNode(n.Right) {
			return "Math.trunc(" + left + " / " + right + ")"
		}
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
		return "(" + cond + " ? " + a + " : " + b + ")"
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
				parts = append(parts, "..."+translateExpr(f.Value, scope))
			} else {
				parts = append(parts, f.Name+": "+translateExpr(f.Value, scope))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = translateExpr(el, scope)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.SpreadExpr:
		return "..." + translateExpr(n.Operand, scope)
	case *ast.InterpolationExpr:
		// Use template literals
		var sb strings.Builder
		sb.WriteByte('`')
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && (lit.Kind == ast.LiteralStringQuoted || lit.Kind == ast.LiteralStringBackticked || lit.Kind == ast.LiteralStringTrippleQuoted) {
				// Raw includes quotes; strip them for template literal content
				raw := lit.Raw
				raw = strings.TrimPrefix(raw, `"`)
				raw = strings.TrimSuffix(raw, `"`)
				raw = strings.TrimPrefix(raw, "`")
				raw = strings.TrimSuffix(raw, "`")
				raw = strings.TrimPrefix(raw, `"""`)
				raw = strings.TrimSuffix(raw, `"""`)
				sb.WriteString(raw)
			} else {
				sb.WriteString("${")
				sb.WriteString(translateExpr(p, scope))
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('`')
		return sb.String()
	case *ast.ElementRefExpr:
		return fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", n.Name)
	case *ast.StmtBlock:
		var stmts []string
		for _, s := range n.Stmts {
			stmts = append(stmts, translateMutation(s, scope)...)
		}
		return strings.Join(stmts, "\n")
	case *ast.LambdaExpr:
		body := translateExpr(n.Body, scope)
		params := make([]string, len(n.Params.Params))
		for i, p := range n.Params.Params {
			params[i] = p.Name
		}
		if len(params) == 1 {
			return params[0] + " => " + body
		}
		return "(" + strings.Join(params, ", ") + ") => " + body
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
	case ast.LiteralInt, ast.LiteralFloat:
		return n.Raw
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
		return "$" + name + "()"
	}
	if scope.ModelFields[name] {
		return "state." + name
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
		if scope.NeededHelpers != nil {
			scope.NeededHelpers["String"] = true
		}
		return "String(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "Math.trunc(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "parseFloat(" + translateExpr(args[0], scope) + ")"
	}
	if fn == "regex" && len(args) == 1 {
		return "new RegExp(" + translateExpr(args[0], scope) + ")"
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
	if js := jsBuiltinMethod(sel, args, scope); js != "" {
		return js
	}
	// Type-qualified call: int.double(5) → int_double(5)
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName := ident.Name + "." + method
		if scope.FuncNames[qualName] {
			jsName := strings.ReplaceAll(qualName, ".", "_")
			argStrs := make([]string, len(args))
			for i, a := range args {
				argStrs[i] = translateExpr(a, scope)
			}
			return jsName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}
	// Instance method: x.double() → look for type_method(x, ...)
	if scope.FuncNames != nil {
		for qualName := range scope.FuncNames {
			if _, m, ok := ast.SplitMethodName(qualName); ok && m == method {
				jsName := strings.ReplaceAll(qualName, ".", "_")
				argStrs := []string{translateExpr(sel.Operand, scope)}
				for _, a := range args {
					argStrs = append(argStrs, translateExpr(a, scope))
				}
				return jsName + "(" + strings.Join(argStrs, ", ") + ")"
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
	case *ast.CallStmt:
		if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
			args := extractArgs(n.Call.Args)
			method := sel.Field
			// Handle mutation methods: push, remove
			switch method {
			case "push":
				if len(args) == 1 {
					target := translateMutationTarget(sel.Operand, scope)
					value := translateExpr(args[0], scope)
					return []string{target + ".push(" + value + ")"}
				}
			case "remove":
				if len(args) == 1 {
					target := translateMutationTarget(sel.Operand, scope)
					idx := translateExpr(args[0], scope)
					return []string{target + ".splice(" + idx + ", 1)"}
				}
			}
		}
		return []string{translateCall(n.Call, scope)}
	case *ast.EmitStmt:
		args := extractArgs(n.Args)
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = translateExpr(a, scope)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

func translateMutationTarget(e ast.Expr, scope *codegen.ExprScope) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if scope.ModelFields[n.Name] {
			return "state." + n.Name
		}
		if scope.LocalVars[n.Name] && scope.Renames != nil {
			if renamed, ok := scope.Renames[n.Name]; ok {
				return renamed
			}
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
		return "==="
	case ast.BinNeq:
		return "!=="
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

// isIntNode reports whether a SNGL node is known to produce an integer value.
func isIntNode(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return n.Kind == ast.LiteralInt
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.IdentExpr); ok {
			return ident.Name == "int"
		}
		return false
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinAdd, ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
			return isIntNode(n.Left) && isIntNode(n.Right)
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNeg {
			return isIntNode(n.Operand)
		}
	case *ast.ParenExpr:
		return isIntNode(n.Inner)
	}
	return false
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

// jsBuiltinMethod returns a native JS expression for stdlib methods, or "" if not a stdlib method.
// sel is the SelectExpr (receiver.method), args are the already-extracted argument expressions.
func jsBuiltinMethod(sel *ast.SelectExpr, args []ast.Expr, scope *codegen.ExprScope) string {
	method := sel.Field
	var qualName string
	var argExprs []string

	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName = ident.Name + "." + method
		for _, a := range args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
	} else {
		// Instance method: receiver becomes first arg
		argExprs = append(argExprs, translateExpr(sel.Operand, scope))
		for _, a := range args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
		// TODO: resolve receiver type from IR
		qualName = "*." + method
	}

	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "undefined"
	}

	switch qualName {
	// int
	case "int.min", "*.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "Math.abs(" + a(0) + ")"
	case "int.clamp", "*.clamp":
		return "Math.min(Math.max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	// float
	case "float.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "float.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "float.abs":
		return "Math.abs(" + a(0) + ")"
	case "float.clamp":
		return "Math.min(Math.max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	case "float.floor", "*.floor":
		return "Math.floor(" + a(0) + ")"
	case "float.ceil", "*.ceil":
		return "Math.ceil(" + a(0) + ")"
	case "float.round", "*.round":
		return "Math.round(" + a(0) + ")"
	case "float.sqrt", "*.sqrt":
		return "Math.sqrt(" + a(0) + ")"
	case "float.pow", "*.pow":
		return "Math.pow(" + a(0) + ", " + a(1) + ")"
	case "float.sin", "*.sin":
		return "Math.sin(" + a(0) + ")"
	case "float.cos", "*.cos":
		return "Math.cos(" + a(0) + ")"
	case "float.tan", "*.tan":
		return "Math.tan(" + a(0) + ")"
	case "float.asin", "*.asin":
		return "Math.asin(" + a(0) + ")"
	case "float.acos", "*.acos":
		return "Math.acos(" + a(0) + ")"
	case "float.atan", "*.atan":
		return "Math.atan(" + a(0) + ")"
	case "float.atan2", "*.atan2":
		return "Math.atan2(" + a(0) + ", " + a(1) + ")"
	// string
	case "string.length", "*.length":
		return a(0) + ".length"
	case "string.upper", "*.upper":
		return a(0) + ".toUpperCase()"
	case "string.lower", "*.lower":
		return a(0) + ".toLowerCase()"
	case "string.trim", "*.trim":
		return a(0) + ".trim()"
	case "string.replace", "*.replace":
		return a(0) + ".replaceAll(" + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "string.substring", "*.substring":
		return a(0) + ".substring(" + a(1) + ", " + a(2) + ")"
	// color
	case "color.rgb":
		return `"#" + (` + a(0) + `).toString(16).padStart(2, "0") + (` + a(1) + `).toString(16).padStart(2, "0") + (` + a(2) + `).toString(16).padStart(2, "0")`
	case "color.rgba":
		return `"#" + (` + a(0) + `).toString(16).padStart(2, "0") + (` + a(1) + `).toString(16).padStart(2, "0") + (` + a(2) + `).toString(16).padStart(2, "0") + Math.round(` + a(3) + ` * 255).toString(16).padStart(2, "0")`
	// list
	case "list.length":
		return a(0) + ".length"
	case "list.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "list.join", "*.join":
		return a(0) + ".join(" + a(1) + ")"
	case "list.reverse", "*.reverse":
		return "[..." + a(0) + "].reverse()"
	case "list.slice", "*.slice":
		return a(0) + ".slice(" + a(1) + ", " + a(2) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	// regex
	case "regex.matches":
		return a(0) + ".test(" + a(1) + ")"
	case "regex.find", "*.find":
		return "(" + a(0) + ".exec(" + a(1) + ") || [\"\"])[0]"
	// Alert
	case "Alert.toast":
		return `(function(){var d=document.createElement("div");d.textContent=` + a(0) + `;d.style.cssText="position:fixed;bottom:16px;left:50%;transform:translateX(-50%);padding:12px 24px;border-radius:8px;color:#fff;z-index:9999;background:#333";document.body.appendChild(d);setTimeout(function(){d.remove()},3000)})()`
	case "Alert.info":
		return `alert(` + a(0) + `)`
	case "Alert.warn":
		return `alert("Warning: " + ` + a(0) + `)`
	case "Alert.error":
		return `alert("Error: " + ` + a(0) + `)`
	case "Alert.confirm":
		return `confirm(` + a(0) + `)`
	// File
	case "File.pick":
		return `prompt("Enter file path:", "")`
	case "File.pickFolder":
		return `prompt("Enter folder path:", "")`
	}
	return ""
}
