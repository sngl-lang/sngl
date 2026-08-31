package javascript

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
	"git.duckfam.us/jonathan/sngl/codegen/irwalk"
	jsscheme "git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	"git.duckfam.us/jonathan/sngl/ir"
)

// JsIRContext translates IR expressions and statements into JavaScript code.
type JsIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string
	// EventParam is the handler parameter EventVar stands for; see
	// codegen.ExprCtx.
	EventParam ir.Symbol

	// EmitPositionMarkers prepends inline `/*@SNGL:file:line@*/` markers at
	// statement boundaries, which renderJSSourceMap later scans and strips.
	EmitPositionMarkers bool
}

func NewIRContext(ctx *codegen.ExprCtx) *JsIRContext {
	jc := &JsIRContext{Ctx: ctx}
	if ctx != nil {
		jc.EmitPositionMarkers = ctx.Maps
	}
	return jc
}

func (jc *JsIRContext) EvalExpr(e ir.Expr) string { return irwalk.EvalExpr(jc, e) }

func (jc *JsIRContext) EvalStmt(s ir.Stmt) []string { return irwalk.EvalStmt(jc, s) }

func (jc *JsIRContext) NilExpr() string              { return "null" }
func (jc *JsIRContext) Literal(n *ir.Literal) string { return jc.evalLiteral(n) }
func (jc *JsIRContext) Ident(n *ir.Ident) string     { return jc.evalIdent(n) }

func (jc *JsIRContext) Binary(n *ir.Binary, left, right string) string {
	if n.Op == ast.BinDiv && isIntIR(n.Left) && isIntIR(n.Right) {
		// BigInt division truncates toward zero natively; Number division needs
		// an explicit Math.trunc to match integer semantics.
		if jsIsBigInt(n.Type) {
			return jsWrapArith("("+left+" / "+right+")", n.Type)
		}
		return jsWrapArith("Math.trunc("+left+" / "+right+")", n.Type)
	}
	return jsWrapArith("("+left+" "+binaryOpStr(n.Op)+" "+right+")", n.Type)
}
func (jc *JsIRContext) Unary(n *ir.Unary, operand string) string {
	if n.Op == ast.UnaryNot {
		return "!" + operand
	}
	if n.Type != nil && n.Type.IsSized() {
		return jsWrapArith("(-"+operand+")", n.Type)
	}
	return "-" + operand
}
func (jc *JsIRContext) Ternary(_ *ir.Ternary, cond, then_, else_ string) string {
	return "(" + cond + " ? " + then_ + " : " + else_ + ")"
}
func (jc *JsIRContext) Select(n *ir.Select, operand string) string {
	if s := snglI18n.PluralKeyConstString(n); s != "" {
		return s
	}
	// Registered so the platform emits the `import * as` prelude.
	if ident, ok := n.Operand.(*ir.Ident); ok {
		if _, ok := ident.Sym.(*ir.Namespace); ok {
			if jsAlias, importPath := nativeBundledNamespaceAliasCtx(jc.Ctx, ident.Name); jsAlias != "" {
				jc.registerNativeImport(importPath, n.Field)
				return jsAlias + "." + n.Field
			}
		}
	}
	// A MethodFields field is a zero-arg method, so `c.greeting` must invoke
	// it rather than compare the function object.
	if jc.Ctx != nil && jc.Ctx.MethodFields != nil && jc.Ctx.MethodFields[n.Field] {
		return operand + "." + n.Field + "()"
	}
	return operand + "." + jc.fieldKey(n)
}

