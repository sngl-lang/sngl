package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// GoContext translates SNGL expression and mutation nodes into Go code
// with model-field prefixing. Platforms that target Go (BubbleTea, Fyne)
// type-assert req.Lang to *Translator and call NewContext.
type GoContext struct {
	ModelFields    map[string]bool     // bind/computed names → prefix with "m."
	ComputedFields map[string]bool     // computed names → call as methods m.name()
	LocalVars      map[string]bool     // for-loop vars, component params → no prefix
	StructNames    map[string][]string // struct name → ordered field names
	EventVar       string              // what "event" maps to

	// AlertFunc translates Alert.toast/info/warn/error calls. Platforms
	// provide their own because the toast mechanism differs per platform.
	// If nil, a default "m.toasts = append(...)" implementation is used.
	AlertFunc func(ec *GoContext, n *ast.MethodExpr) []string

	// PropOverrides maps param names to pre-translated Go expressions.
	// Set during component expansion to substitute caller prop values.
	PropOverrides map[string]string
}

// NewContext creates a GoContext from a CommonAnalysis.
func (t *Translator) NewContext(modelFields, computedFields map[string]bool, structFields map[string][]string) *GoContext {
	return &GoContext{
		ModelFields:    modelFields,
		ComputedFields: computedFields,
		LocalVars:      make(map[string]bool),
		StructNames:    structFields,
	}
}

// TranslateExpr converts a SNGL expression Node into a Go expression string.
func (ec *GoContext) TranslateExpr(e ast.Node) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return ec.translateLiteral(n)
	case *ast.IdentExpr:
		return ec.translateIdent(n)
	case *ast.SelectExpr:
		operand := ec.TranslateExpr(n.Operand)
		if n.Field == "length" {
			return "len(" + operand + ")"
		}
		field := ExportName(n.Field)
		return operand + "." + field
	case *ast.BinaryExpr:
		left := ec.TranslateExpr(n.Left)
		right := ec.TranslateExpr(n.Right)
		if n.Op == ast.BinDiv {
			return "(float64(" + left + ") / float64(" + right + "))"
		}
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ast.UnaryExpr:
		operand := ec.TranslateExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		cond := ec.TranslateExpr(n.Cond)
		a := ec.TranslateExpr(n.Then)
		b := ec.TranslateExpr(n.Else)
		return "ternary(" + cond + ", " + a + ", " + b + ")"
	case *ast.IndexExpr:
		operand := ec.TranslateExpr(n.Operand)
		index := ec.TranslateExpr(n.Index)
		return operand + "[" + index + "]"
	case *ast.CallExpr:
		return ec.translateCall(n)
	case *ast.MethodExpr:
		if goCode := ec.builtinMethod(n); goCode != "" {
			return goCode
		}
		target := ec.TranslateExpr(n.Receiver)
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = ec.TranslateExpr(a)
		}
		return target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"
	case *ast.StructExpr:
		if fields, ok := ec.StructNames[n.Name]; ok {
			// Build name→value map from the expression's fields.
			exprFields := make(map[string]ast.Node, len(n.Fields))
			for _, f := range n.Fields {
				exprFields[f.Name] = f.Value
			}
			var parts []string
			for _, f := range fields {
				if val, ok := exprFields[f]; ok {
					parts = append(parts, ExportName(f)+": "+ec.TranslateExpr(val))
				}
			}
			return ExportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
		}
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, ExportName(f.Name)+": "+ec.TranslateExpr(f.Value))
		}
		return ExportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = ec.TranslateExpr(el)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case *ast.SpreadExpr:
		return ec.TranslateExpr(n.Operand) + "..."
	case *ast.InterpolationExpr:
		var fmtParts []string
		var args []string
		for _, part := range n.Parts {
			if lit, ok := part.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				fmtParts = append(fmtParts, strings.ReplaceAll(fmt.Sprintf("%v", lit.Value), "%", "%%"))
			} else {
				fmtParts = append(fmtParts, "%v")
				args = append(args, ec.TranslateExpr(part))
			}
		}
		if len(args) == 0 {
			return fmt.Sprintf("%q", strings.Join(fmtParts, ""))
		}
		return "fmt.Sprintf(" + fmt.Sprintf("%q", strings.Join(fmtParts, "")) + ", " + strings.Join(args, ", ") + ")"
	case *ast.StmtBlock:
		stmts := ec.TranslateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.AssignStmt:
		stmts := ec.TranslateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.ToggleStmt:
		stmts := ec.TranslateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.CallStmt:
		return ec.TranslateExpr(n.Call)
	case *ast.LambdaExpr:
		for _, param := range n.Params {
			ec.LocalVars[param] = true
		}
		body := ec.TranslateExpr(n.Body)
		for _, param := range n.Params {
			delete(ec.LocalVars, param)
		}
		params := make([]string, len(n.Params))
		for i, param := range n.Params {
			params[i] = param + " any"
		}
		return "func(" + strings.Join(params, ", ") + ") any { return " + body + " }"
	case *ast.ParenExpr:
		return "(" + ec.TranslateExpr(n.Inner) + ")"
	default:
		return fmt.Sprintf("/* unsupported node %T */nil", e)
	}
}

