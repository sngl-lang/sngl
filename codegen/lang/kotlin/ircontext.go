package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/irwalk"
	"git.duckfam.us/jonathan/sngl/ir"
)

// KtIRContext translates IR expressions and statements into Kotlin code.
type KtIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string // what "event" maps to in current handler scope
	// IdentRewrites remaps bare identifiers regardless of scope kind.
	// Used by the Android test-mode emit to route every component-level
	// var through a hoisted state object (`count` → `state.count`).
	IdentRewrites map[string]string
}

// NewIRContext creates a KtIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *KtIRContext {
	return &KtIRContext{Ctx: ctx}
}

// EvalExpr translates an IR expression into a Kotlin expression string.
func (kc *KtIRContext) EvalExpr(e ir.Expr) string { return irwalk.EvalExpr(kc, e) }

// EvalStmt translates an IR statement into Kotlin statement strings.
func (kc *KtIRContext) EvalStmt(s ir.Stmt) []string { return irwalk.EvalStmt(kc, s) }

// --- irwalk.Renderer implementation ---

func (kc *KtIRContext) NilExpr() string              { return "null" }
func (kc *KtIRContext) Literal(n *ir.Literal) string { return kc.evalLiteral(n) }
func (kc *KtIRContext) Ident(n *ir.Ident) string     { return kc.evalIdent(n) }

func (kc *KtIRContext) Binary(n *ir.Binary, left, right string) string {
	return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
}
func (kc *KtIRContext) Unary(n *ir.Unary, operand string) string {
	if n.Op == ast.UnaryNot {
		return "!" + operand
	}
	return "-" + operand
}
func (kc *KtIRContext) Ternary(_ *ir.Ternary, cond, then_, else_ string) string {
	return "(if (" + cond + ") " + then_ + " else " + else_ + ")"
}
func (kc *KtIRContext) Select(n *ir.Select, operand string) string {
	field := n.Field
	if field == "length" {
		if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeList {
			field = "size"
		}
	}
	return operand + "." + field
}
func (kc *KtIRContext) Index(n *ir.Index, operand, idx string) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
		valZero := ktMapValZero(t)
		return "(" + operand + "[" + idx + "] ?: " + valZero + ")"
	}
	return operand + "[" + idx + "]"
}
func (kc *KtIRContext) ListLit(_ *ir.ListLit, elems []string) string {
	return "listOf(" + strings.Join(elems, ", ") + ")"
}
func (kc *KtIRContext) MapLit(_ *ir.MapLitIR, keys, vals []string) string {
	var b strings.Builder
	b.WriteString("mapOf(")
	for i := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(keys[i])
		b.WriteString(" to ")
		b.WriteString(vals[i])
	}
	b.WriteString(")")
	return b.String()
}
func (kc *KtIRContext) StructLit(n *ir.StructLit, fieldStrs []string) string {
	name := "Any"
	if n.Def != nil {
		name = exportName(n.Def.Name)
	}
	parts := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		if f.Spread {
			parts[i] = "/* ..." + fieldStrs[i] + " */"
		} else {
			parts[i] = f.Name + " = " + fieldStrs[i]
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}
func (kc *KtIRContext) Spread(_ *ir.Spread, operand string) string { return "*" + operand }

func (kc *KtIRContext) Call(n *ir.Call) string             { return kc.evalCall(n) }
func (kc *KtIRContext) Conversion(n *ir.Conversion) string { return kc.evalConversion(n) }
func (kc *KtIRContext) Lambda(n *ir.Lambda) string         { return kc.evalLambda(n) }

func (kc *KtIRContext) AssignText(n *ir.Assign, target, value string) string {
	return target + " " + assignOpStr(n.Op) + " " + value
}
func (kc *KtIRContext) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target
}
func (kc *KtIRContext) CallStmtLines(n *ir.CallStmt) []string {
	if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
		if lines := kc.evalErrorAwareCall(n.Call); lines != nil {
			return lines
		}
	}
	return []string{kc.EvalExpr(n.Call)}
}
func (kc *KtIRContext) EmitText(n *ir.Emit, argStrs []string) string {
	name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
	if len(argStrs) > 0 {
		return name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"
	}
	return name + "?.invoke()"
}
func (kc *KtIRContext) LocalVarText(n *ir.LocalVar, initStr string) string {
	if n.Init != nil {
		return "var " + n.Name + " = " + initStr
	}
	goType := "Any"
	if n.Type != nil {
		goType = IRTypeToKt(n.Type)
	}
	return "var " + n.Name + ": " + goType
}
func (kc *KtIRContext) ReturnText(n *ir.Return, valueStr string) string {
	if n.Value != nil {
		return "return " + valueStr
	}
	return "return"
}