// fieldKey is the property a Select reaches, for reads and for assignment
// targets alike: a write spelled the SNGL way would not fail, it would create
// a second, lowercase property alongside the one the module declared.
func (jc *JsIRContext) fieldKey(n *ir.Select) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeStruct {
		sd, _ := t.Decl.(*ir.StructDef)
		return jsFieldKey(sd, n.Field)
	}
	return n.Field
}
func (jc *JsIRContext) Index(n *ir.Index, operand, idx string) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
		return operand + ".get(" + idx + ")"
	}
	return operand + "[" + idx + "]"
}
func (jc *JsIRContext) ListLit(_ *ir.ListLit, elems []string) string {
	return "[" + strings.Join(elems, ", ") + "]"
}
func (jc *JsIRContext) MapLit(n *ir.MapLitIR, keys, vals []string) string {
	if isPluralKeyMapType(n.Type) {
		var b strings.Builder
		b.WriteString("{")
		for i := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[")
			b.WriteString(keys[i])
			b.WriteString("]: ")
			b.WriteString(vals[i])
		}
		b.WriteString("}")
		return b.String()
	}
	var b strings.Builder
	b.WriteString("new Map([")
	for i := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("[")
		b.WriteString(keys[i])
		b.WriteString(", ")
		b.WriteString(vals[i])
		b.WriteString("]")
	}
	b.WriteString("])")
	return b.String()
}
func (jc *JsIRContext) StructLit(n *ir.StructLit, fieldStrs []string) string {
	parts := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		if f.Spread {
			panic("javascript: struct spread must be lowered by flatten_struct_spread")
		}
		parts[i] = jsFieldKey(n.Def, f.Name) + ": " + fieldStrs[i]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// jsFieldKey is the property name a field is written under in JavaScript. For
// a struct read out of a JavaScript module that is the name the module
// declared, since the importer lowered its leading capital to reach the SNGL
// field name.
//
// Only that scheme's declarations: a go: struct's native names are Go's, and
// nothing in a generated page reads them.
func jsFieldKey(sd *ir.StructDef, name string) string {
	if sd == nil || !jsscheme.DeclaredHere(sd.Foreign) {
		return name
	}
	for _, f := range sd.Fields {
		if f.Name == name && f.Foreign.Name != "" {
			return f.Foreign.Name
		}
	}
	return name
}
func (jc *JsIRContext) Spread(_ *ir.Spread, operand string) string { return "..." + operand }

func (jc *JsIRContext) Call(n *ir.Call) string             { return jc.evalCall(n) }
func (jc *JsIRContext) Conversion(n *ir.Conversion) string { return jc.evalConversion(n) }
func (jc *JsIRContext) Lambda(n *ir.Lambda) string         { return jc.evalLambda(n) }

func (jc *JsIRContext) AssignText(n *ir.Assign, target, value string) string {
	return target + " " + assignOpStr(n.Op) + " " + value
}
func (jc *JsIRContext) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target
}
func (jc *JsIRContext) CallStmtLines(n *ir.CallStmt) []string {
	if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
		if lines := jc.evalErrorAwareCall(n.Call); lines != nil {
			return lines
		}
	}
	return []string{jc.EvalExpr(n.Call)}
}
func (jc *JsIRContext) EmitText(n *ir.Emit, argStrs []string) string {
	return "emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"
}
func (jc *JsIRContext) LocalVarText(n *ir.LocalVar, initStr string) string {
	if n.Init != nil {
		return "let " + n.Name + " = " + jsValueCopy(n.Init, n.Type, initStr)
	}
	return "let " + n.Name
}

// jsValueCopy binds a struct value the way SNGL binds one: by copy. A SNGL
// struct is a value, so `var next = this` gives you your own -- Go's
// assignment does that and JavaScript's does not, so mutating `next` wrote
// through to whatever else held the object.
//
// It has not shown as a wrong pixel here, because the html updaters run after
// every handler whether anything changed or not. It is still the wrong
// semantics, and a program comparing a value it kept against the current one
// sees them both move. The same lowering on Compose, whose recomposition is
// decided by equality, meant no button did anything.
//
// A literal needs no copy: it is already nobody else's.
func jsValueCopy(init ir.Expr, t *ir.Type, rendered string) string {
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
	return "{ ..." + rendered + " }"
}
func (jc *JsIRContext) ReturnText(n *ir.Return, valueStr string) string {
	if n.Value != nil {
		return "return " + valueStr
	}
	return "return"
}

