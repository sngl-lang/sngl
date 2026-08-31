package kotlin

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
	"git.duckfam.us/jonathan/sngl/codegen/irwalk"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ktImportSet is the shared import collector for a KtIRContext tree.
// Shared by pointer across all derived contexts (WithLocal, ForComponent)
// so any RequireImport call anywhere in the tree is visible to the root.
type ktImportSet struct {
	paths map[string]struct{}
	order []string
}

func newKtImportSet() *ktImportSet { return &ktImportSet{paths: map[string]struct{}{}} }

func (s *ktImportSet) require(path string) {
	if path == "" {
		return
	}
	if _, seen := s.paths[path]; seen {
		return
	}
	s.paths[path] = struct{}{}
	s.order = append(s.order, path)
}

// KtIRContext translates IR expressions and statements into Kotlin code.
type KtIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string // what the handler event parameter maps to in this scope
	// IdentRewrites remaps bare identifiers regardless of scope kind.
	// Used by the Android test-mode emit to route every component-level
	// var through a hoisted state object (`count` → `state.count`).
	IdentRewrites map[string]string
	imports       *ktImportSet
}

// NewIRContext creates a KtIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *KtIRContext {
	return &KtIRContext{Ctx: ctx, imports: newKtImportSet()}
}

// RequireImport records that the emitted Kotlin file needs the given import.
// Safe to call repeatedly; insertion order is preserved, duplicates ignored.
// Called from emit sites when a native package reference is rendered.
// Platforms read the result after translation via Imports().
func (kc *KtIRContext) RequireImport(path string) {
	if kc.imports != nil {
		kc.imports.require(path)
	}
}

// Imports returns recorded import paths in insertion order.
func (kc *KtIRContext) Imports() []string {
	if kc.imports == nil {
		return nil
	}
	out := make([]string, len(kc.imports.order))
	copy(out, kc.imports.order)
	return out
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
	// The Kotlin i18n runtime keys plural forms by string category exclusively.
	if s := snglI18n.PluralKeyConstString(n); s != "" {
		return s
	}
	field := n.Field
	if field == "length" {
		if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeList {
			field = "size"
		}
	}
	// Field access on a `dyn` operand: Kotlin's `Any` has no user fields.
	// If exactly one package struct declares this field, emit a safe cast.
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeDyn {
		if name := kc.uniqueStructWithField(n.Field); name != "" {
			return "(" + operand + " as " + name + ")." + field
		}
	}
	return operand + "." + field
}

// uniqueStructWithField returns the Kotlin type name of the sole package
// struct declaring a field with this SNGL field name, or "" if zero or
// multiple structs match. Mirrors the Go IRContext equivalent.
func (kc *KtIRContext) uniqueStructWithField(field string) string {
	if kc.Ctx == nil || kc.Ctx.Pkg == nil {
		return ""
	}
	var match *ir.StructDef
	for _, sd := range kc.Ctx.Pkg.Structs {
		for _, f := range sd.Fields {
			if f.Name == field {
				if match != nil {
					return ""
				}
				match = sd
				break
			}
		}
	}
	if match == nil {
		return ""
	}
	return exportName(match.Name)
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
			panic("kotlin: struct spread must be lowered by flatten_struct_spread")
		}
		parts[i] = f.Name + " = " + fieldStrs[i]
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