// TranslateMutation converts a SNGL statement node into Go assignment statements.
func (ec *GoContext) TranslateMutation(e ast.Node) []string {
	switch n := e.(type) {
	case *ast.StmtBlock:
		var stmts []string
		for _, s := range n.Stmts {
			stmts = append(stmts, ec.TranslateMutation(s)...)
		}
		return stmts
	case *ast.AssignStmt:
		target := ec.TranslateMutationTarget(n.Target)
		value := ec.TranslateExpr(n.Value)
		switch n.Op {
		case ast.AssignAdd:
			return []string{target + " = " + target + " + " + value}
		case ast.AssignSub:
			return []string{target + " = " + target + " - " + value}
		case ast.AssignMul:
			return []string{target + " = " + target + " * " + value}
		case ast.AssignDiv:
			return []string{target + " = " + target + " / " + value}
		case ast.AssignMod:
			return []string{target + " = " + target + " % " + value}
		default:
			return []string{target + " = " + value}
		}
	case *ast.ToggleStmt:
		target := ec.TranslateMutationTarget(n.Target)
		return []string{target + " = !" + target}
	case *ast.MethodExpr:
		if ident, ok := n.Receiver.(*ast.IdentExpr); ok && ident.Name == "Alert" {
			return ec.translateAlert(n)
		}
		target := ec.TranslateMutationTarget(n.Receiver)
		switch n.Method {
		case "push":
			if len(n.Args) == 1 {
				value := ec.TranslateExpr(n.Args[0])
				return []string{target + " = append(" + target + ", " + value + ")"}
			}
		case "remove":
			if len(n.Args) == 1 {
				idx := ec.TranslateExpr(n.Args[0])
				return []string{target + " = append(" + target + "[:" + idx + "], " + target + "[" + idx + "+1:]...)"}
			}
		}
		return []string{ec.TranslateExpr(n)}
	case *ast.CallStmt:
		return []string{ec.TranslateExpr(n.Call)}
	case *ast.CallExpr:
		return []string{ec.TranslateExpr(n)}
	default:
		return []string{fmt.Sprintf("// unsupported mutation: %T", e)}
	}
}

// TranslateMutationTarget translates a SNGL expression used as a mutation target.
func (ec *GoContext) TranslateMutationTarget(e ast.Node) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if ec.ModelFields[n.Name] {
			return "m." + n.Name
		}
		return n.Name
	case *ast.SelectExpr:
		operand := ec.TranslateMutationTarget(n.Operand)
		return operand + "." + ExportName(n.Field)
	case *ast.IndexExpr:
		operand := ec.TranslateMutationTarget(n.Operand)
		index := ec.TranslateExpr(n.Index)
		return operand + "[" + index + "]"
	default:
		return ec.TranslateExpr(e)
	}
}

func (ec *GoContext) translateLiteral(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralString:
		return fmt.Sprintf("%q", n.Value)
	case ast.LiteralInt:
		return fmt.Sprintf("%d", n.Value)
	case ast.LiteralFloat:
		s := fmt.Sprintf("%v", n.Value)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case ast.LiteralBool:
		if n.Value.(bool) {
			return "true"
		}
		return "false"
	case ast.LiteralNull:
		return "nil"
	case ast.LiteralColor:
		return fmt.Sprintf("%q", n.Value)
	default:
		return fmt.Sprintf("%v", n.Value)
	}
}

func (ec *GoContext) translateIdent(n *ast.IdentExpr) string {
	name := n.Name
	if name == "event" && ec.EventVar != "" {
		return ec.EventVar
	}
	// Component param overrides: resolve to caller's pre-translated expression.
	if ec.PropOverrides != nil {
		if val, ok := ec.PropOverrides[name]; ok {
			return val
		}
	}
	if ec.LocalVars[name] {
		return name
	}
	if ec.ComputedFields[name] {
		return "m." + name + "()"
	}
	if ec.ModelFields[name] {
		return "m." + name
	}
	return name
}