func (jc *JsIRContext) ForHead(n *ir.For, iter string) string {
	switch n.IterKind {
	case ir.IterMapEntries:
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for (const [%s, %s] of %s.entries()) {", n.Key, valueVar, iter)
	case ir.IterIndexed:
		return fmt.Sprintf("for (const [%s, %s] of %s.entries()) {", n.Key, n.Value, iter)
	default:
		return fmt.Sprintf("for (const %s of %s) {", n.Key, iter)
	}
}
func (jc *JsIRContext) IfHead(_ *ir.If, cond string) string { return "if (" + cond + ") {" }
func (jc *JsIRContext) ElseHead() string                    { return "} else {" }
func (jc *JsIRContext) BlockEnd() string                    { return "}" }
func (jc *JsIRContext) Indent() string                      { return "\t" }

func (jc *JsIRContext) MutTargetIdent(n *ir.Ident) string {
	// A synthesized ref is a plain module-scoped local, not a state field, as
	// evalIdent's read path also treats it. Emitting `state.__slotN = []` never
	// clears the real accumulator, and the slot's removeChild loop then throws
	// on the second update.
	if n.Synthesized {
		return n.Name
	}
	_, kind := jc.Ctx.Resolve(n.Name)
	if kind == codegen.NameStateVar {
		return "state." + n.Name
	}
	if kind == codegen.NameLocal {
		return jc.Ctx.RenamedName(n.Name)
	}
	return n.Name
}
func (jc *JsIRContext) MutTargetField(n *ir.Select) string { return jc.fieldKey(n) }

func (jc *JsIRContext) StmtPrefix(s ir.Stmt) []string {
	if !jc.EmitPositionMarkers {
		return nil
	}
	pos := ir.StmtPos(s)
	if !pos.IsValid() || pos.File == "" {
		return nil
	}
	return []string{fmt.Sprintf("/*@SNGL:%s:%d@*/", pos.File, pos.Line)}
}

func (jc *JsIRContext) Scoped(name string) irwalk.Renderer { return jc.WithLocal(name) }

// evalErrorAwareCall emits JS statements for a fallible call whose error
// handler effect analysis resolved. Only error.raise is recognised; anything
// else returns nil and falls back to the normal call path.
func (jc *JsIRContext) evalErrorAwareCall(call *ir.Call) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !ir.IsErrorRaiseFunc(call.Func) {
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
		// ErrorBubble has no fallible-signature lowering; throw so the
		// enclosing scope surfaces the error natively.
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

// emitHandlerInvoke inlines the handler body in a JS block scope. It emits no
// terminate, as in the Go translator.
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

// evalLiteral defers to the package-level translateIRLiteral so the IRContext
// and lang-translator paths stay identical.
func (jc *JsIRContext) evalLiteral(n *ir.Literal) string {
	return translateIRLiteral(n)
}

func (jc *JsIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		if src, ok := nativeEnumMemberJS(n.Type, n.Member); ok {
			return src
		}
		return fmt.Sprintf("%q", n.Member)
	}
	// A synthesized ref is a bare identifier: JS has no Model receiver.
	if n.Synthesized {
		return n.Name
	}
	// IsElementRef deliberately does NOT become a querySelector here: in the
	// htmlTranslator pipeline those idents are bound to local JS vars and must
	// stay bare, and the IR does not reliably mark them Synthesized, so a
	// blanket branch wraps them in a querySelector against a not-yet-attached
	// node.
	//
	// passNoImplicitRecv's synthesized receiver renders as `state`.
	if _, ok := n.Sym.(*ir.Component); ok {
		return "state"
	}
	name := n.Name
	if jc.EventVar != "" && jc.EventParam != nil && n.Sym == jc.EventParam {
		return jc.EventVar
	}
	sym, kind := jc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return jc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return "$" + name + "()"
	case codegen.NameStateVar:
		return "state." + name
	case codegen.NameConst:
		// A component-scoped const is a per-instance state field, since it may
		// need runtime construction; a package-level const stays a bare
		// top-level var.
		if jc.Ctx.Component != nil {
			for _, v := range jc.Ctx.Component.Vars {
				if v == sym {
					return "state." + name
				}
			}
		}
		return name
	default:
		if n.Type != nil && n.Type.Kind == ir.TypeEnum {
			if src, ok := nativeEnumMemberJS(n.Type, name); ok {
				return src
			}
			return fmt.Sprintf("%q", name)
		}
		return name
	}
}

