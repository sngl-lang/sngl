package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// GoIRContext translates IR expressions and statements into Go code.
// It replaces GoContext for platforms that have been ported to IR.
type GoIRContext struct {
	Ctx *codegen.ExprCtx

	// AlertFunc translates Alert.toast/info/warn/error calls.
	// If nil, a default "m.toasts = append(...)" implementation is used.
	AlertFunc func(ctx *GoIRContext, method string, args []ir.CallArg) []string
}

// NewIRContext creates a GoIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *GoIRContext {
	return &GoIRContext{Ctx: ctx}
}

// EvalExpr translates an IR expression into a Go expression string.
func (gc *GoIRContext) EvalExpr(e ir.Expr) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return gc.evalLiteral(n)
	case *ir.Ident:
		return gc.evalIdent(n)
	case *ir.Binary:
		left := gc.EvalExpr(n.Left)
		right := gc.EvalExpr(n.Right)
		return "(" + left + " " + irBinaryOp(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := gc.EvalExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		cond := gc.EvalExpr(n.Cond)
		a := gc.EvalExpr(n.Then)
		b := gc.EvalExpr(n.Else)
		return "ternary(" + cond + ", " + a + ", " + b + ")"
	case *ir.Select:
		operand := gc.EvalExpr(n.Operand)
		if n.Field == "length" {
			return "len(" + operand + ")"
		}
		return operand + "." + ExportName(n.Field)
	case *ir.Index:
		operand := gc.EvalExpr(n.Operand)
		idx := gc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	case *ir.Call:
		return gc.maybeWrapErrorReturn(n, gc.evalCall(n))
	case *ir.Conversion:
		return gc.evalConversion(n)
	case *ir.StructLit:
		return gc.evalStructLit(n)
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = gc.EvalExpr(el)
		}
		elemType := "any"
		if n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) > 0 {
			elemType = IRTypeToGo(n.Type.Elems[0])
		}
		return "[]" + elemType + "{" + strings.Join(parts, ", ") + "}"
	case *ir.Spread:
		return gc.EvalExpr(n.Operand) + "..."
	case *ir.Lambda:
		return gc.evalLambda(n)
	default:
		return fmt.Sprintf("/* unsupported IR node %T */nil", e)
	}
}

// EvalStmt translates an IR statement into Go statement strings.
func (gc *GoIRContext) EvalStmt(s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		target := gc.evalMutTarget(n.Target)
		value := gc.EvalExpr(n.Value)
		op := irAssignOp(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := gc.evalMutTarget(n.Target)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
			if lines := gc.evalErrorAwareCall(n.Call); lines != nil {
				return lines
			}
		}
		return []string{gc.EvalExpr(n.Call)}
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = gc.EvalExpr(a.Value)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{n.Name + " := " + gc.EvalExpr(n.Init)}
		}
		goType := "any"
		if n.Type != nil {
			goType = IRTypeToGo(n.Type)
		}
		return []string{"var " + n.Name + " " + goType}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + gc.EvalExpr(n.Value)}
		}
		return []string{"return"}
	default:
		return []string{fmt.Sprintf("// unsupported IR stmt: %T", s)}
	}
}

func (gc *GoIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Raw
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Raw)
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return n.Raw
	case ir.TypeNull:
		return "nil"
	case ir.TypeColor:
		return fmt.Sprintf("%q", n.Raw)
	default:
		if n.Suffix != "" {
			return fmt.Sprintf("%q", n.Raw+n.Suffix)
		}
		return n.Raw
	}
}

func (gc *GoIRContext) evalIdent(n *ir.Ident) string {
	// Bare enum member — emit as string literal
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}

	name := n.Name
	_, kind := gc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return gc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return "m." + name + "()"
	case codegen.NameStateVar:
		return "m." + name
	case codegen.NameConst:
		return name
	case codegen.NameFunc:
		return "m." + ExportName(name)
	case codegen.NameExternFunc, codegen.NameExternVar:
		return "m." + ExportName(name)
	default:
		// If the type is an enum, emit as string
		if n.Type != nil && n.Type.Kind == ir.TypeEnum {
			return fmt.Sprintf("%q", name)
		}
		return name
	}
}