func (kc *KtIRContext) ForHead(n *ir.For, iter string) string {
	iterType := n.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		// Map iteration: for ((k, v) in m) { ... }
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for ((%s, %s) in %s) {", n.Key, valueVar, iter)
	}
	// List / iter<T> iteration: for (x in list) { ... }
	return fmt.Sprintf("for (%s in %s) {", n.Key, iter)
}
func (kc *KtIRContext) IfHead(_ *ir.If, cond string) string { return "if (" + cond + ") {" }
func (kc *KtIRContext) ElseHead() string                    { return "} else {" }
func (kc *KtIRContext) BlockEnd() string                    { return "}" }
func (kc *KtIRContext) Indent() string                      { return "\t" }

func (kc *KtIRContext) MutTargetIdent(n *ir.Ident) string {
	if kc.IdentRewrites != nil {
		if rewritten, ok := kc.IdentRewrites[n.Name]; ok {
			return rewritten
		}
	}
	return n.Name
}
func (kc *KtIRContext) MutTargetField(field string) string { return field }

func (kc *KtIRContext) StmtPrefix(_ ir.Stmt) []string { return nil }

func (kc *KtIRContext) Scoped(name string) irwalk.Renderer { return kc.WithLocal(name) }

func (kc *KtIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Raw
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Raw)
	case ir.TypeInt:
		return n.Raw
	case ir.TypeFloat:
		s := n.Raw
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case ir.TypeBool:
		return n.Raw
	case ir.TypeNull:
		return "null"
	case ir.TypeColor:
		return fmt.Sprintf("%q", n.Raw)
	default:
		return n.Raw
	}
}

func (kc *KtIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		// Enum member: emit qualified Kotlin enum value (Gender.female)
		// so the value matches the declared enum type at the use site.
		if n.Type != nil && n.Type.Kind == ir.TypeEnum && n.Type.Decl != nil {
			return exportName(n.Type.Decl.SymName()) + "." + n.Member
		}
		return fmt.Sprintf("%q", n.Member)
	}
	name := n.Name
	if kc.IdentRewrites != nil {
		if rewritten, ok := kc.IdentRewrites[name]; ok {
			return rewritten
		}
	}
	_, kind := kc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return kc.Ctx.RenamedName(name)
	default:
		return name
	}
}

func (kc *KtIRContext) evalCall(n *ir.Call) string {
	if n.Receiver != nil {
		return kc.evalNamespaceCall(n)
	}
	if n.Func != nil && n.Func.Receiver != "" {
		return kc.evalTypeMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := kc.evalCallArgs(n.Args)
		switch fname {
		case "string":
			if len(args) == 1 {
				return args[0] + ".toString()"
			}
		case "int":
			if len(args) == 1 {
				return args[0] + ".toInt()"
			}
		case "float":
			if len(args) == 1 {
				return args[0] + ".toDouble()"
			}
		}
		// IdentRewrites can redirect a bare func call to a method
		// on a hoisted state object (e.g. `label()` →
		// `state.label()` in Android test mode).
		if kc.IdentRewrites != nil {
			if rewritten, ok := kc.IdentRewrites[fname]; ok {
				return rewritten + "(" + strings.Join(args, ", ") + ")"
			}
		}
		return fname + "(" + strings.Join(args, ", ") + ")"
	}
	args := kc.evalCallArgs(n.Args)
	if n.Callee != nil {
		return kc.EvalExpr(n.Callee) + "(" + strings.Join(args, ", ") + ")"
	}
	return "(" + strings.Join(args, ", ") + ")"
}

func (kc *KtIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := kc.EvalExpr(n.Receiver)
	args := kc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		receiverName := n.Func.Receiver
		qualName := receiverName + "." + fname

		// Intrinsic dispatch: stdlib intrinsics that map to per-locale
		// runtime entry points. After NoContext + InlinePure, i18n.*
		// wrapper calls have been lowered to direct intl.* intrinsic
		// calls with the locale threaded as the first arg.
		if result := kotlinEvalIntlIntrinsic(n.Func, args); result != "" {
			return result
		}

		// For i18n.* calls the namespace receiver is the module object, not a
		// value argument. Pass only the real call args to the builtin dispatcher
		// so that a(0) is the first semantic argument (matches type-method path).
		if receiverName == "i18n" {
			if result := kotlinBuiltinMethodFromArgs(qualName, args); result != "" {
				return result
			}
		}

		allArgs := append([]string{receiver}, args...)
		if result := kotlinBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		if result := kotlinBuiltinMethodFromArgs("*."+fname, allArgs); result != "" {
			return result
		}
		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}
	// Func resolution may be incomplete (e.g. checker did not bind a
	// `c.label()` call to a specific IR func because the receiver is
	// a component-typed parameter). Recover the method name from the
	// AST so we still emit `receiver.method(args)` rather than
	// `receiver(args)`.
	if n.AST != nil {
		if sel, ok := n.AST.Func.(*ast.SelectExpr); ok && sel.Field != "" {
			return receiver + "." + sel.Field + "(" + strings.Join(args, ", ") + ")"
		}
	}
	return receiver + "(" + strings.Join(args, ", ") + ")"
}