// nativeEnumMemberJS returns the JavaScript a member of t erases to in the
// module that declared it, for an enum a scheme importer built.
//
// A SNGL enum member is its own name in generated JS; a TypeScript member is
// the value its declaration gives it, and that value is what the module's own
// code compares against. Which of the two applies is the scheme's answer, read
// off Origin the way jsFieldKey reads it for a field.
func nativeEnumMemberJS(t *ir.Type, member string) (string, bool) {
	if t == nil || t.Kind != ir.TypeEnum {
		return "", false
	}
	ed, _ := t.Decl.(*ir.EnumDef)
	if ed == nil || !jsscheme.DeclaredHere(ed.Foreign) {
		return "", false
	}
	for _, m := range ed.Members {
		if m.Name != member {
			continue
		}
		lit, _ := m.Value.(*ir.Literal)
		if lit == nil || lit.Value == "" || lit.Type == nil {
			return "", false
		}
		if lit.Type.Kind == ir.TypeString {
			return fmt.Sprintf("%q", lit.Value), true
		}
		if _, err := strconv.ParseFloat(lit.Value, 64); err != nil {
			return "", false
		}
		return lit.Value, true
	}
	return "", false
}

func (jc *JsIRContext) evalCall(n *ir.Call) string {
	// Intrinsic dispatch by ID — never by method name — and uniformly whether
	// the call reached codegen as a type-method call or was inlined to a direct
	// intrinsic call. Must precede every other branch so e.g. an inlined
	// `stdlib.StrUpper(s)` is emitted as `s.toUpperCase()`, not a bare call.
	if out, _, ok := codegen.EmitIntrinsicCall(langJS, jc.Ctx.Platform, n, jc.EvalExpr); ok {
		return out
	}
	// Native scheme-import call (e.g. js:): emit through the bundler
	// alias when the module is in BundledNativePkgs, recording the
	// module → name binding for top-level `import * as` emission. Only for a
	// declaration JavaScript has: a #[foreign] mark naming another language
	// would otherwise rewrite the call to a name nothing here declares.
	if n.Func != nil && jsscheme.CallsHere(n.Func.Foreign) {
		return jc.evalNativeCall(n)
	}
	if n.Receiver != nil {
		return jc.evalNamespaceCall(n)
	}
	if n.Func != nil && n.Func.Receiver != "" {
		return jc.evalTypeMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := jc.evalCallArgs(n.Args)
		// regex(x) is a genuine builtin (RegExp constructor). The primitive
		// casts string/int/float are materialized as ir.Conversion by the
		// checker and handled in evalConversion, so they never arrive here.
		if fname == "regex" && len(args) == 1 {
			return "new RegExp(" + args[0] + ")"
		}
		codegen.RequireIntrinsicFallback(langJS, n.Func)
		call := fname + "(" + strings.Join(args, ", ") + ")"
		if n.Func.IsAsync {
			call = "await " + call
		}
		return call
	}
	// Funcvar invocation: Func is nil, Callee holds the funcvar expression.
	// Prepend `await` when the slot's points-to color is Async; without a
	// color entry fall back to "any async candidate ⇒ await".
	if n.Callee != nil {
		return jc.evalFuncvarCall(n)
	}
	// Codegen-only fallback: neither Func nor Callee resolved. Emit a
	// valid no-op expression so the surrounding statement parses.
	return "void 0 /* unresolved call */"
}

func (jc *JsIRContext) evalNativeCall(n *ir.Call) string {
	mod := n.Func.Foreign.Path
	name := n.Func.Foreign.Name
	if name == "" {
		name = n.Func.Name
	}
	bundled := isBundledNativePkg(jc.Ctx.Pkg, mod)
	if bundled {
		jc.registerNativeImport(mod, name)
	}
	args := jc.evalCallArgs(n.Args)
	var call string
	if bundled {
		call = codegen.NativeAlias(mod) + "." + name + "(" + strings.Join(args, ", ") + ")"
	} else {
		call = name + "(" + strings.Join(args, ", ") + ")"
	}
	if n.Func.IsAsync {
		call = "await " + call
	}
	return call
}

