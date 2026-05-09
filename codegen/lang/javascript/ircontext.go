package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// JsIRContext translates IR expressions and statements into JavaScript code.
type JsIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string
}

// NewIRContext creates a JsIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *JsIRContext {
	return &JsIRContext{Ctx: ctx}
}

// EvalExpr translates an IR expression into a JavaScript expression string.
func (jc *JsIRContext) EvalExpr(e ir.Expr) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return jc.evalLiteral(n)
	case *ir.Ident:
		return jc.evalIdent(n)
	case *ir.Binary:
		left := jc.EvalExpr(n.Left)
		right := jc.EvalExpr(n.Right)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := jc.EvalExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		return "(" + jc.EvalExpr(n.Cond) + " ? " + jc.EvalExpr(n.Then) + " : " + jc.EvalExpr(n.Else) + ")"
	case *ir.Select:
		return jc.EvalExpr(n.Operand) + "." + n.Field
	case *ir.Index:
		operand := jc.EvalExpr(n.Operand)
		idx := jc.EvalExpr(n.Idx)
		if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
			return operand + ".get(" + idx + ")"
		}
		return operand + "[" + idx + "]"
	case *ir.Call:
		return jc.evalCall(n)
	case *ir.Conversion:
		return jc.evalConversion(n)
	case *ir.StructLit:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "..."+jc.EvalExpr(f.Value))
			} else {
				parts = append(parts, f.Name+": "+jc.EvalExpr(f.Value))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = jc.EvalExpr(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ir.MapLitIR:
		var b strings.Builder
		b.WriteString("new Map([")
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[")
			b.WriteString(jc.EvalExpr(e.Key))
			b.WriteString(", ")
			b.WriteString(jc.EvalExpr(e.Value))
			b.WriteString("]")
		}
		b.WriteString("])")
		return b.String()
	case *ir.Spread:
		return "..." + jc.EvalExpr(n.Operand)
	case *ir.Lambda:
		return jc.evalLambda(n)
	default:
		return fmt.Sprintf("/* unsupported IR %T */null", e)
	}
}

// EvalStmt translates an IR statement into JS statement strings.
func (jc *JsIRContext) EvalStmt(s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		target := jc.evalMutTarget(n.Target)
		value := jc.EvalExpr(n.Value)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := jc.evalMutTarget(n.Target)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
			if lines := jc.evalErrorAwareCall(n.Call); lines != nil {
				return lines
			}
		}
		return []string{jc.EvalExpr(n.Call)}
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = jc.EvalExpr(a.Value)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"let " + n.Name + " = " + jc.EvalExpr(n.Init)}
		}
		return []string{"let " + n.Name}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + jc.EvalExpr(n.Value)}
		}
		return []string{"return"}
	case *ir.For:
		return jc.evalFor(n)
	default:
		return []string{"// unsupported IR stmt: " + fmt.Sprintf("%T", s)}
	}
}

// evalFor emits a JS for-loop. Maps use Map.entries(); lists/iter<T> use for-of.
func (jc *JsIRContext) evalFor(n *ir.For) []string {
	iterExpr := jc.EvalExpr(n.Iter)
	loopJC := jc.WithLocal(n.Key)
	if n.Value != "" {
		loopJC = loopJC.WithLocal(n.Value)
	}

	var lines []string
	iterType := n.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		// Map iteration: for (const [k, v] of m.entries()) { ... }
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		lines = append(lines, fmt.Sprintf("for (const [%s, %s] of %s.entries()) {", n.Key, valueVar, iterExpr))
	} else {
		// List / iter<T> iteration: for (const x of list) { ... }
		lines = append(lines, fmt.Sprintf("for (const %s of %s) {", n.Key, iterExpr))
	}
	for _, stmt := range n.Body {
		for _, l := range loopJC.EvalStmt(stmt) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

// evalErrorAwareCall emits JS statements for a fallible call whose error
// handler was resolved by effect analysis. Mirrors the Go translator;
// only error.raise is recognised in MVP. Returns nil to signal no
// specialised emission (fallback to normal call path).
func (jc *JsIRContext) evalErrorAwareCall(call *ir.Call) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !jsIsRaiseFunc(call.Func) {
		return nil
	}
	msg := `""`
	kind := `""`
	if len(call.Args) >= 1 {
		msg = jc.EvalExpr(call.Args[0].Value)
	}
	if len(call.Args) >= 2 {
		kind = jc.EvalExpr(call.Args[1].Value)
	}
	evt := fmt.Sprintf("{message: %s, kind: %s}", msg, kind)

	switch call.ErrorMode {
	case ir.ErrorPropagateNative, ir.ErrorBubble:
		// ErrorBubble: no fallible-signature lowering in MVP. Throw so the
		// enclosing scope (if any) surfaces the error natively.
		return []string{"throw Object.assign(new Error(" + msg + "), {kind: " + kind + "})"}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler == nil || call.ResolvedHandler.Func == nil {
			return []string{"throw Object.assign(new Error(" + msg + "), {kind: " + kind + "})"}
		}
		return jc.emitHandlerInvoke(evt, call.ResolvedHandler)
	case ir.ErrorPerCall:
		if call.ErrorHandler == nil || call.ErrorHandler.Func == nil {
			return []string{"void " + evt}
		}
		return jc.emitHandlerInvoke(evt, call.ErrorHandler)
	}
	return nil
}