func (ec *GoContext) translateCall(n *ast.CallExpr) string {
	fn := n.Func
	args := n.Args

	if fn == "string" && len(args) == 1 {
		return "fmt.Sprint(" + ec.TranslateExpr(args[0]) + ")"
	}
	if fn == "size" && len(args) == 1 {
		return "len(" + ec.TranslateExpr(args[0]) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "int(" + ec.TranslateExpr(args[0]) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "float64(" + ec.TranslateExpr(args[0]) + ")"
	}
	if fn == "push" && len(args) == 2 {
		return "append(" + ec.TranslateExpr(args[0]) + ", " + ec.TranslateExpr(args[1]) + ")"
	}
	if fn == "remove" && len(args) == 2 {
		list := ec.TranslateExpr(args[0])
		idx := ec.TranslateExpr(args[1])
		return "append(" + list + "[:" + idx + "], " + list + "[" + idx + "+1:]...)"
	}

	// Struct constructor call
	if fields, ok := ec.StructNames[fn]; ok {
		var parts []string
		for i, f := range fields {
			val := "nil"
			if i < len(args) {
				val = ec.TranslateExpr(args[i])
			}
			parts = append(parts, ExportName(f)+": "+val)
		}
		return ExportName(fn) + "{" + strings.Join(parts, ", ") + "}"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = ec.TranslateExpr(a)
	}
	// Extern functions are model fields — prefix with m.
	if ec.ModelFields[fn] {
		fn = "m." + ExportName(fn)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func (ec *GoContext) translateAlert(n *ast.MethodExpr) []string {
	if ec.AlertFunc != nil {
		return ec.AlertFunc(ec, n)
	}
	// Default: append to m.toasts
	switch n.Method {
	case "toast":
		msg := ec.TranslateExpr(n.Args[0])
		variant := `"info"`
		if len(n.Args) > 1 {
			variant = ec.TranslateExpr(n.Args[1])
		}
		return []string{fmt.Sprintf("m.toasts = append(m.toasts, snglToast{%s, %s})", msg, variant)}
	case "info", "warn", "error":
		msg := ec.TranslateExpr(n.Args[0])
		return []string{fmt.Sprintf("m.toasts = append(m.toasts, snglToast{%s, %q})", msg, n.Method)}
	case "confirm":
		return []string{"// Alert.confirm not supported in TUI"}
	}
	return []string{ec.TranslateExpr(n)}
}

// ExprToGoValue converts an ast.Expr to a Go value string.
func ExprToGoValue(expr ast.Expr, ec *GoContext) string {
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
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	if expr.SNGL != nil && ec != nil {
		return ec.TranslateExpr(expr.SNGL)
	}
	return `""`
}

// ExprToGoCond converts an ast.Expr to a Go boolean expression string.
func ExprToGoCond(expr ast.Expr, ec *GoContext) string {
	if expr.SNGL != nil {
		return ec.TranslateExpr(expr.SNGL)
	}
	if expr.Literal != nil {
		if v, ok := expr.Literal.(bool); ok {
			if v {
				return "true"
			}
			return "false"
		}
	}
	return "true"
}

// builtinMethod checks for stdlib builtin methods (like string.length) and
// returns translated Go code, or "" if not a builtin. Mirrors goBuiltinMethod
// in golang.go but uses GoContext for sub-expression translation.
func (ec *GoContext) builtinMethod(n *ast.MethodExpr) string {
	var argExprs []string
	var qualName string

	if ident, ok := n.Receiver.(*ast.IdentExpr); ok {
		// Check type-qualified name first (e.g., "string.length", "int.min")
		qualName = ident.Name + "." + n.Method
		for _, a := range n.Args {
			argExprs = append(argExprs, ec.TranslateExpr(a))
		}
		if result := goBuiltinMethodFromArgs(qualName, argExprs); result != "" {
			return result
		}
		// Not a type-qualified builtin; treat as instance method (e.g., notes.length())
		argExprs = []string{ec.TranslateExpr(n.Receiver)}
		for _, a := range n.Args {
			argExprs = append(argExprs, ec.TranslateExpr(a))
		}
		if n.Resolved != "" {
			qualName = n.Resolved
		} else {
			qualName = "*." + n.Method
		}
	} else {
		argExprs = append(argExprs, ec.TranslateExpr(n.Receiver))
		for _, a := range n.Args {
			argExprs = append(argExprs, ec.TranslateExpr(a))
		}
		if n.Resolved != "" {
			qualName = n.Resolved
		} else {
			qualName = "*." + n.Method
		}
	}

	return goBuiltinMethodFromArgs(qualName, argExprs)
}

// ExprToGoStringList converts an ast.Expr that should be a list of strings
// into a Go []string expression.
func ExprToGoStringList(expr ast.Expr, ec *GoContext) string {
	if expr.SNGL != nil {
		if list, ok := expr.SNGL.(*ast.ListExpr); ok {
			parts := make([]string, len(list.Elements))
			for i, el := range list.Elements {
				parts[i] = ec.TranslateExpr(el)
			}
			return "[]string{" + strings.Join(parts, ", ") + "}"
		}
		return ec.TranslateExpr(expr.SNGL)
	}
	return "nil"
}
