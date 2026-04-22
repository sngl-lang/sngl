package javascript

// Native IR-to-JavaScript translator. Mirrors the AST-based translator in
// javascript.go but walks ir.Expr / ir.Stmt trees directly so callers don't
// need ir.ConvertExpr / ir.ConvertStmt shims.

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func translateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return translateIRLiteral(n)
	case *ir.Ident:
		return translateIRIdent(n, scope)
	case *ir.Binary:
		left := translateIRExpr(n.Left, scope)
		right := translateIRExpr(n.Right, scope)
		if n.Op == ast.BinDiv && isIntIR(n.Left) && isIntIR(n.Right) {
			return "Math.trunc(" + left + " / " + right + ")"
		}
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := translateIRExpr(n.Operand, scope)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		return "(" + translateIRExpr(n.Cond, scope) + " ? " +
			translateIRExpr(n.Then, scope) + " : " +
			translateIRExpr(n.Else, scope) + ")"
	case *ir.Select:
		return translateIRExpr(n.Operand, scope) + "." + n.Field
	case *ir.Index:
		return translateIRExpr(n.Operand, scope) + "[" + translateIRExpr(n.Idx, scope) + "]"
	case *ir.Call:
		return translateIRCall(n, scope)
	case *ir.Conversion:
		return translateIRConversion(n, scope)
	case *ir.StructLit:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "..."+translateIRExpr(f.Value, scope))
			} else {
				parts = append(parts, f.Name+": "+translateIRExpr(f.Value, scope))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = translateIRExpr(el, scope)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ir.Spread:
		return "..." + translateIRExpr(n.Operand, scope)
	case *ir.Lambda:
		return translateIRLambda(n, scope)
	default:
		return fmt.Sprintf("/* unsupported ir %T */null", e)
	}
}

func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "null"
	}
	// Unit literal (e.g. 500ms): emit as quoted string carrying the suffix.
	if n.Suffix != "" {
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString, ir.TypeColor:
			// ir.Literal.Raw mirrors ast.LiteralExpr.Raw — the unquoted text.
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "null"
		}
	}
	return n.Raw
}

