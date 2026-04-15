package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Translator also implements codegen.ExprTranslator.
var _ codegen.ExprTranslator = (*Translator)(nil)

func (t *Translator) Expr(e ast.Expr, ctx *codegen.ExprCtx) string {
	return exprV2(e, ctx)
}

func (t *Translator) Mutation(s ast.Stmt, ctx *codegen.ExprCtx) []string {
	return mutationV2(s, ctx)
}

func (t *Translator) Literal(e ast.Expr) string {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		return translateLiteral(lit)
	}
	return `""`
}

func (t *Translator) TypeName(typ *ir.Type) string {
	if typ == nil {
		return "any"
	}
	switch typ.Kind {
	case ir.TypeInt, ir.TypeFloat:
		return "number"
	case ir.TypeBool:
		return "boolean"
	case ir.TypeString:
		return "string"
	case ir.TypeList:
		return "Array"
	case ir.TypeOption:
		if len(typ.Elems) > 0 {
			return t.TypeName(typ.Elems[0])
		}
		return "any"
	case ir.TypeStruct:
		if typ.Decl != nil {
			return typ.Decl.SymName()
		}
		return "Object"
	case ir.TypeEnum:
		if typ.Decl != nil {
			return typ.Decl.SymName()
		}
		return "string"
	default:
		return "any"
	}
}

func exprV2(e ast.Expr, ctx *codegen.ExprCtx) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return translateLiteral(n)
	case *ast.IdentExpr:
		return identV2(n, ctx)
	case *ast.BinaryExpr:
		left := exprV2(n.Left, ctx)
		right := exprV2(n.Right, ctx)
		if n.Op == ast.BinDiv && isIntDiv(n, ctx) {
			return "Math.trunc(" + left + " / " + right + ")"
		}
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ast.UnaryExpr:
		operand := exprV2(n.Operand, ctx)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		return "(" + exprV2(n.Cond, ctx) + " ? " + exprV2(n.Then, ctx) + " : " + exprV2(n.Else, ctx) + ")"
	case *ast.SelectExpr:
		return exprV2(n.Operand, ctx) + "." + n.Field
	case *ast.IndexExpr:
		return exprV2(n.Operand, ctx) + "[" + exprV2(n.Index, ctx) + "]"
	case *ast.CallExpr:
		return callV2(n, ctx)
	case *ast.StructExpr:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "..."+exprV2(f.Value, ctx))
			} else {
				parts = append(parts, f.Name+": "+exprV2(f.Value, ctx))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = exprV2(el, ctx)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.SpreadExpr:
		return "..." + exprV2(n.Operand, ctx)
	case *ast.InterpolationExpr:
		var sb strings.Builder
		sb.WriteByte('`')
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && isStringLiteral(lit) {
				raw := stripStringQuotes(lit)
				sb.WriteString(raw)
			} else {
				sb.WriteString("${")
				sb.WriteString(exprV2(p, ctx))
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
			stmts = append(stmts, mutationV2(s, ctx)...)
		}
		return strings.Join(stmts, "\n")
	case *ast.LambdaExpr:
		body := exprV2(n.Body, ctx)
		params := make([]string, len(n.Params.Params))
		for i, p := range n.Params.Params {
			params[i] = p.Name
		}
		if len(params) == 1 {
			return params[0] + " => " + body
		}
		return "(" + strings.Join(params, ", ") + ") => " + body
	case *ast.ParenExpr:
		return "(" + exprV2(n.Inner, ctx) + ")"
	default:
		return fmt.Sprintf("/* unsupported node %T */null", e)
	}
}

func identV2(n *ast.IdentExpr, ctx *codegen.ExprCtx) string {
	name := n.Name
	if name == "event" && ctx.EventVar != "" {
		return ctx.EventVar
	}
	_, kind := ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return ctx.RenamedName(name)
	case codegen.NameComputed:
		return "$" + name + "()"
	case codegen.NameStateVar:
		return "state." + name
	case codegen.NameConst, codegen.NameFunc, codegen.NameExternFunc, codegen.NameExternVar:
		return name
	default:
		return name
	}
}

