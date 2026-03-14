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

func (t *Translator) Lang() string { return "js" }

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
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idn-email", "idn-hostname", "irl", "irl-reference", "url-reference",
		"url-template", "currency", "country-2", "country-3", "country-subdivision", "decimal":
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
		target := translateExpr(n.Receiver, scope)
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateExpr(a, scope)
		}
		return target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"
	case *ast.StructExpr:
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, f.Name+": "+translateExpr(f.Value, scope))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = translateExpr(el, scope)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.InterpolationExpr:
		// Use template literals
		var sb strings.Builder
		sb.WriteByte('`')
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				sb.WriteString(fmt.Sprintf("%v", lit.Value))
			} else {
				sb.WriteString("${")
				sb.WriteString(translateExpr(p, scope))
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('`')
		return sb.String()
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
	case ast.LiteralDuration:
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
	if fn == "size" && len(args) == 1 {
		return translateExpr(args[0], scope) + ".length"
	}
	if fn == "int" && len(args) == 1 {
		return "Math.trunc(" + translateExpr(args[0], scope) + ")"
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
		return n.Func == "int" || n.Func == "size"
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