func (jc *JsIRContext) evalFuncvarCall(n *ir.Call) string {
	calleeJS := jc.EvalExpr(n.Callee)
	args := jc.evalCallArgs(n.Args)
	call := calleeJS + "(" + strings.Join(args, ", ") + ")"

	if jc.Ctx.Pkg != nil && jc.Ctx.Pkg.PointsTo != nil {
		if k, ok := ir.CalleeSlotKey(n.Callee); ok {
			pts := jc.Ctx.Pkg.PointsTo
			if color, present := pts.SlotColor[k]; present {
				if color == ir.ColorAsync {
					call = "await " + call
				}
				return call
			}
			for _, fn := range pts.Candidates(k) {
				if fn.IsAsync {
					call = "await " + call
					break
				}
			}
		}
	}
	return call
}

func (jc *JsIRContext) registerNativeImport(mod, name string) {
	if jc.Ctx.NativeImports == nil {
		// Caller didn't preallocate — nothing to record into.
		return
	}
	if jc.Ctx.NativeImports[mod] == nil {
		jc.Ctx.NativeImports[mod] = map[string]bool{}
	}
	jc.Ctx.NativeImports[mod][name] = true
}

// jsSnglNamespace reports whether an expression is the alias of an imported
// SNGL package — one whose declarations this build emits itself, as opposed to
// a scheme import naming a real JavaScript module.
func jsSnglNamespace(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	ns, ok := id.Sym.(*ir.Namespace)
	return ok && ns.Pkg != nil
}

func (jc *JsIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := jc.EvalExpr(n.Receiver)
	args := jc.evalCallArgs(n.Args)

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

		// lower.CreateComponent(comp, props) → __cf_<name>(props). Mirrors
		// legacy translateIRNamespaceCall (translate_ir.go:390-404): the
		// component-instance lowering emits this intrinsic with the `lower`
		// namespace receiver, the component ident as arg[0], and the prop
		// struct as arg[1]. Must precede every dispatch below.
		if fname == "CreateComponent" {
			if len(n.Args) != 2 {
				return "/* CreateComponent: wrong arity */"
			}
			compIdent, ok := n.Args[0].Value.(*ir.Ident)
			if !ok {
				return "/* CreateComponent: arg[0] not an Ident */"
			}
			comp, ok := compIdent.Sym.(*ir.Component)
			if !ok {
				return "/* CreateComponent: arg[0].Sym not a Component */"
			}
			return factoryName(comp) + "(" + jc.EvalExpr(n.Args[1].Value) + ")"
		}

		// Intrinsic dispatch: stdlib intrinsics that map to per-locale
		// runtime entry points. After NoContext + InlinePure, i18n.*
		// wrapper calls have been lowered to direct intl.* intrinsic
		// calls with the locale threaded as the first arg.

		// For i18n.* calls the namespace receiver is the module object, not a
		// value argument. Pass only the real call args to the builtin dispatcher
		// so that a(0) is the first semantic argument (matches type-method path).
		if receiverName == "i18n" {
			if result := jsBuiltinMethodFromArgs(qualName, args); result != "" {
				return result
			}
		}

		// A SNGL package's declarations are emitted into this very module, so
		// the alias at the call site names nothing: `readout.cells(…)` is the
		// free `cells(…)` the emitter wrote. The alias survives only for a
		// native import, which carries a Foreign path.
		if jsSnglNamespace(n.Receiver) && n.Func.Foreign.Path == "" {
			return fname + "(" + strings.Join(args, ", ") + ")"
		}

		allArgs := append([]string{receiver}, args...)
		if result := jsBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		// Only for a receiver whose type the checker could not resolve. With an id
		// in hand the registry is the answer, and its absence has to reach
		// RequireIntrinsicFallback rather than be hidden by a name match.
		if n.Func == nil || n.Func.Intrinsic == "" {
			if result := jsBuiltinMethodFromArgs("*."+fname, allArgs); result != "" {
				return result
			}
		}

		// User-defined namespace-qualified function: emitted as a free
		// function `<Receiver>_<Method>(receiver, args...)`. Mirrors legacy
		// translate_ir.go:431-433 (scope.FuncNames path), which joins the
		// receiver expression plus the call args. Here the equivalent is a
		// scan of Pkg.Funcs for a matching Receiver+Name (same source the
		// type-method path uses).
		// Nothing above recognised it, so this is the generic emission and
		// the same guard the other paths carry applies.
		codegen.RequireIntrinsicFallback(langJS, n.Func)
		var call string
		if jc.Ctx != nil && jc.Ctx.Pkg != nil && jc.userFuncMatches(receiverName, fname) {
			jsName := strings.ReplaceAll(qualName, ".", "_")
			call = jsName + "(" + strings.Join(allArgs, ", ") + ")"
		} else {
			call = receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
		}
		if n.Func.IsAsync {
			call = "await " + call
		}
		return call
	}
	if receiver == "" {
		// Codegen-only fallback: receiver resolved to nothing (e.g. an
		// @event propagation site where the user didn't supply a handler).
		// Emit a no-op so the surrounding statement parses.
		return "void 0 /* unresolved namespace call */"
	}
	return receiver + "(" + strings.Join(args, ", ") + ")"
}