func callV2(n *ast.CallExpr, ctx *codegen.ExprCtx) string {
	if sel, ok := n.Func.(*ast.SelectExpr); ok {
		return methodCallV2(sel, n.Args, ctx)
	}

	fn := ""
	if ident, ok := n.Func.(*ast.IdentExpr); ok {
		fn = ident.Name
	} else {
		fn = exprV2(n.Func, ctx)
	}

	args := extractArgs(n.Args)

	if fn == "string" && len(args) == 1 {
		if ctx.Helpers != nil {
			ctx.Helpers["String"] = true
		}
		return "String(" + exprV2(args[0], ctx) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "Math.trunc(" + exprV2(args[0], ctx) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "parseFloat(" + exprV2(args[0], ctx) + ")"
	}
	if fn == "regex" && len(args) == 1 {
		return "new RegExp(" + exprV2(args[0], ctx) + ")"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = exprV2(a, ctx)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func methodCallV2(sel *ast.SelectExpr, argList ast.ArgList, ctx *codegen.ExprCtx) string {
	method := sel.Field
	args := extractArgs(argList)

	if js := jsBuiltinMethodV2(sel, args, ctx); js != "" {
		return js
	}

	// Type-qualified call: int.double(5)
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName := ident.Name + "." + method
		_, kind := ctx.Resolve(qualName)
		if kind == codegen.NameFunc {
			jsName := strings.ReplaceAll(qualName, ".", "_")
			argStrs := make([]string, len(args))
			for i, a := range args {
				argStrs[i] = exprV2(a, ctx)
			}
			return jsName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}

	// Instance method via type-attached function
	if ctx.Pkg != nil && ctx.Pkg.Symbols != nil {
		operandType := ctx.TypeOf(sel.Operand)
		if operandType != nil {
			typeName := operandType.String()
			if f, ok := ctx.Pkg.Symbols.LookupMethod(typeName, method); ok {
				jsName := strings.ReplaceAll(typeName+"."+f.Name, ".", "_")
				argStrs := []string{exprV2(sel.Operand, ctx)}
				for _, a := range args {
					argStrs = append(argStrs, exprV2(a, ctx))
				}
				return jsName + "(" + strings.Join(argStrs, ", ") + ")"
			}
		}
	}

	target := exprV2(sel.Operand, ctx)
	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = exprV2(a, ctx)
	}
	return target + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
}

func mutationV2(s ast.Stmt, ctx *codegen.ExprCtx) []string {
	switch n := s.(type) {
	case *ast.AssignStmt:
		target := mutationTargetV2(n.Target, ctx)
		value := exprV2(n.Value, ctx)
		return []string{target + " " + assignOpStr(n.Op) + " " + value}
	case *ast.ToggleStmt:
		target := mutationTargetV2(n.Target, ctx)
		return []string{target + " = !" + target}
	case *ast.CallStmt:
		if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
			args := extractArgs(n.Call.Args)
			switch sel.Field {
			case "push":
				if len(args) == 1 {
					target := mutationTargetV2(sel.Operand, ctx)
					return []string{target + ".push(" + exprV2(args[0], ctx) + ")"}
				}
			case "remove":
				if len(args) == 1 {
					target := mutationTargetV2(sel.Operand, ctx)
					return []string{target + ".splice(" + exprV2(args[0], ctx) + ", 1)"}
				}
			}
		}
		return []string{callV2(n.Call, ctx)}
	case *ast.EmitStmt:
		args := extractArgs(n.Args)
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = exprV2(a, ctx)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", s)}
	}
}

