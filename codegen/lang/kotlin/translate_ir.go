package kotlin

// Native IR-to-Kotlin translator. Mirrors the AST-based translator in
// kotlin.go but walks ir.Expr / ir.Stmt trees directly so callers don't
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
		return "(if (" + cond + ") " + a + " else " + b + ")"
	case *ir.Select:
		operand := translateIRExpr(n.Operand, scope)
		return operand + "." + n.Field
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
				parts = append(parts, f.Name+" = "+translateIRExpr(f.Value, scope))
			}
		}
		typeName := ""
		if n.Def != nil {
			typeName = exportName(n.Def.Name)
		}
		return typeName + "(" + strings.Join(parts, ", ") + ")"
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = translateIRExpr(el, scope)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ir.Spread:
		return "*" + translateIRExpr(n.Operand, scope)
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
	if n.Suffix != "" {
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString, ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt, ir.TypeBool:
			return n.Raw
		case ir.TypeFloat:
			s := n.Raw
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
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
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}
	if scope.LocalVars[name] && scope.Renames != nil {
		if renamed, ok := scope.Renames[name]; ok {
			return renamed
		}
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
			return argStrs[0] + ".toString()"
		}
	case "int":
		if len(argStrs) == 1 {
			return argStrs[0] + ".toInt()"
		}
	case "float":
		if len(argStrs) == 1 {
			return argStrs[0] + ".toDouble()"
		}
	case "regex":
		if len(argStrs) == 1 {
			return "Regex(" + argStrs[0] + ")"
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
	receiverKT := translateIRExpr(n.Receiver, scope)

	if id, ok := n.Receiver.(*ir.Ident); ok && isStaticTypeReceiver(id) {
		if scope.FuncNames[qualName] {
			ktName := id.Name + strings.ToUpper(method[:1]) + method[1:]
			argStrs := make([]string, len(n.Args))
			for i, a := range n.Args {
				argStrs[i] = translateIRExpr(a.Value, scope)
			}
			return ktName + "(" + strings.Join(argStrs, ", ") + ")"
		}
	}

	argsForBuiltin := []string{receiverKT}
	for _, a := range n.Args {
		argsForBuiltin = append(argsForBuiltin, translateIRExpr(a.Value, scope))
	}
	if code := kotlinBuiltinMethodFromArgs(qualName, argsForBuiltin); code != "" {
		return code
	}
	if code := kotlinBuiltinMethodFromArgs("*."+method, argsForBuiltin); code != "" {
		return code
	}

	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		ktName := receiverName + strings.ToUpper(method[:1]) + method[1:]
		return ktName + "(" + strings.Join(argsForBuiltin, ", ") + ")"
	}

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	return receiverKT + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateIRConversion(n *ir.Conversion, scope *codegen.ExprScope) string {
	operand := translateIRExpr(n.Operand, scope)
	if n.Type == nil {
		return operand
	}
	switch n.Type.Kind {
	case ir.TypeInt:
		return operand + ".toInt()"
	case ir.TypeFloat:
		return operand + ".toDouble()"
	case ir.TypeString:
		return operand + ".toString()"
	case ir.TypeBool:
		return operand + " as Boolean"
	}
	return operand
}

func translateIRLambda(n *ir.Lambda, scope *codegen.ExprScope) string {
	if n.Func == nil {
		return "{ null }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	subScope := *scope
	subScope.LocalVars = make(map[string]bool, len(scope.LocalVars)+len(params))
	maps.Copy(subScope.LocalVars, scope.LocalVars)
	for _, p := range params {
		subScope.LocalVars[p] = true
	}

	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := translateIRExpr(ret.Value, &subScope)
			if len(params) == 0 {
				return "{ " + body + " }"
			}
			return "{ " + strings.Join(params, ", ") + " -> " + body + " }"
		}
	}
	var b strings.Builder
	b.WriteString("{ " + strings.Join(params, ", ") + " ->\n")
	for _, s := range n.Func.Block {
		for _, line := range translateIRMutation(s, &subScope) {
			b.WriteString("  " + line + "\n")
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
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
		}
		// push / remove mutation short-circuits.
		if n.Call.Receiver != nil && n.Call.Func != nil {
			target := translateIRMutTarget(n.Call.Receiver, scope)
			method := n.Call.Func.Name
			argStrs := make([]string, len(n.Call.Args))
			for i, a := range n.Call.Args {
				argStrs[i] = translateIRExpr(a.Value, scope)
			}
			switch method {
			case "push":
				if len(argStrs) == 1 {
					return []string{target + ".add(" + argStrs[0] + ")"}
				}
			case "remove":
				if len(argStrs) == 1 {
					return []string{target + ".removeAt(" + argStrs[0] + ")"}
				}
			}
		}
		return []string{translateIRCall(n.Call, scope)}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"val " + n.Name + " = " + translateIRExpr(n.Init, scope)}
		}
		return []string{"var " + n.Name + ": Any? = null"}
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