// evalErrorAwareCall emits Kotlin statements for a fallible call whose
// error handler was resolved by effect analysis. Only error.raise is
// recognised in MVP; returns nil otherwise (callers fall back to normal
// expression emission).
func (kc *KtIRContext) evalErrorAwareCall(call *ir.Call) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !ktIsRaiseFunc(call.Func) {
		return nil
	}
	msg := `""`
	kind := `""`
	if len(call.Args) >= 1 {
		msg = kc.EvalExpr(call.Args[0].Value)
	}
	if len(call.Args) >= 2 {
		kind = kc.EvalExpr(call.Args[1].Value)
	}
	evt := fmt.Sprintf("ErrorEvent(%s, %s)", msg, kind)

	switch call.ErrorMode {
	case ir.ErrorPropagateNative, ir.ErrorBubble:
		return []string{"throw RuntimeException(" + msg + ")"}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler == nil || call.ResolvedHandler.Func == nil {
			return []string{"throw RuntimeException(" + msg + ")"}
		}
		return kc.emitHandlerInvoke(evt, call.ResolvedHandler)
	case ir.ErrorPerCall:
		if call.ErrorHandler == nil || call.ErrorHandler.Func == nil {
			return []string{"val _e = " + evt}
		}
		return kc.emitHandlerInvoke(evt, call.ErrorHandler)
	}
	return nil
}

