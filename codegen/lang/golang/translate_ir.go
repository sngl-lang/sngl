package golang

// Native IR-to-Go translator. Mirrors the AST-based translator in
// golang.go but walks ir.Expr / ir.Stmt trees directly so callers don't
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
	case *ir.MapLitIR:
		keyType := "any"
		valType := "any"
		if n.Type != nil && n.Type.Kind == ir.TypeMap && len(n.Type.Elems) == 2 {
			keyType = IRTypeToGo(n.Type.Elems[0])
			valType = IRTypeToGo(n.Type.Elems[1])
		}
		var parts []string
		for _, e := range n.Entries {
			parts = append(parts, translateIRExpr(e.Key, scope)+": "+translateIRExpr(e.Value, scope))
		}
		return "map[" + keyType + "]" + valType + "{" + strings.Join(parts, ", ") + "}"
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
		return translateIRNamespaceCall(n, scope)
	}
	if n.Func != nil && n.Func.Receiver != "" {
		return translateIRTypeMethodCall(n, scope)
	}
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

// translateIRNamespaceCall handles calls that retain a Receiver expression:
// namespace function / component calls like `ns.foo(x)` and `html.div(...)`.
func translateIRNamespaceCall(n *ir.Call, scope *codegen.ExprScope) string {
	if n.Func == nil {
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		return translateIRExpr(n.Receiver, scope) + "(" + strings.Join(argStrs, ", ") + ")"
	}
	if n.Func.NativePkg != "" {
		return translateIRNativeCall(n, scope)
	}
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method
	receiverJS := translateIRExpr(n.Receiver, scope)

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

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	return receiverJS + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
}

// translateIRNativeCall emits a call into a scheme-imported Go function,
// using the NativeName recorded on the Func and injecting a context argument
// or wrapping in an error adapter as flagged by the importer.
func translateIRNativeCall(n *ir.Call, scope *codegen.ExprScope) string {
	argStrs := make([]string, 0, len(n.Args)+1)
	if n.Func.HasContextArg {
		ctxVar := scope.ContextVar
		if ctxVar == "" {
			ctxVar = "context.Background()"
		}
		argStrs = append(argStrs, ctxVar)
	}
	for _, a := range n.Args {
		argStrs = append(argStrs, translateIRExpr(a.Value, scope))
	}
	call := n.Func.NativeName + "(" + strings.Join(argStrs, ", ") + ")"
	if n.Func.HasErrorReturn {
		if scope.NeededHelpers == nil {
			scope.NeededHelpers = map[string]bool{}
		}
		scope.NeededHelpers["nativeMustOK"] = true
		return "nativeMustOK(" + call + ")"
	}
	return call
}

// translateIRTypeMethodCall handles type-attached method calls after checker
// normalization: Func.Receiver is the type name; Args[0] is the receiver value;
// Args[1:] are the explicit arguments.
func translateIRTypeMethodCall(n *ir.Call, scope *codegen.ExprScope) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}

	if code := goBuiltinMethodFromArgs(qualName, argStrs); code != "" {
		return code
	}
	if code := goBuiltinMethodFromArgs("*."+method, argStrs); code != "" {
		return code
	}

	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(argStrs, ", ") + ")"
	}

	// Best-effort fallback: receiver.method(args).
	if len(argStrs) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return argStrs[0] + "." + method + "(" + strings.Join(argStrs[1:], ", ") + ")"
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
	maps.Copy(subScope.LocalVars, scope.LocalVars)
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
	case *ir.For:
		return translateIRForGo(n, scope)
	default:
		return []string{"// unsupported ir mutation: " + fmt.Sprintf("%T", s)}
	}
}

func translateIRForGo(n *ir.For, scope *codegen.ExprScope) []string {
	iterExpr := translateIRExpr(n.Iter, scope)
	loopScope := *scope
	locals := make(map[string]bool, len(scope.LocalVars)+2)
	for k, v := range scope.LocalVars {
		locals[k] = v
	}
	locals[n.Key] = true
	if n.Value != "" {
		locals[n.Value] = true
	}
	loopScope.LocalVars = locals

	var lines []string
	iterType := n.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		lines = append(lines, fmt.Sprintf("for %s, %s := range %s {", n.Key, valueVar, iterExpr))
	} else {
		indexVar := "_"
		if n.Value != "" {
			indexVar = n.Value
		}
		lines = append(lines, fmt.Sprintf("for %s, %s := range %s {", indexVar, n.Key, iterExpr))
	}
	for _, stmt := range n.Body {
		for _, l := range translateIRMutation(stmt, &loopScope) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
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