// emitHandlerInvoke inlines the handler body in a JS block scope.
// See Go emitHandlerInvoke docs — same MVP limitation on terminate.
func (jc *JsIRContext) emitHandlerInvoke(evt string, handler *ir.EventHandler) []string {
	paramName := "e"
	if handler.Func != nil && len(handler.Func.Params) > 0 {
		paramName = handler.Func.Params[0].Name
	}
	lines := []string{
		"{",
		fmt.Sprintf("\tlet %s = %s", paramName, evt),
		fmt.Sprintf("\tvoid %s", paramName),
	}
	for _, stmt := range handler.Func.Block {
		for _, l := range jc.EvalStmt(stmt) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

func jsIsRaiseFunc(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "ErrorRaise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

func (jc *JsIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Raw
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Raw)
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return n.Raw
	case ir.TypeNull:
		return "null"
	case ir.TypeColor:
		return fmt.Sprintf("%q", n.Raw)
	default:
		if n.Suffix != "" {
			return fmt.Sprintf("%q", n.Raw+n.Suffix)
		}
		return n.Raw
	}
}

func (jc *JsIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}
	name := n.Name
	if name == "event" && jc.EventVar != "" {
		return jc.EventVar
	}
	_, kind := jc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return jc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return "$" + name + "()"
	case codegen.NameStateVar:
		return "state." + name
	case codegen.NameConst:
		return name
	default:
		if n.Type != nil && n.Type.Kind == ir.TypeEnum {
			return fmt.Sprintf("%q", name)
		}
		return name
	}
}

func (jc *JsIRContext) evalCall(n *ir.Call) string {
	if n.Receiver != nil {
		return jc.evalNamespaceCall(n)
	}
	if n.Func != nil && n.Func.Receiver != "" {
		return jc.evalTypeMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := jc.evalCallArgs(n.Args)
		switch fname {
		case "string":
			if len(args) == 1 {
				if jc.Ctx.Helpers != nil {
					jc.Ctx.Helpers["String"] = true
				}
				return "String(" + args[0] + ")"
			}
		case "int":
			if len(args) == 1 {
				return "Math.trunc(" + args[0] + ")"
			}
		case "float":
			if len(args) == 1 {
				return "parseFloat(" + args[0] + ")"
			}
		}
		return fname + "(" + strings.Join(args, ", ") + ")"
	}
	args := jc.evalCallArgs(n.Args)
	if n.Callee != nil {
		return jc.EvalExpr(n.Callee) + "(" + strings.Join(args, ", ") + ")"
	}
	return "(" + strings.Join(args, ", ") + ")"
}

func (jc *JsIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := jc.EvalExpr(n.Receiver)
	args := jc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		receiverName := n.Func.Receiver
		qualName := receiverName + "." + fname
		allArgs := append([]string{receiver}, args...)
		if result := jsBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		if result := jsBuiltinMethodFromArgs("*."+fname, allArgs); result != "" {
			return result
		}
		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}
	return receiver + "(" + strings.Join(args, ", ") + ")"
}

func (jc *JsIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	args := jc.evalCallArgs(n.Args)
	if result := jsBuiltinMethodFromArgs(qualName, args); result != "" {
		return result
	}
	if result := jsBuiltinMethodFromArgs("*."+method, args); result != "" {
		return result
	}

	if len(args) >= 1 {
		recv := args[0]
		rest := args[1:]
		switch method {
		case "push":
			if len(rest) == 1 {
				return recv + ".push(" + rest[0] + ")"
			}
		case "remove":
			if len(rest) == 1 {
				return recv + ".splice(" + rest[0] + ", 1)"
			}
		}
		return recv + "." + method + "(" + strings.Join(rest, ", ") + ")"
	}
	return "/* unresolved method " + qualName + " */"
}

func (jc *JsIRContext) evalConversion(n *ir.Conversion) string {
	if isNullToFuncConvJS(n) {
		return nullFuncStubJS(n.Type)
	}
	operand := jc.EvalExpr(n.Operand)
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt:
			return "Math.trunc(" + operand + ")"
		case ir.TypeFloat:
			return "parseFloat(" + operand + ")"
		case ir.TypeString:
			return "String(" + operand + ")"
		}
	}
	return operand
}