func (kc *KtIRContext) emitHandlerInvoke(evt string, handler *ir.EventHandler) []string {
	paramName := "e"
	if handler.Func != nil && len(handler.Func.Params) > 0 {
		paramName = handler.Func.Params[0].Name
	}
	lines := []string{
		"run {",
		fmt.Sprintf("\tval %s = %s", paramName, evt),
	}
	for _, stmt := range handler.Func.Block {
		for _, l := range kc.EvalStmt(stmt) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

func ktIsRaiseFunc(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "ErrorRaise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

func (kc *KtIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	args := kc.evalCallArgs(n.Args)
	if result := kotlinBuiltinMethodFromArgs(qualName, args); result != "" {
		return result
	}
	if result := kotlinBuiltinMethodFromArgs("*."+method, args); result != "" {
		return result
	}

	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

func (kc *KtIRContext) evalConversion(n *ir.Conversion) string {
	if isNullToFuncConvKt(n) {
		return nullFuncStubKt(n.Type)
	}
	operand := kc.EvalExpr(n.Operand)
	if n.Type != nil {
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
	}
	return operand
}

func isNullToFuncConvKt(n *ir.Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != ir.TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

func nullFuncStubKt(t *ir.Type) string {
	if t == nil || t.Sig == nil {
		return "{ }"
	}
	arity := len(t.Sig.Params)
	params := make([]string, arity)
	for i := range params {
		params[i] = "_"
	}
	zero := ktZeroFor(t.Sig.Return)
	if arity == 0 {
		return "{ " + zero + " }"
	}
	return "{ " + strings.Join(params, ", ") + " -> " + zero + " }"
}

func ktZeroFor(t *ir.Type) string {
	if t == nil {
		return "Unit"
	}
	switch t.Kind {
	case ir.TypeInt:
		return "0"
	case ir.TypeFloat:
		return "0.0"
	case ir.TypeBool:
		return "false"
	case ir.TypeString:
		return `""`
	case ir.TypeList:
		return "listOf()"
	}
	return "null"
}

func (kc *KtIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "{ }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := kc.EvalExpr(ret.Value)
			return "{ " + strings.Join(params, ", ") + " -> " + body + " }"
		}
	}
	var b strings.Builder
	b.WriteString("{ " + strings.Join(params, ", ") + " ->\n")
	for _, stmt := range n.Func.Block {
		for _, line := range kc.EvalStmt(stmt) {
			b.WriteString("    " + line + "\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func (kc *KtIRContext) evalCallArgs(args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = kc.EvalExpr(a.Value)
	}
	return out
}

// WithLocal returns a new context with an additional local variable.
func (kc *KtIRContext) WithLocal(name string) *KtIRContext {
	return &KtIRContext{
		Ctx:           kc.Ctx.WithLocal(name),
		EventVar:      kc.EventVar,
		IdentRewrites: kc.IdentRewrites,
	}
}

// ForComponent returns a new context scoped to a component.
func (kc *KtIRContext) ForComponent(comp *ir.Component) *KtIRContext {
	return &KtIRContext{
		Ctx:           kc.Ctx.ForComponent(comp),
		EventVar:      kc.EventVar,
		IdentRewrites: kc.IdentRewrites,
	}
}

// --- IR type → Kotlin type ---

// IRTypeToKt converts an IR type to a Kotlin type string.
func IRTypeToKt(t *ir.Type) string {
	if t == nil {
		return "Any"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "Boolean"
	case ir.TypeInt:
		return "Int"
	case ir.TypeFloat:
		return "Double"
	case ir.TypeString, ir.TypeColor,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname,
		ir.TypeDecimal:
		return "String"
	case ir.TypeList:
		if len(t.Elems) > 0 {
			return "List<" + IRTypeToKt(t.Elems[0]) + ">"
		}
		return "List<Any>"
	case ir.TypeMap:
		if len(t.Elems) == 2 {
			return "Map<" + IRTypeToKt(t.Elems[0]) + ", " + IRTypeToKt(t.Elems[1]) + ">"
		}
		return "Map<Any, Any>"
	case ir.TypeOption:
		if len(t.Elems) > 0 {
			return IRTypeToKt(t.Elems[0]) + "?"
		}
		return "Any?"
	case ir.TypeStruct:
		if t.Decl != nil {
			// i18n.PluralKey is represented as String in Kotlin — the Kotlin i18n
			// runtime uses string plural categories exclusively.
			if t.Decl.SymName() == "PluralKey" {
				return "String"
			}
			return exportName(t.Decl.SymName())
		}
		return "Any"
	case ir.TypeEnum:
		if t.Decl != nil {
			return exportName(t.Decl.SymName())
		}
		return "String"
	case ir.TypeUnit:
		if t.Decl != nil && t.Decl.SymName() == "duration" {
			return "Long" // milliseconds
		}
		return "String"
	case ir.TypeFunc:
		return "Any" // TODO: proper function types
	case ir.TypeNull:
		return "Any?"
	default:
		return "Any"
	}
}

// IRLiteralToKt converts an IR literal expression to a Kotlin literal.
func IRLiteralToKt(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	switch n := e.(type) {
	case *ir.Literal:
		if n.Type == nil {
			return n.Raw
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt:
			return n.Raw
		case ir.TypeFloat:
			s := n.Raw
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
		case ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "null"
		case ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		default:
			return n.Raw
		}
	case *ir.ListLit:
		if len(n.Elems) == 0 {
			return "emptyList()"
		}
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = IRLiteralToKt(el)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ir.MapLitIR:
		if len(n.Entries) == 0 {
			return "emptyMap()"
		}
		var b strings.Builder
		b.WriteString("mapOf(")
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(IRLiteralToKt(e.Key))
			b.WriteString(" to ")
			b.WriteString(IRLiteralToKt(e.Value))
		}
		b.WriteString(")")
		return b.String()
	case *ir.StructLit:
		name := "Any"
		if n.Def != nil {
			name = exportName(n.Def.Name)
		}
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, f.Name+" = "+IRLiteralToKt(f.Value))
		}
		return name + "(" + strings.Join(parts, ", ") + ")"
	case *ir.Lambda:
		return irLambdaLiteralToKt(n)
	}
	return `""`
}

// irLambdaLiteralToKt emits a Kotlin lambda literal for a synthetic zero-value
// lambda (body is always a single Return of another IR literal).
func irLambdaLiteralToKt(n *ir.Lambda) string {
	if n.Func == nil {
		return "{}"
	}
	body := ""
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body = IRLiteralToKt(ret.Value)
		}
	}
	if len(n.Func.Params) == 0 {
		return "{ " + body + " }"
	}
	placeholders := make([]string, len(n.Func.Params))
	for i := range n.Func.Params {
		placeholders[i] = "_"
	}
	return "{ " + strings.Join(placeholders, ", ") + " -> " + body + " }"
}