// userFuncMatches reports whether Pkg.Funcs holds a user-defined function with
// the given receiver type/namespace and name. This is the Pkg.Funcs analog of
// the legacy scope.FuncNames lookup used by both the namespace-call and
// type-method dispatch paths.
func (jc *JsIRContext) userFuncMatches(receiver, name string) bool {
	if jc.Ctx == nil || jc.Ctx.Pkg == nil {
		return false
	}
	for _, f := range jc.Ctx.Pkg.Funcs {
		if f.Receiver == receiver && f.Name == name {
			return true
		}
	}
	return false
}

func (jc *JsIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	args := jc.evalCallArgs(n.Args)
	if result := jsBuiltinMethodFromArgs(qualName, args); result != "" {
		return result
	}
	// Only for a receiver whose type the checker could not resolve. With an id
	// in hand the registry is the answer, and its absence has to reach
	// RequireIntrinsicFallback rather than be hidden by a name match.
	if n.Func == nil || n.Func.Intrinsic == "" {
		if result := jsBuiltinMethodFromArgs("*."+method, args); result != "" {
			return result
		}
	}

	// User-defined method on a user type: emitted as a free function
	// `<Receiver>_<Method>(args...)`. After passNoImplicitRecv, Args[0]
	// is the receiver expression (component-self ident → "state").
	if jc.Ctx != nil && jc.Ctx.Pkg != nil {
		for _, f := range jc.Ctx.Pkg.Funcs {
			if f.Receiver == receiverName && f.Name == method {
				return receiverName + "_" + method + "(" + strings.Join(args, ", ") + ")"
			}
		}
	}

	codegen.RequireIntrinsicFallback(langJS, n.Func)
	if len(args) >= 1 {
		recv := args[0]
		rest := args[1:]
		return recv + "." + method + "(" + strings.Join(rest, ", ") + ")"
	}
	return "null /* unresolved method " + qualName + " */"
}