// maybeWrapErrorReturn wraps a native call whose imported signature is
// (T, error) so the value can be used as a single Go expression. Without this,
// calls like `m.issues = jira.Search(...)` lower to `m.issues = jira.Search(...)`
// — a 2-value RHS against a 1-value LHS — and fail to compile. The Go importer
// already strips the trailing `error` and records `HasErrorReturn` on
// `ir.Func`; we honor that here for all eval paths (namespace calls, type
// methods, plain resolved funcs).
//
// The wrap is an IIFE — `func() T { v, _ := f(args); return v }()` — chosen
// over a top-level helper to keep this fix local to ircontext.go and avoid
// threading "needs helper" plumbing through every platform's emit pipeline.
// Errors are silently discarded; the http.go path emits a logging
// `nativeMustOK` helper for HTTP-action handlers, which is unchanged.
func (gc *GoIRContext) maybeWrapErrorReturn(n *ir.Call, raw string) string {
	if n.Func == nil || !n.Func.HasErrorReturn || n.Func.Return == nil {
		return raw
	}
	rt := IRTypeToGo(n.Func.Return)
	return fmt.Sprintf("func() %s { v, _ := %s; return v }()", rt, raw)
}

func (gc *GoIRContext) evalCall(n *ir.Call) string {
	// Namespace / component call — Receiver expression preserved.
	if n.Receiver != nil {
		return gc.evalNamespaceCall(n)
	}

	// Type-attached method call (checker-normalized: Func.Receiver set,
	// Args[0] is the receiver value).
	if n.Func != nil && n.Func.Receiver != "" {
		return gc.evalTypeMethodCall(n)
	}

	// Resolved function
	if n.Func != nil {
		fname := n.Func.Name
		args := gc.evalCallArgs(n.Args)

		// Builtin conversions
		switch fname {
		case "string":
			if len(args) == 1 {
				return "fmt.Sprint(" + args[0] + ")"
			}
		case "int":
			if len(args) == 1 {
				return "int(" + args[0] + ")"
			}
		case "float":
			if len(args) == 1 {
				return "float64(" + args[0] + ")"
			}
		case "size":
			if len(args) == 1 {
				return "len(" + args[0] + ")"
			}
		}

		return fname + "(" + strings.Join(args, ", ") + ")"
	}

	// Unresolved function (func-typed var, etc.) — evaluate the callee expr.
	args := gc.evalCallArgs(n.Args)
	if n.Callee != nil {
		return gc.EvalExpr(n.Callee) + "(" + strings.Join(args, ", ") + ")"
	}
	return "(" + strings.Join(args, ", ") + ")"
}

func (gc *GoIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := gc.EvalExpr(n.Receiver)
	args := gc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		receiverName := n.Func.Receiver

		qualName := receiverName + "." + fname
		allArgs := append([]string{receiver}, args...)
		if result := goBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		if result := goBuiltinMethodFromArgs("*."+fname, allArgs); result != "" {
			return result
		}

		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}

	return receiver + "(" + strings.Join(args, ", ") + ")"
}

