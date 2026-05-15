package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
func (kc *KtIRContext) EvalExpr(e ir.Expr) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return kc.evalLiteral(n)
	case *ir.Ident:
		return kc.evalIdent(n)
	case *ir.Binary:
		left := kc.EvalExpr(n.Left)
		right := kc.EvalExpr(n.Right)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := kc.EvalExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		cond := kc.EvalExpr(n.Cond)
		a := kc.EvalExpr(n.Then)
		b := kc.EvalExpr(n.Else)
		return "(if (" + cond + ") " + a + " else " + b + ")"
	case *ir.Select:
		operand := kc.EvalExpr(n.Operand)
		field := n.Field
		if field == "length" {
			if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeList {
				field = "size"
			}
		}
		return operand + "." + field
	case *ir.Index:
		operand := kc.EvalExpr(n.Operand)
		idx := kc.EvalExpr(n.Idx)
		if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
			valZero := ktMapValZero(t)
			return "(" + operand + "[" + idx + "] ?: " + valZero + ")"
		}
		return operand + "[" + idx + "]"
	case *ir.Call:
		return kc.evalCall(n)
	case *ir.Conversion:
		return kc.evalConversion(n)
	case *ir.StructLit:
		return kc.evalStructLit(n)
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = kc.EvalExpr(el)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ir.MapLitIR:
		var b strings.Builder
		b.WriteString("mapOf(")
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(kc.EvalExpr(e.Key))
			b.WriteString(" to ")
			b.WriteString(kc.EvalExpr(e.Value))
		}
		b.WriteString(")")
		return b.String()
	case *ir.Spread:
		return "*" + kc.EvalExpr(n.Operand)
	case *ir.Lambda:
		return kc.evalLambda(n)
	default:
		return fmt.Sprintf("/* unsupported IR node %T */null", e)
	}
}

// EvalStmt translates an IR statement into Kotlin statement strings.
func (kc *KtIRContext) EvalStmt(s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		target := kc.evalMutTarget(n.Target)
		value := kc.EvalExpr(n.Value)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := kc.evalMutTarget(n.Target)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
			if lines := kc.evalErrorAwareCall(n.Call); lines != nil {
				return lines
			}
		}
		return []string{kc.EvalExpr(n.Call)}
	case *ir.Emit:
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = kc.EvalExpr(a.Value)
		}
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"var " + n.Name + " = " + kc.EvalExpr(n.Init)}
		}
		goType := "Any"
		if n.Type != nil {
			goType = IRTypeToKt(n.Type)
		}
		return []string{"var " + n.Name + ": " + goType}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + kc.EvalExpr(n.Value)}
		}
		return []string{"return"}
	case *ir.For:
		return kc.evalFor(n)
	default:
		return []string{"// unsupported IR stmt: " + fmt.Sprintf("%T", s)}
	}
}

// evalFor emits a Kotlin for-loop. Maps use destructuring; lists/iter<T> use for-in.
func (kc *KtIRContext) evalFor(n *ir.For) []string {
	iterExpr := kc.EvalExpr(n.Iter)
	loopKC := kc.WithLocal(n.Key)
	if n.Value != "" {
		loopKC = loopKC.WithLocal(n.Value)
	}

	var lines []string
	iterType := n.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		// Map iteration: for ((k, v) in m) { ... }
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		lines = append(lines, fmt.Sprintf("for ((%s, %s) in %s) {", n.Key, valueVar, iterExpr))
	} else {
		// List / iter<T> iteration: for (x in list) { ... }
		lines = append(lines, fmt.Sprintf("for (%s in %s) {", n.Key, iterExpr))
	}
	for _, stmt := range n.Body {
		for _, l := range loopKC.EvalStmt(stmt) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

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

func (kc *KtIRContext) evalStructLit(n *ir.StructLit) string {
	name := "Any"
	if n.Def != nil {
		name = exportName(n.Def.Name)
	}
	var parts []string
	for _, f := range n.Fields {
		if f.Spread {
			parts = append(parts, "/* ..."+kc.EvalExpr(f.Value)+" */")
		} else {
			parts = append(parts, f.Name+" = "+kc.EvalExpr(f.Value))
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
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

func (kc *KtIRContext) evalMutTarget(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		if kc.IdentRewrites != nil {
			if rewritten, ok := kc.IdentRewrites[n.Name]; ok {
				return rewritten
			}
		}
		return n.Name
	case *ir.Select:
		operand := kc.evalMutTarget(n.Operand)
		return operand + "." + n.Field
	case *ir.Index:
		operand := kc.evalMutTarget(n.Operand)
		idx := kc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	default:
		return kc.EvalExpr(e)
	}
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
	// i18n — all calls delegate to I18n.getTranslator() from the Kotlin runtime.
	case "i18n.tr":
		// Args: a(0)=key/template, a(1)=argsMap. The Kotlin runtime's
		// Translator.tr(key, inlinedTemplate, args) takes three arguments.
		// The template string doubles as both the lookup key and the inline
		// fallback, so it is passed twice.
		return "I18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
	case "i18n.format":
		// Args: template string, args map.
		return "I18n.getTranslator().format(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberInt":
		// Args: n int, style string.
		return "I18n.getTranslator().numberInt(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberFloat":
		// Args: n float (Double), style string.
		return "I18n.getTranslator().numberFloat(" + a(0) + ", " + a(1) + ")"
	case "i18n.date":
		// Args: d date, style string.
		return "I18n.getTranslator().date(" + a(0) + ", " + a(1) + ")"
	case "i18n.time":
		// Args: t time, style string.
		return "I18n.getTranslator().time(" + a(0) + ", " + a(1) + ")"
	case "i18n.datetime":
		// Args: dt dateTime, dateStyle string, timeStyle string.
		return "I18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.select":
		// Args: value string, cases map.
		return "I18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
	case "i18n.plural":
		// Args: count, forms. a(0)=count, a(1)=forms.
		return "I18n.getTranslator().plural(" + a(0) + ", " + a(1) + ")"
	case "i18n.selectordinal":
		// Args: count, forms. a(0)=count, a(1)=forms.
		return "I18n.getTranslator().selectordinal(" + a(0) + ", " + a(1) + ")"
	case "i18n.exactly":
		// Args: n. a(0)=n. Returns a PluralKey string like "=0".
		return "(\"=\" + (" + a(0) + "))"
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
