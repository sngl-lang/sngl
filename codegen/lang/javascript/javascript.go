package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) Lang() string      { return "js" }
func (t *Translator) PkgSource() string { return "" }

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

func translateExpr(e ast.Node, scope *codegen.ExprScope) string {
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
	case *ast.MethodExpr:
		// Stdlib native override: emit native JS instead of user-func call
		if js := jsBuiltinMethod(n, scope); js != "" {
			return js
		}
		// Type-qualified call: int.double(5) → int_double(5)
		if ident, ok := n.Receiver.(*ast.IdentExpr); ok {
			qualName := ident.Name + "." + n.Method
			if scope.FuncNames[qualName] {
				jsName := strings.ReplaceAll(qualName, ".", "_")
				argStrs := make([]string, len(n.Args))
				for i, a := range n.Args {
					argStrs[i] = translateExpr(a, scope)
				}
				return jsName + "(" + strings.Join(argStrs, ", ") + ")"
			}
		}
		// Instance method: x.double() → look for type_method(x, ...)
		if scope.FuncNames != nil {
			for qualName := range scope.FuncNames {
				if _, method, ok := ast.SplitMethodName(qualName); ok && method == n.Method {
					jsName := strings.ReplaceAll(qualName, ".", "_")
					argStrs := []string{translateExpr(n.Receiver, scope)}
					for _, a := range n.Args {
						argStrs = append(argStrs, translateExpr(a, scope))
					}
					return jsName + "(" + strings.Join(argStrs, ", ") + ")"
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
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				fmt.Fprintf(&sb, "%v", lit.Value)
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
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	case *ast.AssignStmt:
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	case *ast.ToggleStmt:
		stmts := translateMutation(n, scope)
		return strings.Join(stmts, "\n")
	case *ast.CallStmt:
		return translateCall(n.Call, scope)
	case *ast.LambdaExpr:
		body := translateExpr(n.Body, scope)
		if len(n.Params) == 1 {
			return n.Params[0] + " => " + body
		}
		return "(" + strings.Join(n.Params, ", ") + ") => " + body
	default:
		return fmt.Sprintf("/* unsupported node %T */null", e)
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
		return "null"
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
		return "$" + name + "()"
	}
	if scope.ModelFields[name] {
		return "state." + name
	}
	return name
}

func translateCall(n *ast.CallExpr, scope *codegen.ExprScope) string {
	fn := n.Func
	args := n.Args

	if fn == "string" && len(args) == 1 {
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
		if js := jsBuiltinMethod(n, scope); js != "" {
			return []string{js}
		}
		target := translateMutationTarget(n.Receiver, scope)
		switch n.Method {
		case "push":
			if len(n.Args) == 1 {
				value := translateExpr(n.Args[0], scope)
				return []string{target + ".push(" + value + ")"}
			}
		case "remove":
			if len(n.Args) == 1 {
				idx := translateExpr(n.Args[0], scope)
				return []string{target + ".splice(" + idx + ", 1)"}
			}
		}
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
			return "state." + n.Name
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
func isIntNode(e ast.Node) bool {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return n.Kind == ast.LiteralInt
	case *ast.CallExpr:
		return n.Func == "int"
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinAdd, ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
			return isIntNode(n.Left) && isIntNode(n.Right)
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNeg {
			return isIntNode(n.Operand)
		}
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
func jsBuiltinMethod(n *ast.MethodExpr, scope *codegen.ExprScope) string {
	// Determine the qualified name and argument expressions
	var qualName string
	var argExprs []string

	if ident, ok := n.Receiver.(*ast.IdentExpr); ok {
		qualName = ident.Name + "." + n.Method
		for _, a := range n.Args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
	} else {
		// Instance method: receiver becomes first arg
		argExprs = append(argExprs, translateExpr(n.Receiver, scope))
		for _, a := range n.Args {
			argExprs = append(argExprs, translateExpr(a, scope))
		}
		if n.Resolved != "" {
			qualName = n.Resolved
		} else {
			qualName = "*." + n.Method
		}
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