// valueCopy binds a struct value the way SNGL binds one: by copy.
//
// A SNGL struct is a value, so `var next = this` gives you your own; Go's
// assignment already does that and Kotlin's does not -- `next` is the same
// object, and mutating it writes through to whatever else holds it. On
// Compose that is fatal rather than merely wrong: the state a handler
// reassigns is the object it just mutated, structural equality says nothing
// changed, and the screen never recomposes. Every button animated and none of
// them did anything.
//
// A literal needs no copy: it is already nobody else's. Neither does an
// assignment -- copying on binding is what makes every mutable name one this
// scope owns, and copying again on the way out would only defeat the
// structural-equality check Compose uses to decide whether to recompose.
func valueCopy(init ir.Expr, t *ir.Type, rendered string) string {
	if t == nil || t.Kind != ir.TypeStruct || t.Decl == nil {
		return rendered
	}
	if ir.StringReprStruct(t) {
		return rendered
	}
	switch init.(type) {
	case *ir.StructLit, *ir.Literal, nil:
		return rendered
	}
	return rendered + ".copy()"
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
		// A SNGL list local is mutable -- `out.push(x)` is ordinary -- and
		// Kotlin's listOf() is neither mutable nor, when empty, typed. Both
		// bit at once: `var out = listOf()` could not infer its element and
		// had no add().
		if n.Type != nil && n.Type.Kind == ir.TypeList {
			if ll, ok := n.Init.(*ir.ListLit); ok {
				elem := "Any"
				if len(n.Type.Elems) > 0 {
					elem = IRTypeToKt(n.Type.Elems[0])
				}
				if len(ll.Elems) == 0 {
					return "var " + n.Name + " = mutableListOf<" + elem + ">()"
				}
				if rest, cut := strings.CutPrefix(initStr, "listOf("); cut {
					return "var " + n.Name + " = mutableListOf<" + elem + ">(" + rest
				}
			}
		}
		return "var " + n.Name + " = " + valueCopy(n.Init, n.Type, initStr)
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
	switch n.IterKind {
	case ir.IterMapEntries:
		// Map iteration: for ((k, v) in m) { ... }
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for ((%s, %s) in %s) {", n.Key, valueVar, iter)
	case ir.IterIndexed:
		// Two-var list/iter: Key is the index, Value the element.
		// withIndex() yields (index, element), matching that order.
		return fmt.Sprintf("for ((%s, %s) in %s.withIndex()) {", n.Key, n.Value, iter)
	default:
		// Single-var list / iter<T>: for (x in list) { ... }
		return fmt.Sprintf("for (%s in %s) {", n.Key, iter)
	}
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
func (kc *KtIRContext) MutTargetField(n *ir.Select) string { return n.Field }

func (kc *KtIRContext) StmtPrefix(_ ir.Stmt) []string { return nil }

func (kc *KtIRContext) Scoped(name string) irwalk.Renderer { return kc.WithLocal(name) }

func (kc *KtIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Value)
	case ir.TypeInt:
		return n.Value
	case ir.TypeFloat:
		s := n.Value
		// An exponent already makes it a Double in Kotlin, and `1e+09.0` is
		// not a literal at all -- it is where `const ROUNDABLE = 1000000000.0`
		// stopped compiling.
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case ir.TypeBool:
		return n.Value
	case ir.TypeNull:
		return "null"
	case ir.TypeStruct:
		if ir.StringReprStruct(n.Type) {
			return fmt.Sprintf("%q", n.Value)
		}
		return n.Value
	default:
		return n.Value
	}
}