func translateIRIdent(n *ir.Ident, scope *codegen.ExprScope) string {
	name := n.Name
	if name == "event" && scope.EventVar != "" {
		return scope.EventVar
	}
	if n.IsElementRef {
		return fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", name)
	}
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
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

func translateIRCall(n *ir.Call, scope *codegen.ExprScope) string {
	// Method call: receiver.method(args).
	if n.Receiver != nil {
		return translateIRMethodCall(n, scope)
	}

	// Plain function call.
	fn := ""
	if n.Func != nil {
		fn = n.Func.Name
	} else if n.Callee != nil {
		fn = translateIRExpr(n.Callee, scope)
	}
	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}

	switch fn {
	case "string":
		if len(argStrs) == 1 {
			if scope.NeededHelpers != nil {
				scope.NeededHelpers["String"] = true
			}
			return "String(" + argStrs[0] + ")"
		}
	case "int":
		if len(argStrs) == 1 {
			return "Math.trunc(" + argStrs[0] + ")"
		}
	case "float":
		if len(argStrs) == 1 {
			return "parseFloat(" + argStrs[0] + ")"
		}
	case "regex":
		if len(argStrs) == 1 {
			return "new RegExp(" + argStrs[0] + ")"
		}
	}
	if fn == "" {
		fn = "/* unresolved call */"
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateIRMethodCall(n *ir.Call, scope *codegen.ExprScope) string {
	if n.Func == nil {
		// Unresolved method on dynamic receiver — emit best-effort call.
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		return translateIRExpr(n.Receiver, scope) + "(" + strings.Join(argStrs, ", ") + ")"
	}
	method := n.Func.Name
	receiverName := n.Func.Receiver

	qualName := receiverName + "." + method
	receiverJS := translateIRExpr(n.Receiver, scope)

	// Type-qualified static call: int.abs(x) → int_abs(x). Detect via
	// receiver being a bare type ident (its checker-assigned Sym is *TypeSym).
	if id, ok := n.Receiver.(*ir.Ident); ok && isStaticTypeReceiver(id) {
		if scope.FuncNames[qualName] {
			jsName := strings.ReplaceAll(qualName, ".", "_")
			argStrs := make([]string, len(n.Args))
			for i, a := range n.Args {
				argStrs[i] = translateIRExpr(a.Value, scope)
			}
			return jsName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}

	// Stdlib method overrides.
	argsForBuiltin := []string{receiverJS}
	for _, a := range n.Args {
		argsForBuiltin = append(argsForBuiltin, translateIRExpr(a.Value, scope))
	}
	if js := jsBuiltinMethodFromArgs(qualName, argsForBuiltin); js != "" {
		return js
	}
	if js := jsBuiltinMethodFromArgs("*."+method, argsForBuiltin); js != "" {
		return js
	}

	// User-defined method looked up as a free function type_method(recv, ...).
	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		jsName := strings.ReplaceAll(qualName, ".", "_")
		return jsName + "(" + strings.Join(argsForBuiltin, ", ") + ")"
	}

	// Mutation methods: push / remove act as instance-method calls.
	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	switch method {
	case "push":
		if len(argStrs) == 1 {
			return receiverJS + ".push(" + argStrs[0] + ")"
		}
	case "remove":
		if len(argStrs) == 1 {
			return receiverJS + ".splice(" + argStrs[0] + ", 1)"
		}
	}
	return receiverJS + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateIRConversion(n *ir.Conversion, scope *codegen.ExprScope) string {
	operand := translateIRExpr(n.Operand, scope)
	if n.Type == nil {
		return operand
	}
	switch n.Type.Kind {
	case ir.TypeInt:
		return "Math.trunc(" + operand + ")"
	case ir.TypeFloat:
		return "parseFloat(" + operand + ")"
	case ir.TypeString:
		if scope.NeededHelpers != nil {
			scope.NeededHelpers["String"] = true
		}
		return "String(" + operand + ")"
	case ir.TypeBool:
		return "Boolean(" + operand + ")"
	}
	return operand
}

func translateIRLambda(n *ir.Lambda, scope *codegen.ExprScope) string {
	if n.Func == nil {
		return "() => null"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	// Extend scope with lambda params as locals so nested refs resolve.
	subScope := *scope
	subScope.LocalVars = make(map[string]bool, len(scope.LocalVars)+len(params))
	maps.Copy(subScope.LocalVars, scope.LocalVars)
	for _, p := range params {
		subScope.LocalVars[p] = true
	}

	// Single-expression body.
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := translateIRExpr(ret.Value, &subScope)
			if len(params) == 1 {
				return params[0] + " => " + body
			}
			return "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString("(" + strings.Join(params, ", ") + ") => {\n")
	for _, s := range n.Func.Block {
		for _, line := range translateIRMutation(s, &subScope) {
			b.WriteString("  " + line + ";\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func translateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	if s == nil {
		return nil
	}
	switch n := s.(type) {
	case *ir.Assign:
		target := translateIRMutTarget(n.Target, scope)
		value := translateIRExpr(n.Value, scope)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := translateIRMutTarget(n.Target, scope)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
		}
		return []string{translateIRCall(n.Call, scope)}
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"let " + n.Name + " = " + translateIRExpr(n.Init, scope)}
		}
		return []string{"let " + n.Name}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + translateIRExpr(n.Value, scope)}
		}
		return []string{"return"}
	default:
		return []string{"// unsupported ir mutation: " + fmt.Sprintf("%T", s)}
	}
}

func translateIRMutTarget(e ir.Expr, scope *codegen.ExprScope) string {
	switch n := e.(type) {
	case *ir.Ident:
		if scope.ModelFields[n.Name] {
			return "state." + n.Name
		}
		if scope.LocalVars[n.Name] && scope.Renames != nil {
			if renamed, ok := scope.Renames[n.Name]; ok {
				return renamed
			}
		}
		return n.Name
	case *ir.Select:
		return translateIRMutTarget(n.Operand, scope) + "." + n.Field
	case *ir.Index:
		return translateIRMutTarget(n.Operand, scope) + "[" + translateIRExpr(n.Idx, scope) + "]"
	default:
		return translateIRExpr(e, scope)
	}
}

// isIntIR reports whether an IR expression is typed as int (for integer
// division lowering).
func isIntIR(e ir.Expr) bool {
	if e == nil {
		return false
	}
	t := e.ExprType()
	if t != nil && t.Kind == ir.TypeInt {
		return true
	}
	return false
}

// isStaticTypeReceiver reports whether an ident refers to a type name
// (for qualifying static method calls like int.abs).
func isStaticTypeReceiver(id *ir.Ident) bool {
	if id == nil {
		return false
	}
	switch id.Name {
	case "int", "float", "string", "bool", "list", "option",
		"date", "time", "dateTime", "duration", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6",
		"hostname", "decimal":
		return true
	}
	return false
}