func (gc *GoIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	// Alert methods short-circuit to the context's alert emission.
	if receiverName == "Alert" {
		return gc.evalAlertCall(method, n.Args)
	}

	args := gc.evalCallArgs(n.Args)
	if result := goBuiltinMethodFromArgs(qualName, args); result != "" {
		return result
	}
	if result := goBuiltinMethodFromArgs("*."+method, args); result != "" {
		return result
	}

	// User-attached method on a primitive type — emit as a free function call
	// because Go doesn't allow methods on int/float/string/bool/etc. The
	// function lifts to TypeNameMethodName (e.g. `int.double` → `IntDouble`).
	if isPrimitiveTypeName(receiverName) && gc.userMethodKnown(receiverName, method) {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(args, ", ") + ")"
	}

	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// userMethodKnown reports whether a method qualName has a user-defined
// implementation in the package or in any component. Used to decide whether
// to emit a primitive-receiver call as a free function.
func (gc *GoIRContext) userMethodKnown(receiver, method string) bool {
	if gc.Ctx == nil || gc.Ctx.Pkg == nil {
		return false
	}
	for _, f := range gc.Ctx.Pkg.Funcs {
		if f.Receiver == receiver && f.Name == method {
			return true
		}
	}
	for _, comp := range gc.Ctx.Pkg.Components {
		for _, f := range comp.Funcs {
			if f.Receiver == receiver && f.Name == method {
				return true
			}
		}
	}
	return false
}

// isPrimitiveTypeName reports whether a SNGL receiver type name refers to a
// primitive type whose Go representation cannot host methods directly.
func isPrimitiveTypeName(name string) bool {
	switch name {
	case "int", "float", "bool", "string", "list", "option":
		return true
	}
	return false
}

// evalErrorAwareCall emits Go statements for a fallible call whose error
// handler was resolved by effect analysis. Returns nil when the call is not
// specially handled (MVP: only error.raise is recognised) — the caller
// falls back to the normal expression path.
//
// Output per mode, for error.raise(msg, kind):
//   - ErrorPropagateNative: panic(ErrorEvent{...})
//   - ErrorInvokeAndTerminate: create ErrorEvent, inline handler body, return
//   - ErrorBubble: not yet supported (requires fallible-signature lowering)
func (gc *GoIRContext) evalErrorAwareCall(call *ir.Call) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !isGoRaiseFunc(call.Func) {
		return nil
	}
	msg := `""`
	kind := `""`
	if len(call.Args) >= 1 {
		msg = gc.EvalExpr(call.Args[0].Value)
	}
	if len(call.Args) >= 2 {
		kind = gc.EvalExpr(call.Args[1].Value)
	}
	evt := fmt.Sprintf("ErrorEvent{Message: %s, Kind: %s}", msg, kind)

	switch call.ErrorMode {
	case ir.ErrorPropagateNative, ir.ErrorBubble:
		// ErrorBubble would ideally thread the error up a fallible-signature
		// return channel so a caller-provided handler can catch it. MVP has
		// no fallible-signature lowering yet, so we conservatively emit a
		// panic — any enclosing recover-based boundary would catch it, but
		// since we don't emit recover either this effectively aborts. Users
		// should put error.raise directly inside the handler where the
		// boundary/window resolution can inline the handler body.
		return []string{"panic(" + evt + ")"}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler == nil || call.ResolvedHandler.Func == nil {
			return []string{"panic(" + evt + ")"}
		}
		return gc.emitHandlerInvoke(evt, call.ResolvedHandler)
	case ir.ErrorPerCall:
		if call.ErrorHandler == nil || call.ErrorHandler.Func == nil {
			return []string{"_ = " + evt}
		}
		return gc.emitHandlerInvoke(evt, call.ErrorHandler)
	}
	return nil
}