func (kc *KtIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		// Enum member: emit qualified Kotlin enum value (Gender.female)
		// so the value matches the declared enum type at the use site.
		if n.Type != nil && n.Type.Kind == ir.TypeEnum && n.Type.Decl != nil {
			return exportName(n.Type.Decl.SymName()) + "." + EnumEntry(n.Member)
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
	// Intrinsic dispatch by ID — never by method name. Backends register only
	// the intrinsics they emit; unregistered IDs fall through.
	if out, imports, ok := codegen.EmitIntrinsicCall(langKt, n, kc.EvalExpr); ok {
		for _, p := range imports {
			kc.RequireImport(p)
		}
		return out
	}
	if n.Receiver != nil {
		return kc.evalNamespaceCall(n)
	}
	if n.Func != nil && n.Func.Receiver != "" {
		return kc.evalTypeMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := kc.evalCallArgs(n.Args)
		// Primitive casts string/int/float are materialized as ir.Conversion
		// by the checker and handled in evalConversion, so they never arrive
		// here as a named call.
		// IdentRewrites can redirect a bare func call to a method
		// on a hoisted state object (e.g. `label()` →
		// `state.label()` in Android test mode).
		if kc.IdentRewrites != nil {
			if rewritten, ok := kc.IdentRewrites[fname]; ok {
				return rewritten + "(" + strings.Join(args, ", ") + ")"
			}
		}
		codegen.RequireIntrinsicFallback(langKt, n.Func)
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
		// A package function called through its import has no receiver on the
		// declaration — the namespace is the alias at the call site. Name it
		// from there so a qualified call reads the same either way.
		receiverName := n.Func.Receiver
		if receiverName == "" {
			if id, ok := n.Receiver.(*ir.Ident); ok {
				receiverName = id.Name
			}
		}
		qualName := receiverName + "." + fname

		// Intrinsic dispatch: stdlib intrinsics that map to per-locale
		// runtime entry points. After NoContext + InlinePure, i18n.*
		// wrapper calls have been lowered to direct intl.* intrinsic
		// calls with the locale threaded as the first arg.

		// For i18n.* calls the namespace receiver is the module object, not a
		// value argument. Pass only the real call args to the builtin dispatcher
		// so that a(0) is the first semantic argument (matches type-method path).
		if receiverName == "i18n" {
			if result := kotlinBuiltinMethodFromArgs(qualName, args); result != "" {
				kc.RequireImport(SnglI18nKotlinPackage + ".I18n")
				return result
			}
		}

		allArgs := append([]string{receiver}, args...)
		if result := kotlinBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		// Only for a receiver whose type the checker could not resolve. With an id
		// in hand the registry is the answer, and its absence has to reach
		// RequireIntrinsicFallback rather than be hidden by a name match.
		if n.Func == nil || n.Func.Intrinsic == "" {
			if result := kotlinBuiltinMethodFromArgs("*."+fname, allArgs); result != "" {
				return result
			}
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
			// Nothing above recognised it, so this is the generic emission
			// and the same guard the other paths carry applies.
			codegen.RequireIntrinsicFallback(langKt, n.Func)
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
	if !ir.IsErrorRaiseFunc(call.Func) {
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

func (kc *KtIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	// Component-self method reference (the receiver names a component in this
	// package) — e.g. a computed or 0-arg func referenced from the view. The
	// Compose backend emits a computed as a `val <name> by remember {
	// derivedStateOf {...} }` property (read by bare name) and a plain func as
	// `<name>()`. Resolve to the bare name here rather than falling through to
	// the unresolved-method marker below. Routes through IdentRewrites so
	// test-mode `state.` prefixing still applies.
	if kc.receiverIsComponent(receiverName) {
		name := method
		if kc.IdentRewrites != nil {
			if rw, ok := kc.IdentRewrites[name]; ok {
				name = rw
			}
		}
		if codegen.IsComputed(n.Func) {
			return name
		}
		callArgs := kc.evalCallArgs(n.Args)
		return name + "(" + strings.Join(callArgs, ", ") + ")"
	}

	args := kc.evalCallArgs(n.Args)
	if result := kotlinBuiltinMethodFromArgs(qualName, args); result != "" {
		return result
	}
	// Only for a receiver whose type the checker could not resolve. With an id
	// in hand the registry is the answer, and its absence has to reach
	// RequireIntrinsicFallback rather than be hidden by a name match.
	if n.Func == nil || n.Func.Intrinsic == "" {
		if result := kotlinBuiltinMethodFromArgs("*."+method, args); result != "" {
			return result
		}
	}

	codegen.RequireIntrinsicFallback(langKt, n.Func)
	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// receiverIsComponent reports whether name matches a component declared in the
// package — i.e. the call is a component-self method (computed/func), not a
// real type method on some value.
func (kc *KtIRContext) receiverIsComponent(name string) bool {
	if kc.Ctx == nil || kc.Ctx.Pkg == nil {
		return false
	}
	for _, comp := range kc.Ctx.Pkg.Components {
		if comp.Name == name {
			return true
		}
	}
	return false
}

// ktIntConvMethod returns the Kotlin conversion method for an integer target
// width (e.g. .toByte(), .toULong()). Plain int uses .toInt().
func ktIntConvMethod(t *ir.Type) string {
	switch {
	case t.Bits == 8 && t.Unsigned:
		return "toUByte()"
	case t.Bits == 8:
		return "toByte()"
	case t.Bits == 16 && t.Unsigned:
		return "toUShort()"
	case t.Bits == 16:
		return "toShort()"
	case t.Bits == 32 && t.Unsigned:
		return "toUInt()"
	case t.Bits == 32:
		return "toInt()"
	case t.Bits == 64 && t.Unsigned:
		return "toULong()"
	case t.Bits == 64:
		return "toLong()"
	}
	return "toInt()"
}

func (kc *KtIRContext) evalConversion(n *ir.Conversion) string {
	if ir.IsNullToFuncConv(n) {
		return nullFuncStubKt(n.Type)
	}
	operand := kc.EvalExpr(n.Operand)
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt:
			return operand + "." + ktIntConvMethod(n.Type)
		case ir.TypeFloat:
			if n.Type.Bits == 32 {
				return operand + ".toFloat()"
			}
			return operand + ".toDouble()"
		case ir.TypeString:
			// Kotlin's Double.toString always writes a fraction, so a
			// calculator that Go and JS both spell `24` came out as `24.0`.
			// string(float) has to mean the same thing on every target.
			if src := n.Operand.ExprType(); src != nil && src.Kind == ir.TypeFloat {
				return FloatStringFn + "(" + operand + ")"
			}
			return operand + ".toString()"
		case ir.TypeBool:
			return operand + " as Boolean"
		}
	}
	return operand
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

// KtZeroFor returns the Kotlin zero/default-value expression for an IR type.
// Exported for platform emitters that need a concrete initializer (e.g. data
// class field defaults whose source value isn't carried on the IR placeholder).
func KtZeroFor(t *ir.Type) string { return ktZeroFor(t) }

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
	case ir.TypeEnum:
		// An enum's zero is its first member, as it is on every other target.
		// `null` is not a value of a non-nullable Kotlin enum, so a struct
		// field left at its default did not compile.
		if ed, ok := t.Decl.(*ir.EnumDef); ok && len(ed.Members) > 0 {
			return exportName(ed.Name) + "." + EnumEntry(ed.Members[0].Name)
		}
	}
	return "null"
}

// EnumEntry is the Kotlin spelling of one SNGL enum member. Upper-cased
// because Kotlin entries are, and because a SNGL member may be named for
// something Any already declares -- `Action.equals` reads as the method
// otherwise.
//
// Declaration and reference both go through here. They used to spell it
// separately, so every `Op.none` in the output named an entry declared as
// `NONE`.
func EnumEntry(member string) string { return strings.ToUpper(member) }

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
		imports:       kc.imports,
	}
}

// WithIdentRewrite returns a context in which one name renders as another.
// Used for a method's receiver: an extension function's receiver is `this`
// whatever the declaration named it, so a body reading `o.symbol` has to read
// `this.symbol`.
func (kc *KtIRContext) WithIdentRewrite(from, to string) *KtIRContext {
	rewrites := make(map[string]string, len(kc.IdentRewrites)+1)
	maps.Copy(rewrites, kc.IdentRewrites)
	rewrites[from] = to
	return &KtIRContext{
		Ctx:           kc.Ctx,
		EventVar:      kc.EventVar,
		IdentRewrites: rewrites,
		imports:       kc.imports,
	}
}

// ForComponent returns a new context scoped to a component.
func (kc *KtIRContext) ForComponent(comp *ir.Component) *KtIRContext {
	return &KtIRContext{
		Ctx:           kc.Ctx.ForComponent(comp),
		EventVar:      kc.EventVar,
		IdentRewrites: kc.IdentRewrites,
		imports:       kc.imports,
	}
}

// --- IR type → Kotlin type ---

// IRTypeToKt converts an IR type to a Kotlin type string.
// ktIntType maps a (possibly sized) integer type to its Kotlin type name.
// Unsigned widths use Kotlin's U-prefixed types; plain int stays Int.
func ktIntType(t *ir.Type) string {
	switch {
	case t.Bits == 8 && t.Unsigned:
		return "UByte"
	case t.Bits == 8:
		return "Byte"
	case t.Bits == 16 && t.Unsigned:
		return "UShort"
	case t.Bits == 16:
		return "Short"
	case t.Bits == 32 && t.Unsigned:
		return "UInt"
	case t.Bits == 32:
		return "Int"
	case t.Bits == 64 && t.Unsigned:
		return "ULong"
	case t.Bits == 64:
		return "Long"
	}
	return "Int"
}

func IRTypeToKt(t *ir.Type) string {
	if t == nil {
		return "Any"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "Boolean"
	case ir.TypeInt:
		return ktIntType(t)
	case ir.TypeFloat:
		if t.Bits == 32 {
			return "Float"
		}
		return "Double"
	case ir.TypeString:
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
		// color/date/time/datetime are string-representable stdlib structs;
		// the Kotlin runtime carries them as String (date/time/datetime are
		// quoted ISO strings, matching their literal emission).
		if ir.StringReprStruct(t) {
			return "String"
		}
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
	case ir.TypeNull, ir.TypeDyn:
		// dyn holds anything including null (recursive structs like TreeNode
		// use `dyn = null` for absent children).
		return "Any?"
	case ir.TypeVoid, ir.TypeIter, ir.TypeComponent, ir.TypeTypeParam,
		ir.TypeRef, ir.TypeNative, ir.TypeInvalid:
		// No first-class Kotlin spelling in emitted code. "Any" is what the
		// former default arm produced for each of these, so listing them
		// changes nothing today — it only lets the arm below catch a kind
		// nobody has considered, which "Any" would otherwise have absorbed
		// into plausible-looking output.
		return "Any"
	default:
		panic(fmt.Sprintf("IRTypeToKt: unhandled ir.TypeKind %v", t.Kind))
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
			return n.Value
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Value)
		case ir.TypeInt:
			return n.Value
		case ir.TypeFloat:
			s := n.Value
			if !strings.ContainsAny(s, ".eE") {
				s += ".0"
			}
			return s
		case ir.TypeBool:
			return n.Value
		case ir.TypeNull:
			return "null"
		case ir.TypeStruct:
			if ir.StringReprStruct(n.Type) {
				return fmt.Sprintf("%q", n.Value)
			}
			return n.Value
		default:
			return n.Value
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
	case *ir.Ident:
		// An enum member is a literal value, and the only one that arrives as
		// an identifier. Falling through to the empty string put `op = ""` in
		// a constructor call whose parameter is an enum.
		if n.Member != "" {
			if n.Type != nil && n.Type.Kind == ir.TypeEnum && n.Type.Decl != nil {
				return exportName(n.Type.Decl.SymName()) + "." + EnumEntry(n.Member)
			}
			return fmt.Sprintf("%q", n.Member)
		}
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
	// Alert.* → Android Toast. The android backend gates the `Toast` import
	// and the `val context = LocalContext.current` declaration on
	// CommonAnalysis.NeedsToast, so `context` is in scope at the call site
	// (toast calls live inside @Composable handler lambdas that capture it).
	case "*.min":
		return "minOf(" + a(0) + ", " + a(1) + ")"
	case "*.max":
		return "maxOf(" + a(0) + ", " + a(1) + ")"
	case "*.abs":
		return "kotlin.math.abs(" + a(0) + ")"
	// string.* and float math are intrinsic-backed and emitted by ID via the
	// registry (intrinsics.go). string.contains stays: it is composed
	// (indexOf >= 0), not an intrinsic.
	case "string.contains":
		return a(0) + ".contains(" + a(1) + ")"
	case "*.join":
		return a(0) + ".joinToString(" + a(1) + ")"
	case "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "*.map":
		return a(0) + ".map(" + a(1) + ")"
	case "*.reverse":
		return a(0) + ".reversed()"
	// map
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
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "I18n.defaultLocale()"
	}
	return ""
}

// ktMapValZero returns the Kotlin zero value for the value type of a map IR type.
func ktMapValZero(t *ir.Type) string {
	if t == nil || len(t.Elems) < 2 {
		return "null"
	}
	return ktZeroFor(t.Elems[1])
}

// FloatStringFn names the helper `string(<float>)` lowers to, and
// FloatStringDecl is its declaration. Kotlin's own Double.toString always
// writes a fraction; Go's fmt.Sprint and JavaScript's String both drop it for
// a whole number, and SNGL follows them.
const FloatStringFn = "_snglFloatStr"

const FloatStringDecl = `fun ` + FloatStringFn + `(v: Double): String =
    if (v.isFinite() && v == kotlin.math.floor(v) && kotlin.math.abs(v) < 9.007199254740992E15) v.toLong().toString() else v.toString()
`
