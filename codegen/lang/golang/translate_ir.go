package golang

// Native IR-to-Go translator. Mirrors the AST-based translator in
// golang.go but walks ir.Expr / ir.Stmt trees directly so callers don't
// need ir.ConvertExpr / ir.ConvertStmt shims.

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func translateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return translateIRLiteral(n)
	case *ir.Ident:
		return translateIRIdent(n, scope)
	case *ir.Binary:
		left := translateIRExpr(n.Left, scope)
		right := translateIRExpr(n.Right, scope)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := translateIRExpr(n.Operand, scope)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		cond := translateIRExpr(n.Cond, scope)
		a := translateIRExpr(n.Then, scope)
		b := translateIRExpr(n.Else, scope)
		return "ternary(" + cond + ", " + a + ", " + b + ")"
	case *ir.Select:
		operand := translateIRExpr(n.Operand, scope)
		if n.Field == "length" {
			return "len(" + operand + ")"
		}
		return operand + "." + ExportName(n.Field)
	case *ir.Index:
		operand := translateIRExpr(n.Operand, scope)
		idx := translateIRExpr(n.Idx, scope)
		return operand + "[" + idx + "]"
	case *ir.Call:
		return translateIRCall(n, scope)
	case *ir.Conversion:
		return translateIRConversion(n, scope)
	case *ir.StructLit:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "/* ..."+translateIRExpr(f.Value, scope)+" */")
			} else {
				parts = append(parts, ExportName(f.Name)+": "+translateIRExpr(f.Value, scope))
			}
		}
		typeName := ""
		if n.Def != nil {
			typeName = ExportName(n.Def.Name)
		}
		return typeName + "{" + strings.Join(parts, ", ") + "}"
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = translateIRExpr(el, scope)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case *ir.Spread:
		return translateIRExpr(n.Operand, scope) + "..."
	case *ir.Lambda:
		return translateIRLambda(n, scope)
	default:
		return fmt.Sprintf("/* unsupported ir %T */nil", e)
	}
}

func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "nil"
	}
	if n.Suffix != "" {
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString, ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "nil"
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
		return fmt.Sprintf("elementRef(%q)", name)
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
		return "m." + name + "()"
	}
	if scope.ModelFields[name] {
		return "m." + ExportName(name)
	}
	return name
}

func translateIRCall(n *ir.Call, scope *codegen.ExprScope) string {
	if n.Receiver != nil {
		return translateIRMethodCall(n, scope)
	}
	fn := ""
	if n.Func != nil {
		fn = n.Func.Name
	}
	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	switch fn {
	case "string":
		if len(argStrs) == 1 {
			return "fmt.Sprint(" + argStrs[0] + ")"
		}
	case "int":
		if len(argStrs) == 1 {
			return "int(" + argStrs[0] + ")"
		}
	case "float":
		if len(argStrs) == 1 {
			return "float64(" + argStrs[0] + ")"
		}
	case "regex":
		if len(argStrs) == 1 {
			return "regexp.MustCompile(" + argStrs[0] + ")"
		}
	}
	if fn == "" {
		fn = "/* unresolved call */"
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateIRMethodCall(n *ir.Call, scope *codegen.ExprScope) string {
	if n.Func == nil {
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

	// Static type call: int.abs(x) → IntAbs(x)
	if id, ok := n.Receiver.(*ir.Ident); ok && isStaticTypeReceiver(id) {
		if scope.FuncNames[qualName] {
			goName := ExportName(id.Name) + ExportName(method)
			argStrs := make([]string, len(n.Args))
			for i, a := range n.Args {
				argStrs[i] = translateIRExpr(a.Value, scope)
			}
			return goName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}

	// Stdlib builtins.
	argsForBuiltin := []string{receiverJS}
	for _, a := range n.Args {
		argsForBuiltin = append(argsForBuiltin, translateIRExpr(a.Value, scope))
	}
	if code := goBuiltinMethodFromArgs(qualName, argsForBuiltin); code != "" {
		return code
	}
	if code := goBuiltinMethodFromArgs("*."+method, argsForBuiltin); code != "" {
		return code
	}

	// User-declared method surfaced as TypeMethod(recv, ...).
	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(argsForBuiltin, ", ") + ")"
	}

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
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
		return "int(" + operand + ")"
	case ir.TypeFloat:
		return "float64(" + operand + ")"
	case ir.TypeString:
		return "fmt.Sprint(" + operand + ")"
	case ir.TypeBool:
		return operand + ".(bool)"
	}
	return operand
}

func translateIRLambda(n *ir.Lambda, scope *codegen.ExprScope) string {
	if n.Func == nil {
		return "func() any { return nil }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name + " any"
	}
	subScope := *scope
	subScope.LocalVars = make(map[string]bool, len(scope.LocalVars)+len(params))
	for k, v := range scope.LocalVars {
		subScope.LocalVars[k] = v
	}
	for _, p := range n.Func.Params {
		subScope.LocalVars[p.Name] = true
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			return "func(" + strings.Join(params, ", ") + ") any { return " + translateIRExpr(ret.Value, &subScope) + " }"
		}
	}
	var b strings.Builder
	b.WriteString("func(" + strings.Join(params, ", ") + ") {\n")
	for _, s := range n.Func.Block {
		for _, line := range translateIRMutation(s, &subScope) {
			b.WriteString("\t" + line + "\n")
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
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
		}
		return []string{translateIRCall(n.Call, scope)}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{n.Name + " := " + translateIRExpr(n.Init, scope)}
		}
		return []string{"var " + n.Name + " any"}
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
			return "m." + ExportName(n.Name)
		}
		if scope.LocalVars[n.Name] && scope.Renames != nil {
			if renamed, ok := scope.Renames[n.Name]; ok {
				return renamed
			}
		}
		return n.Name
	case *ir.Select:
		return translateIRMutTarget(n.Operand, scope) + "." + ExportName(n.Field)
	case *ir.Index:
		return translateIRMutTarget(n.Operand, scope) + "[" + translateIRExpr(n.Idx, scope) + "]"
	default:
		return translateIRExpr(e, scope)
	}
}

// isStaticTypeReceiver reports whether an ident is a builtin type name,
// used to route qualified method calls through Type_Method naming.
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