// emitHandlerInvoke produces the block that declares the event variable
// (name from handler param) and inlines the handler body.
//
// The block is wrapped in a Go lexical block `{ ... }` so the variable
// does not leak into the surrounding scope. Note: this does not currently
// emit a `return` / `goto end` to terminate the enclosing handler scope —
// statements following a raise still execute. Users should place
// error.raise at the end of a handler body. Phase 2 will add a labeled
// exit so terminate semantics are honoured.
func (gc *GoIRContext) emitHandlerInvoke(evt string, handler *ir.EventHandler) []string {
	paramName := "e"
	if handler.Func != nil && len(handler.Func.Params) > 0 {
		paramName = handler.Func.Params[0].Name
	}
	lines := []string{
		"{",
		fmt.Sprintf("\t%s := %s", paramName, evt),
		fmt.Sprintf("\t_ = %s", paramName),
	}
	for _, stmt := range handler.Func.Block {
		for _, l := range gc.EvalStmt(stmt) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

// isGoRaiseFunc recognises the stdlib error.raise function as the special
// user-raise primitive. Kept in sync with checker.isRaiseFunc.
func isGoRaiseFunc(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "ErrorRaise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

func (gc *GoIRContext) evalAlertCall(method string, args []ir.CallArg) string {
	if gc.AlertFunc != nil {
		return strings.Join(gc.AlertFunc(gc, method, args), "; ")
	}
	// Default: append to m.toasts
	switch method {
	case "toast":
		msg := gc.EvalExpr(args[0].Value)
		variant := `"info"`
		if len(args) > 1 {
			variant = gc.EvalExpr(args[1].Value)
		}
		return fmt.Sprintf("m.toasts = append(m.toasts, snglToast{%s, %s})", msg, variant)
	case "info", "warn", "error":
		msg := gc.EvalExpr(args[0].Value)
		return fmt.Sprintf("m.toasts = append(m.toasts, snglToast{%s, %q})", msg, method)
	case "confirm":
		return "true"
	}
	return "// unsupported Alert." + method
}

func (gc *GoIRContext) evalConversion(n *ir.Conversion) string {
	if isNullToFuncConv(n) {
		return nullFuncStubGo(n.Type)
	}
	goType := IRTypeToGo(n.Type)
	operand := gc.EvalExpr(n.Operand)
	// Go's string(int) builds a single-rune string; use fmt.Sprint for numeric
	// and general stringification.
	if n.Type != nil && n.Type.Kind == ir.TypeString {
		return "fmt.Sprint(" + operand + ")"
	}
	return goType + "(" + operand + ")"
}

// isNullToFuncConv reports whether conv wraps a null literal with a func
// target type. This is the shape the checker emits for `var f func() T = null`
// and similar null-flowing-into-a-func slots.
func isNullToFuncConv(n *ir.Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != ir.TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

// nullFuncStubGo renders a Go function literal whose body returns the zero
// value of the declared return type. Callable substitute for a null func.
func nullFuncStubGo(t *ir.Type) string {
	if t == nil || t.Sig == nil {
		return "nil"
	}
	sig := t.Sig
	params := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		pt := "any"
		if p != nil && p.Type != nil {
			pt = IRTypeToGo(p.Type)
		}
		params[i] = "_ " + pt
	}
	ret := ""
	body := ""
	if sig.Return != nil && sig.Return.Kind != ir.TypeDyn && sig.Return.Kind != ir.TypeInvalid {
		retGo := IRTypeToGo(sig.Return)
		ret = " " + retGo
		body = " return " + ZeroValueGo(retGo) + " "
	}
	return "func(" + strings.Join(params, ", ") + ")" + ret + " {" + body + "}"
}

func (gc *GoIRContext) evalStructLit(n *ir.StructLit) string {
	name := "struct{}"
	if n.Def != nil {
		name = ExportName(n.Def.Name)
	}
	var parts []string
	for _, f := range n.Fields {
		if f.Spread {
			parts = append(parts, "/* ..."+gc.EvalExpr(f.Value)+" */")
		} else {
			parts = append(parts, ExportName(f.Name)+": "+gc.EvalExpr(f.Value))
		}
	}
	return name + "{" + strings.Join(parts, ", ") + "}"
}

func (gc *GoIRContext) evalStructConstructor(sd *ir.StructDef, args []ir.CallArg) string {
	var parts []string
	for i, f := range sd.Fields {
		val := "nil"
		if i < len(args) {
			val = gc.EvalExpr(args[i].Value)
		}
		parts = append(parts, ExportName(f.Name)+": "+val)
	}
	return ExportName(sd.Name) + "{" + strings.Join(parts, ", ") + "}"
}

func (gc *GoIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "func() any { return nil }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		goType := "any"
		if p.Type != nil {
			goType = IRTypeToGo(p.Type)
		}
		params[i] = p.Name + " " + goType
	}
	retType := "any"
	if n.Func.Return != nil {
		retType = IRTypeToGo(n.Func.Return)
	}

	// For single-expression lambdas
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := gc.EvalExpr(ret.Value)
			return "func(" + strings.Join(params, ", ") + ") " + retType + " { return " + body + " }"
		}
	}

	// Multi-statement lambda
	var b strings.Builder
	b.WriteString("func(" + strings.Join(params, ", ") + ") " + retType + " {\n")
	for _, stmt := range n.Func.Block {
		for _, line := range gc.EvalStmt(stmt) {
			b.WriteString("\t\t" + line + "\n")
		}
	}
	b.WriteString("\t}")
	return b.String()
}

func (gc *GoIRContext) evalCallArgs(args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = gc.EvalExpr(a.Value)
	}
	return out
}

func (gc *GoIRContext) evalMutTarget(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		_, kind := gc.Ctx.Resolve(n.Name)
		if kind == codegen.NameStateVar {
			return "m." + n.Name
		}
		return n.Name
	case *ir.Select:
		operand := gc.evalMutTarget(n.Operand)
		return operand + "." + ExportName(n.Field)
	case *ir.Index:
		operand := gc.evalMutTarget(n.Operand)
		idx := gc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	default:
		return gc.EvalExpr(e)
	}
}

// WithLocal returns a new context with an additional local variable.
func (gc *GoIRContext) WithLocal(name string) *GoIRContext {
	return &GoIRContext{
		Ctx:       gc.Ctx.WithLocal(name),
		AlertFunc: gc.AlertFunc,
	}
}

// ForComponent returns a new context scoped to a component.
func (gc *GoIRContext) ForComponent(comp *ir.Component) *GoIRContext {
	return &GoIRContext{
		Ctx:       gc.Ctx.ForComponent(comp),
		AlertFunc: gc.AlertFunc,
	}
}

// --- IR type → Go type ---