// kotlinBuiltinMethodFromArgs checks for builtin Kotlin methods.
func kotlinBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "null"
	}

	switch qualName {
	case "int.min", "*.min":
		return "minOf(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "maxOf(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "kotlin.math.abs(" + a(0) + ")"
	case "string.length", "*.length":
		return a(0) + ".length"
	case "string.upper", "*.upper":
		return a(0) + ".uppercase()"
	case "string.lower", "*.lower":
		return a(0) + ".lowercase()"
	case "string.trim", "*.trim":
		return a(0) + ".trim()"
	case "string.replace", "*.replace":
		return a(0) + ".replace(" + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "string.contains":
		return a(0) + ".contains(" + a(1) + ")"
	case "list.length":
		return a(0) + ".size"
	case "list.push":
		return a(0) + ".add(" + a(1) + ")"
	case "list.remove":
		return a(0) + ".removeAt(" + a(1) + ")"
	case "list.join", "*.join":
		return a(0) + ".joinToString(" + a(1) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	// map
	case "map.length":
		return a(0) + ".size"
	case "map.keys":
		return a(0) + ".keys.toList()"
	case "map.values":
		return a(0) + ".values.toList()"
	case "map.contains":
		return a(0) + ".containsKey(" + a(1) + ")"
	case "map.get":
		return a(0) + ".getOrDefault(" + a(1) + ", " + a(2) + ")"
	// i18n — wrapper calls delegate to per-locale runtime entry points.
	// NoContext threads __ctx_locale as the trailing arg; we lift it to the
	// leading positional arg the runtime expects (I18n.<foo>(locale, ...)).
	// Falls back to I18n.getTranslator() (process-global) when no locale arg
	// was threaded — e.g. legacy callers reached before NoContext runs.
	case "i18n.tr":
		// Wrapper params: (key, args, __ctx_locale).
		if len(argExprs) >= 3 {
			return "I18n.translate(" + a(2) + ", " + a(0) + ", \"\", " + a(1) + ")"
		}
		return "I18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
	case "i18n.trInline":
		// Wrapper params: (key, inlinedTemplate, args, __ctx_locale).
		if len(argExprs) >= 4 {
			return "I18n.translate(" + a(3) + ", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
		}
		return "I18n.getTranslator().tr(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.format":
		// Wrapper params: (template, args, __ctx_locale).
		if len(argExprs) >= 3 {
			return "I18n.format(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().format(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberInt":
		if len(argExprs) >= 3 {
			return "I18n.numberInt(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().numberInt(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberFloat":
		if len(argExprs) >= 3 {
			return "I18n.numberFloat(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().numberFloat(" + a(0) + ", " + a(1) + ")"
	case "i18n.date":
		if len(argExprs) >= 3 {
			return "I18n.date(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().date(" + a(0) + ", " + a(1) + ")"
	case "i18n.time":
		if len(argExprs) >= 3 {
			return "I18n.time(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().time(" + a(0) + ", " + a(1) + ")"
	case "i18n.datetime":
		// Wrapper params: (dt, dateStyle, timeStyle, __ctx_locale).
		if len(argExprs) >= 4 {
			return "I18n.datetime(" + a(3) + ", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
		}
		return "I18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.select":
		// Kotlin runtime exposes selectStr (avoiding the `select` keyword clash).
		if len(argExprs) >= 3 {
			return "I18n.selectStr(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
	case "i18n.plural":
		if len(argExprs) >= 3 {
			return "I18n.plural(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().plural(" + a(0) + ", " + a(1) + ")"
	case "i18n.selectordinal":
		if len(argExprs) >= 3 {
			return "I18n.selectordinal(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "I18n.getTranslator().selectordinal(" + a(0) + ", " + a(1) + ")"
	case "i18n.exactly":
		// Args: n. a(0)=n. Returns a PluralKey string like "=0".
		return "(\"=\" + (" + a(0) + "))"
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "I18n.defaultLocale()"
	}
	return ""
}

// kotlinI18nConstString returns the Kotlin string literal for a predeclared
// i18n.PluralKey constant. Returns "" for non-matches.
func kotlinI18nConstString(qual string) string {
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

// isPluralKeyMapType reports whether t is map<i18n.PluralKey, V>.
// The Kotlin translator lowers such maps to mapOf() with string keys,
// because the Kotlin i18n runtime uses string plural categories exclusively.
func isPluralKeyMapType(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeMap || len(t.Elems) < 1 {
		return false
	}
	k := t.Elems[0]
	if k == nil || k.Kind != ir.TypeStruct || k.Decl == nil {
		return false
	}
	return k.Decl.SymName() == "PluralKey"
}

// ktMapValZero returns the Kotlin zero value for the value type of a map IR type.
func ktMapValZero(t *ir.Type) string {
	if t == nil || len(t.Elems) < 2 {
		return "null"
	}
	return ktZeroFor(t.Elems[1])
}