func isNullToFuncConvJS(n *ir.Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != ir.TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

func nullFuncStubJS(t *ir.Type) string {
	if t == nil || t.Sig == nil {
		return "() => null"
	}
	arity := len(t.Sig.Params)
	params := make([]string, arity)
	for i := range params {
		params[i] = "_"
	}
	zero := jsZeroFor(t.Sig.Return)
	return "(" + strings.Join(params, ", ") + ") => " + zero
}

func jsZeroFor(t *ir.Type) string {
	if t == nil {
		return "null"
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat:
		return "0"
	case ir.TypeBool:
		return "false"
	case ir.TypeString:
		return `""`
	case ir.TypeList:
		return "[]"
	}
	return "null"
}

func (jc *JsIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "() => null"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := jc.EvalExpr(ret.Value)
			if len(params) == 1 {
				return params[0] + " => " + body
			}
			return "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString("(" + strings.Join(params, ", ") + ") => {\n")
	for _, stmt := range n.Func.Block {
		for _, line := range jc.EvalStmt(stmt) {
			b.WriteString("  " + line + ";\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func (jc *JsIRContext) evalCallArgs(args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = jc.EvalExpr(a.Value)
	}
	return out
}

func (jc *JsIRContext) evalMutTarget(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		_, kind := jc.Ctx.Resolve(n.Name)
		if kind == codegen.NameStateVar {
			return "state." + n.Name
		}
		if kind == codegen.NameLocal {
			return jc.Ctx.RenamedName(n.Name)
		}
		return n.Name
	case *ir.Select:
		operand := jc.evalMutTarget(n.Operand)
		return operand + "." + n.Field
	case *ir.Index:
		operand := jc.evalMutTarget(n.Operand)
		idx := jc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	default:
		return jc.EvalExpr(e)
	}
}

// WithLocal returns a new context with an additional local variable.
func (jc *JsIRContext) WithLocal(name string) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.WithLocal(name),
		EventVar: jc.EventVar,
	}
}

// WithEvent returns a clone with EventVar set.
func (jc *JsIRContext) WithEvent(eventVar string) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.Clone(),
		EventVar: eventVar,
	}
}

// ForComponent returns a new context scoped to a component.
func (jc *JsIRContext) ForComponent(comp *ir.Component) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.ForComponent(comp),
		EventVar: jc.EventVar,
	}
}

// --- JS builtin methods ---

func jsBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "undefined"
	}

	switch qualName {
	case "int.min", "*.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "Math.abs(" + a(0) + ")"
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
	case "list.length":
		return a(0) + ".length"
	case "list.join", "*.join":
		return a(0) + ".join(" + a(1) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	case "list.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "list.reverse", "*.reverse":
		return "[..." + a(0) + "].reverse()"
	case "float.floor", "*.floor":
		return "Math.floor(" + a(0) + ")"
	case "float.ceil", "*.ceil":
		return "Math.ceil(" + a(0) + ")"
	case "float.round", "*.round":
		return "Math.round(" + a(0) + ")"
	case "float.sqrt", "*.sqrt":
		return "Math.sqrt(" + a(0) + ")"
	// map
	case "map.length":
		return a(0) + ".size"
	case "map.keys":
		return "Array.from(" + a(0) + ".keys())"
	case "map.values":
		return "Array.from(" + a(0) + ".values())"
	case "map.contains":
		return a(0) + ".has(" + a(1) + ")"
	case "map.get":
		return "(" + a(0) + ".has(" + a(1) + ") ? " + a(0) + ".get(" + a(1) + ") : " + a(2) + ")"
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
	// i18n — all calls delegate to i18n.getTranslator() from the JS runtime.
	case "i18n.tr":
		// Args: key string, args map — pass key twice (key + inlinedTemplate).
		return "i18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
	case "i18n.format":
		// Args: template string, args map.
		return "i18n.getTranslator().format(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberInt":
		// Args: n int, style string.
		return "i18n.getTranslator().numberInt(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberFloat":
		// Args: n float, style string.
		return "i18n.getTranslator().numberFloat(" + a(0) + ", " + a(1) + ")"
	case "i18n.date":
		// Args: d date, style string.
		return "i18n.getTranslator().date(" + a(0) + ", " + a(1) + ")"
	case "i18n.time":
		// Args: t time, style string.
		return "i18n.getTranslator().time(" + a(0) + ", " + a(1) + ")"
	case "i18n.datetime":
		// Args: dt dateTime, dateStyle string, timeStyle string.
		return "i18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.select":
		// Args: value string, cases map.
		return "i18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
	case "i18n.plural":
		// Namespace-call shape: a(0) is the "i18n" receiver; real args at a(1), a(2).
		return "i18n.getTranslator().plural(" + a(1) + ", " + a(2) + ")"
	case "i18n.selectordinal":
		// Namespace-call shape: a(0) is the "i18n" receiver; real args at a(1), a(2).
		return "i18n.getTranslator().selectordinal(" + a(1) + ", " + a(2) + ")"
	case "i18n.exactly":
		// Namespace-call shape: a(0) is the "i18n" receiver; real arg is a(1).
		return "(\"=\" + (" + a(1) + "))"
	}
	return ""
}

// jsI18nConstString returns the JS string literal for a predeclared
// i18n.PluralKey constant. Returns "" for non-matches.
func jsI18nConstString(qual string) string {
	switch qual {
	case "i18n.zero":
		return `"zero"`
	case "i18n.one":
		return `"one"`
	case "i18n.two":
		return `"two"`
	case "i18n.few":
		return `"few"`
	case "i18n.many":
		return `"many"`
	case "i18n.other":
		return `"other"`
	}
	return ""
}