func (jc *JsIRContext) evalConversion(n *ir.Conversion) string {
	if ir.IsNullToFuncConv(n) {
		return nullFuncStubJS(n.Type)
	}
	operand := jc.EvalExpr(n.Operand)
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt, ir.TypeFloat:
			return jsConvert(operand, n.Operand.ExprType(), n.Type)
		case ir.TypeString:
			// Flag the String() helper for emission, mirroring legacy
			// translateIRConversion. The plain-call string(x) path flags it
			// too; without this, an ir.Conversion-to-string would drop the
			// `function String(v)` helper once emitJSFunc/exprToJS migrate.
			if jc.Ctx != nil && jc.Ctx.Helpers != nil {
				jc.Ctx.Helpers["String"] = true
			}
			return "String(" + operand + ")"
		case ir.TypeBool:
			return "Boolean(" + operand + ")"
		}
	}
	return operand
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
	asyncPrefix := ""
	if n.Func.IsAsync {
		asyncPrefix = "async "
	}
	bodyJC := jc
	for _, p := range n.Func.Params {
		bodyJC = bodyJC.WithLocal(p.Name)
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := bodyJC.EvalExpr(ret.Value)
			if asyncPrefix == "" && len(params) == 1 {
				return params[0] + " => " + body
			}
			return asyncPrefix + "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString(asyncPrefix + "(" + strings.Join(params, ", ") + ") => {\n")
	for _, stmt := range n.Func.Block {
		for _, line := range bodyJC.EvalStmt(stmt) {
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

// WithLocal returns a new context with an additional local variable.
func (jc *JsIRContext) WithLocal(name string) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.WithLocal(name),
		EventVar: jc.EventVar,
	}
}

// WithRenamedLocal binds name as a local that renders as `as`. SNGL's method
// receiver is spelled `this`, which JavaScript will not accept as a parameter
// name, so the emitter picks another and the body has to agree with it.
func (jc *JsIRContext) WithRenamedLocal(name, as string) *JsIRContext {
	ctx := jc.Ctx.WithLocal(name)
	ctx.Renames[name] = as
	return &JsIRContext{
		Ctx:      ctx,
		EventVar: jc.EventVar,
	}
}

// WithEvent returns a clone with EventVar set.
func (jc *JsIRContext) WithEvent(eventVar string, param ir.Symbol) *JsIRContext {
	return &JsIRContext{
		Ctx:        jc.Ctx.Clone(),
		EventVar:   eventVar,
		EventParam: param,
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
	case "*.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "*.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "*.abs":
		return "Math.abs(" + a(0) + ")"
	case "*.length":
		return a(0) + ".length"
	case "*.upper":
		return a(0) + ".toUpperCase()"
	case "*.lower":
		return a(0) + ".toLowerCase()"
	case "*.trim":
		return a(0) + ".trim()"
	case "*.replace":
		return a(0) + ".replaceAll(" + a(1) + ", " + a(2) + ")"
	case "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "*.substring":
		return a(0) + ".substring(" + a(1) + ", " + a(2) + ")"
	case "*.join":
		return a(0) + ".join(" + a(1) + ")"
	case "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "*.map":
		return a(0) + ".map(" + a(1) + ")"
	case "*.reverse":
		return "[..." + a(0) + "].reverse()"
	case "*.floor":
		return "Math.floor(" + a(0) + ")"
	case "*.ceil":
		return "Math.ceil(" + a(0) + ")"
	case "*.round":
		return "Math.round(" + a(0) + ")"
	case "*.sqrt":
		return "Math.sqrt(" + a(0) + ")"
	// map
	case "i18n.tr":
		// Args from translateIRTypeMethodCall: a(0)=key, a(1)=argsMap.
		// The JS runtime's Translator.tr(key, inlinedTemplate, args) takes three
		// arguments. The compiler holds no separate manifest key — the template
		// string doubles as both the lookup key and the inline fallback, so it is
		// passed twice. A future manifest-backed build can replace a(0) with the
		// manifest key without changing the call shape.
		return "i18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
	case "i18n.trInline":
		// Args: a(0)=key, a(1)=inlinedTemplate, a(2)=argsMap. The $"..."
		// lowering emits this 3-arg form so the runtime can fall back to the
		// inlined template when the manifest misses the key.
		return "i18n.getTranslator().tr(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
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
		// Args: dt datetime, dateStyle string, timeStyle string.
		return "i18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.select":
		// Args: value string, cases map.
		return "i18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
	case "i18n.plural":
		// Args: count, forms. a(0)=count, a(1)=forms.
		return "i18n.getTranslator().plural(" + a(0) + ", " + a(1) + ")"
	case "i18n.selectordinal":
		// Args: count, forms. a(0)=count, a(1)=forms.
		return "i18n.getTranslator().selectordinal(" + a(0) + ", " + a(1) + ")"
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "i18n.defaultLocale()"
	}
	return ""
}

// EmitFuncDef renders a complete JavaScript function definition from
// an *ir.Func. JS has no method receivers — emits as a top-level
// declaration:
//
//	function <name>(params...) { body... }
//
// Returns lines without trailing newlines. Caller joins with "\n".
func (jc *JsIRContext) EmitFuncDef(fn *ir.Func) []string {
	var lines []string
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name
	}
	lines = append(lines, "function "+fn.Name+"("+strings.Join(params, ", ")+") {")

	bodyJC := jc
	for _, p := range fn.Params {
		bodyJC = bodyJC.WithLocal(p.Name)
	}
	for _, stmt := range fn.Block {
		for _, line := range bodyJC.EvalStmt(stmt) {
			lines = append(lines, "\t"+line)
		}
	}

	lines = append(lines, "}")
	return lines
}