func mutationTargetV2(e ast.Expr, ctx *codegen.ExprCtx) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		_, kind := ctx.Resolve(n.Name)
		if kind == codegen.NameStateVar {
			return "state." + n.Name
		}
		if kind == codegen.NameLocal {
			return ctx.RenamedName(n.Name)
		}
		return n.Name
	case *ast.SelectExpr:
		return mutationTargetV2(n.Operand, ctx) + "." + n.Field
	case *ast.IndexExpr:
		return mutationTargetV2(n.Operand, ctx) + "[" + exprV2(n.Index, ctx) + "]"
	default:
		return exprV2(e, ctx)
	}
}

// isIntDiv checks whether a binary division should use integer truncation.
// Uses TypeMap when available, falls back to syntactic heuristic.
func isIntDiv(n *ast.BinaryExpr, ctx *codegen.ExprCtx) bool {
	if t := ctx.TypeOf(n); t != nil {
		return t.Kind == ir.TypeInt
	}
	if t := ctx.TypeOf(n.Left); t != nil {
		return t.Kind == ir.TypeInt
	}
	// Fallback to syntactic heuristic when TypeMap unavailable
	return isIntNode(n.Left) && isIntNode(n.Right)
}

func isStringLiteral(lit *ast.LiteralExpr) bool {
	return lit.Kind == ast.LiteralStringQuoted ||
		lit.Kind == ast.LiteralStringBackticked ||
		lit.Kind == ast.LiteralStringTrippleQuoted
}

func stripStringQuotes(lit *ast.LiteralExpr) string {
	raw := lit.Raw
	raw = strings.TrimPrefix(raw, `"`)
	raw = strings.TrimSuffix(raw, `"`)
	raw = strings.TrimPrefix(raw, "`")
	raw = strings.TrimSuffix(raw, "`")
	raw = strings.TrimPrefix(raw, `"""`)
	raw = strings.TrimSuffix(raw, `"""`)
	return raw
}

// jsBuiltinMethodV2 returns a native JS expression for stdlib methods using ExprCtx.
func jsBuiltinMethodV2(sel *ast.SelectExpr, args []ast.Expr, ctx *codegen.ExprCtx) string {
	method := sel.Field
	var qualName string
	var argExprs []string

	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		qualName = ident.Name + "." + method
		for _, a := range args {
			argExprs = append(argExprs, exprV2(a, ctx))
		}
	} else {
		argExprs = append(argExprs, exprV2(sel.Operand, ctx))
		for _, a := range args {
			argExprs = append(argExprs, exprV2(a, ctx))
		}
		// Use TypeMap for resolved type name
		if t := ctx.TypeOf(sel.Operand); t != nil {
			qualName = t.String() + "." + method
		} else if sel.ResolvedType != "" {
			qualName = sel.ResolvedType
		} else {
			qualName = "*." + method
		}
	}

	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "undefined"
	}

	// Reuse the same builtin dispatch table
	switch qualName {
	case "int.min", "*.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "Math.abs(" + a(0) + ")"
	case "int.clamp", "*.clamp":
		return "Math.min(Math.max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
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
	case "color.rgb":
		return `"#" + (` + a(0) + `).toString(16).padStart(2, "0") + (` + a(1) + `).toString(16).padStart(2, "0") + (` + a(2) + `).toString(16).padStart(2, "0")`
	case "color.rgba":
		return `"#" + (` + a(0) + `).toString(16).padStart(2, "0") + (` + a(1) + `).toString(16).padStart(2, "0") + (` + a(2) + `).toString(16).padStart(2, "0") + Math.round(` + a(3) + ` * 255).toString(16).padStart(2, "0")`
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
	case "regex.matches":
		return a(0) + ".test(" + a(1) + ")"
	case "regex.find", "*.find":
		return "(" + a(0) + ".exec(" + a(1) + ") || [\"\"])[0]"
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
	case "File.pick":
		return `prompt("Enter file path:", "")`
	case "File.pickFolder":
		return `prompt("Enter folder path:", "")`
	}
	return ""
}