// IRTypeToGo converts an IR type to a Go type string.
func IRTypeToGo(t *ir.Type) string {
	if t == nil {
		return "any"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "bool"
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float64"
	case ir.TypeString, ir.TypeColor,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname,
		ir.TypeDecimal:
		return "string"
	case ir.TypeDate, ir.TypeTime, ir.TypeDateTime:
		return "time.Time"
	case ir.TypeDuration:
		return "time.Duration"
	case ir.TypeList:
		if len(t.Elems) > 0 {
			return "[]" + IRTypeToGo(t.Elems[0])
		}
		return "[]any"
	case ir.TypeRef:
		if len(t.Elems) > 0 {
			return "*" + IRTypeToGo(t.Elems[0])
		}
		return "unsafe.Pointer"
	case ir.TypeOption:
		if len(t.Elems) > 0 {
			return "*" + IRTypeToGo(t.Elems[0])
		}
		return "*any"
	case ir.TypeStruct:
		if sd, ok := t.Decl.(*ir.StructDef); ok {
			if sd.Native != "" {
				return sd.Native
			}
			return ExportName(sd.Name)
		}
		return "any"
	case ir.TypeDyn:
		if meta, ok := t.Meta.(string); ok && meta != "" {
			return meta
		}
		return "any"
	case ir.TypeEnum:
		return "string"
	case ir.TypeUnit:
		if t.Decl != nil {
			name := t.Decl.SymName()
			switch name {
			case "duration":
				return "time.Duration"
			}
			// Other units map to string (e.g., "12px")
			return "string"
		}
		return "string"
	case ir.TypeFunc:
		if t.Sig != nil {
			return irFuncSigToGo(t.Sig)
		}
		return "func()"
	case ir.TypeNull:
		return "any"
	default:
		return "any"
	}
}

// IRLiteralToGo converts an IR literal expression to a Go literal.
func IRLiteralToGo(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	switch n := e.(type) {
	case *ir.Conversion:
		// null → func: emit a zero-value callable lambda so calling through
		// the var at runtime returns the declared return type's zero instead
		// of panicking on a nil func value.
		if isNullToFuncConv(n) {
			return nullFuncStubGo(n.Type)
		}
		return IRLiteralToGo(n.Operand)
	case *ir.Literal:
		if n.Type == nil {
			return n.Raw
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "nil"
		case ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		default:
			return n.Raw
		}
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = IRLiteralToGo(el)
		}
		elemType := "any"
		if n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) > 0 {
			elemType = IRTypeToGo(n.Type.Elems[0])
		}
		return "[]" + elemType + "{" + strings.Join(parts, ", ") + "}"
	case *ir.StructLit:
		name := "struct{}"
		if n.Def != nil {
			name = IRTypeToGo(n.Type)
			if name == "any" || name == "" {
				name = ExportName(n.Def.Name)
			}
		}
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, ExportName(f.Name)+": "+IRLiteralToGo(f.Value))
		}
		return name + "{" + strings.Join(parts, ", ") + "}"
	case *ir.Lambda:
		return irLambdaLiteralToGo(n)
	}
	return `""`
}

// irLambdaLiteralToGo emits a Go function literal for a synthetic zero-value
// lambda (body is always a single Return of another IR literal).
func irLambdaLiteralToGo(n *ir.Lambda) string {
	if n.Func == nil {
		return "nil"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		goType := "any"
		if p != nil && p.Type != nil {
			goType = IRTypeToGo(p.Type)
		}
		params[i] = "_ " + goType
	}
	retType := ""
	if n.Func.Return != nil && n.Func.Return.Kind != ir.TypeDyn && n.Func.Return.Kind != ir.TypeInvalid {
		retType = " " + IRTypeToGo(n.Func.Return)
	}
	body := ""
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body = " return " + IRLiteralToGo(ret.Value) + " "
		}
	}
	return "func(" + strings.Join(params, ", ") + ")" + retType + " {" + body + "}"
}

func irFuncSigToGo(sig *ir.FuncSig) string {
	params := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		params[i] = IRTypeToGo(p.Type)
	}
	ret := ""
	if sig.Return != nil {
		ret = " " + IRTypeToGo(sig.Return)
	}
	return "func(" + strings.Join(params, ", ") + ")" + ret
}

// --- IR operator helpers ---

func irBinaryOp(op ast.BinaryOp) string {
	return binaryOpStr(op)
}

func irAssignOp(op ast.AssignOp) string {
	return assignOpStr(op)
}
