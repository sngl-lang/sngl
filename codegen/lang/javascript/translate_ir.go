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

// factoryName returns the JS factory function name for a component.
// MUST match the convention used by HTML codegen's factory emission.
// If you change this, also update codegen/platform/html/html.go.
func factoryName(comp *ir.Component) string {
	return "__cf_" + sanitizeJSIdent(comp.Name)
}

func sanitizeJSIdent(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

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
		// Predeclared i18n.PluralKey constants (zero/one/two/few/many/other)
		// lower to JS string literals — the JS runtime uses string keys exclusively.
		if ident, ok := n.Operand.(*ir.Ident); ok && ident.Name == "i18n" {
			if s := jsI18nConstString("i18n." + n.Field); s != "" {
				return s
			}
		}
		// When the operand is a native namespace ident and the module is
		// bundled (js://), emit the esbuild-compatible alias and register the
		// module so the platform emits the corresponding `import * as` prelude.
		if ident, ok := n.Operand.(*ir.Ident); ok {
			if _, ok := ident.Sym.(*ir.Namespace); ok {
				if jsAlias, importPath := nativeBundledNamespaceAlias(ident.Name, scope); jsAlias != "" {
					registerNativeImport(scope, importPath, n.Field)
					return jsAlias + "." + n.Field
				}
			}
		}
		return translateIRExpr(n.Operand, scope) + "." + n.Field
	case *ir.Index:
		operand := translateIRExpr(n.Operand, scope)
		idx := translateIRExpr(n.Idx, scope)
		if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
			return operand + ".get(" + idx + ")"
		}
		return operand + "[" + idx + "]"
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
	case *ir.MapLitIR:
		// Special case: map<i18n.PluralKey, V> lowers to a plain JS object with
		// string keys. The JS runtime accepts string keys exclusively; PluralKey
		// only exists in the Go runtime where map keys need value equality.
		if isPluralKeyMapType(n.Type) {
			var b strings.Builder
			b.WriteString("{")
			for i, e := range n.Entries {
				if i > 0 {
					b.WriteString(", ")
				}
				// Keys are already string-typed expressions at this point:
				// predeclared constants were lowered to string literals by the
				// *ir.Select case above, and i18n.exactly(n) emits ("=" + n).
				b.WriteString("[")
				b.WriteString(translateIRExpr(e.Key, scope))
				b.WriteString("]: ")
				b.WriteString(translateIRExpr(e.Value, scope))
			}
			b.WriteString("}")
			return b.String()
		}
		var b strings.Builder
		b.WriteString("new Map([")
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[")
			b.WriteString(translateIRExpr(e.Key, scope))
			b.WriteString(", ")
			b.WriteString(translateIRExpr(e.Value, scope))
			b.WriteString("]")
		}
		b.WriteString("])")
		return b.String()
	case *ir.Spread:
		return "..." + translateIRExpr(n.Operand, scope)
	case *ir.Lambda:
		return translateIRLambda(n, scope)
	default:
		panic(fmt.Sprintf("translateIRExpr: unhandled ir.Expr %T", e))
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
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "null"
		case ir.TypeString, ir.TypeColor,
			ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
			ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64,
			ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal:
			// ir.Literal.Raw mirrors ast.LiteralExpr.Raw — the unquoted text.
			return fmt.Sprintf("%q", n.Raw)
		}
	}
	return n.Raw
}

func translateIRIdent(n *ir.Ident, scope *codegen.ExprScope) string {
	// Component-self ident: synthesized by passNoImplicitRecv as the
	// implicit receiver of a desugared component method. The JS emission
	// uses `state` for per-instance state of the currently-emitting
	// component.
	if _, ok := n.Sym.(*ir.Component); ok {
		return "state"
	}
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
	// Intrinsic dispatch by ID — never by method name — covering both
	// type-method and inlined direct-intrinsic call shapes.
	if out, _, ok := codegen.EmitIntrinsicCall(langJS, n, func(e ir.Expr) string { return translateIRExpr(e, scope) }); ok {
		return out
	}
	// Native scheme-import call (e.g. js://): emit the imported name
	// directly and record the module → name binding for top-level
	// `import { ... } from "module"` emission by the platform.
	if n.Func != nil && n.Func.NativePkg != "" {
		return translateIRNativeCall(n, scope)
	}

	// Namespace / component call: Receiver expression is preserved.
	if n.Receiver != nil {
		return translateIRNamespaceCall(n, scope)
	}

	// Type-attached method call (checker-normalized).
	if n.Func != nil && n.Func.Receiver != "" {
		return translateIRTypeMethodCall(n, scope)
	}

	// Funcvar invocation: Func is nil, Callee holds the funcvar expression.
	// Prepend `await` when the slot's color is Async; for slots without a
	// color entry (SlotParam / SlotReturn), use the conservative fallback:
	// any async candidate ⇒ await.
	if n.Func == nil && n.Callee != nil {
		return translateIRFuncvarCall(n, scope)
	}

	// Plain function call.
	return translateIRPlainCall(n, scope)
}

func translateIRPlainCall(n *ir.Call, scope *codegen.ExprScope) string {
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
		// Codegen-only fallback: callee resolved to nothing. Emit a no-op
		// expression that is valid JS so the surrounding statement parses.
		return "void 0 /* unresolved call */"
	}
	call := fn + "(" + strings.Join(argStrs, ", ") + ")"
	if n.Func != nil && n.Func.IsAsync {
		call = "await " + call
	}
	return call
}

// translateIRFuncvarCall handles funcvar invocations where Func is nil and
// Callee holds the funcvar expression. It prepends `await` when the slot's
// points-to color is ColorAsync. For slots with no color entry (e.g.
// SlotParam, SlotReturn), it uses the conservative g3 rule: any async
// candidate ⇒ await.
func translateIRFuncvarCall(n *ir.Call, scope *codegen.ExprScope) string {
	calleeJS := translateIRExpr(n.Callee, scope)
	if calleeJS == "" {
		// Codegen-only fallback: callee resolved to nothing (e.g. an
		// @event propagation site where the user didn't supply a handler).
		// Emit a no-op so the surrounding statement parses.
		return "void 0 /* unresolved funcvar call */"
	}
	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	call := calleeJS + "(" + strings.Join(argStrs, ", ") + ")"

	if scope.Pkg != nil && scope.Pkg.PointsTo != nil {
		if k, ok := ir.CalleeSlotKey(n.Callee); ok {
			pts := scope.Pkg.PointsTo
			if color, present := pts.SlotColor[k]; present {
				if color == ir.ColorAsync {
					call = "await " + call
				}
			} else {
				// No color entry: conservative fallback — await if any
				// candidate is async.
				for _, fn := range pts.Candidates(k) {
					if fn.IsAsync {
						call = "await " + call
						break
					}
				}
			}
		}
	}
	return call
}

// translateIRNativeCall emits a call to a function imported via a scheme
// (e.g. js://). Records the module → name binding on scope.NativeImports
// so the platform can emit a top-level ES import. Wraps with `await` when
// the imported func is declared async.
func translateIRNativeCall(n *ir.Call, scope *codegen.ExprScope) string {
	mod := n.Func.NativePkg
	name := n.Func.NativeName
	if name == "" {
		name = n.Func.Name
	}
	bundled := isBundledNativePkg(scope.Pkg, mod)
	if bundled {
		registerNativeImport(scope, mod, name)
	}

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	var call string
	if bundled {
		call = codegen.NativeAlias(mod) + "." + name + "(" + strings.Join(argStrs, ", ") + ")"
	} else {
		call = name + "(" + strings.Join(argStrs, ", ") + ")"
	}
	if n.Func.IsAsync {
		call = "await " + call
	}
	return call
}

func translateIRNamespaceCall(n *ir.Call, scope *codegen.ExprScope) string {
	if n.Func == nil {
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		recv := translateIRExpr(n.Receiver, scope)
		if recv == "" {
			// Codegen-only fallback: receiver resolved to nothing (e.g. an
			// @event propagation site where the user didn't supply a handler).
			// Emit a guarded no-op so the surrounding statement parses and
			// runtime doesn't TypeError on calling undefined.
			return "void 0 /* unresolved namespace call */"
		}
		return recv + "(" + strings.Join(argStrs, ", ") + ")"
	}
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method
	receiverJS := translateIRExpr(n.Receiver, scope)

	// lower.CreateComponent(comp, props) → __cf_<name>(props)
	if method == "CreateComponent" {
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
		propsJS := translateIRExpr(n.Args[1].Value, scope)
		return factoryName(comp) + "(" + propsJS + ")"
	}

	// For i18n.* calls the namespace receiver is the module object, not a
	// value argument. Pass only the real call args to the builtin dispatcher
	// so that a(0) is the first semantic argument (matches type-method path).
	if receiverName == "i18n" {
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		if js := jsBuiltinMethodFromArgs(qualName, argStrs); js != "" {
			return js
		}
	}

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

	var call string
	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		jsName := strings.ReplaceAll(qualName, ".", "_")
		call = jsName + "(" + strings.Join(argsForBuiltin, ", ") + ")"
	} else {
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = translateIRExpr(a.Value, scope)
		}
		call = receiverJS + "." + method + "(" + strings.Join(argStrs, ", ") + ")"
	}
	if n.Func.IsAsync {
		call = "await " + call
	}
	return call
}

// translateIRTypeMethodCall handles checker-normalized type-method calls:
// Func.Receiver holds the type name and Args[0] holds the receiver value.
func translateIRTypeMethodCall(n *ir.Call, scope *codegen.ExprScope) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}

	if js := jsBuiltinMethodFromArgs(qualName, argStrs); js != "" {
		return js
	}
	if js := jsBuiltinMethodFromArgs("*."+method, argStrs); js != "" {
		return js
	}

	if scope.FuncNames != nil && scope.FuncNames[qualName] {
		jsName := strings.ReplaceAll(qualName, ".", "_")
		return jsName + "(" + strings.Join(argStrs, ", ") + ")"
	}

	if len(argStrs) >= 1 {
		recv := argStrs[0]
		rest := argStrs[1:]
		return recv + "." + method + "(" + strings.Join(rest, ", ") + ")"
	}
	// Emit a valid expression even when the method is unresolved so the
	// surrounding statement still parses. Real diagnostics come from the
	// checker; this branch only fires for codegen-only fallbacks.
	return "null /* unresolved method " + qualName + " */"
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

	asyncPrefix := ""
	if n.Func.IsAsync {
		asyncPrefix = "async "
	}

	// Single-expression body.
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := translateIRExpr(ret.Value, &subScope)
			if asyncPrefix == "" && len(params) == 1 {
				return params[0] + " => " + body
			}
			return asyncPrefix + "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString(asyncPrefix + "(" + strings.Join(params, ", ") + ") => {\n")
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
		if n.Call.ErrorMode != ir.ErrorNone {
			if lines := translateErrorAwareCall(n.Call, scope); lines != nil {
				return lines
			}
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
	case *ir.For:
		return translateIRForJS(n, scope)
	default:
		panic(fmt.Sprintf("translateIRMutation: unhandled ir.Stmt %T", s))
	}
}

func translateIRForJS(n *ir.For, scope *codegen.ExprScope) []string {
	iterExpr := translateIRExpr(n.Iter, scope)
	loopScope := *scope
	locals := make(map[string]bool, len(scope.LocalVars)+2)
	maps.Copy(locals, scope.LocalVars)
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
		lines = append(lines, fmt.Sprintf("for (const [%s, %s] of %s.entries()) {", n.Key, valueVar, iterExpr))
	} else {
		lines = append(lines, fmt.Sprintf("for (const %s of %s) {", n.Key, iterExpr))
	}
	for _, stmt := range n.Body {
		for _, l := range translateIRMutation(stmt, &loopScope) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

// translateErrorAwareCall emits JS statements for a fallible call whose
// error handler was resolved by effect analysis. Scope-based analogue of
// JsIRContext.evalErrorAwareCall used by platforms that go through
// translateIRMutation (e.g., html-static).
func translateErrorAwareCall(call *ir.Call, scope *codegen.ExprScope) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !jsIsRaiseFunc(call.Func) {
		return nil
	}
	msg := `""`
	kind := `""`
	if len(call.Args) >= 1 {
		msg = translateIRExpr(call.Args[0].Value, scope)
	}
	if len(call.Args) >= 2 {
		kind = translateIRExpr(call.Args[1].Value, scope)
	}
	evt := fmt.Sprintf("{message: %s, kind: %s}", msg, kind)

	switch call.ErrorMode {
	case ir.ErrorPropagateNative, ir.ErrorBubble:
		return []string{"throw Object.assign(new Error(" + msg + "), {kind: " + kind + "})"}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler == nil || call.ResolvedHandler.Func == nil {
			return []string{"throw Object.assign(new Error(" + msg + "), {kind: " + kind + "})"}
		}
		return translateHandlerInvoke(evt, call.ResolvedHandler, scope)
	case ir.ErrorPerCall:
		if call.ErrorHandler == nil || call.ErrorHandler.Func == nil {
			return []string{"void " + evt}
		}
		return translateHandlerInvoke(evt, call.ErrorHandler, scope)
	}
	return nil
}

func translateHandlerInvoke(evt string, handler *ir.EventHandler, scope *codegen.ExprScope) []string {
	paramName := "e"
	if handler.Func != nil && len(handler.Func.Params) > 0 {
		paramName = handler.Func.Params[0].Name
	}
	subScope := *scope
	subScope.LocalVars = make(map[string]bool, len(scope.LocalVars)+1)
	maps.Copy(subScope.LocalVars, scope.LocalVars)
	subScope.LocalVars[paramName] = true
	lines := []string{
		"{",
		fmt.Sprintf("\tlet %s = %s", paramName, evt),
		fmt.Sprintf("\tvoid %s", paramName),
	}
	for _, stmt := range handler.Func.Block {
		for _, l := range translateIRMutation(stmt, &subScope) {
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

// nativeBundledNamespaceAlias resolves a namespace alias name (e.g. "api") to
// the esbuild-compatible JS identifier (NativeAlias(importPath)) when the
// namespace maps to a bundled js:// native import. Returns ("", "") when not
// applicable.
func nativeBundledNamespaceAlias(nsName string, scope *codegen.ExprScope) (jsAlias, importPath string) {
	if scope == nil || scope.Pkg == nil {
		return "", ""
	}
	for _, imp := range scope.Pkg.Imports {
		if imp == nil || imp.Alias != nsName || imp.Native == nil {
			continue
		}
		if !isBundledImport(imp) {
			continue
		}
		path := imp.Native.ImportPath
		return codegen.NativeAlias(path), path
	}
	return "", ""
}

// isBundledImport reports whether a native import is routed through the JS
// bundler (esbuild) instead of the WASM extern bridge. Decided by the source
// scheme: js:// is bundled, others (go://, etc.) go through WASM extern.
func isBundledImport(imp *ir.Import) bool {
	if imp == nil || imp.AST == nil {
		return false
	}
	scheme, _ := codegen.SplitScheme(imp.AST.Path)
	return scheme == "js"
}

// isBundledNativePkg reports whether the named native package path
// (NativePkg / ImportPath) corresponds to a bundled js:// import in pkg.
func isBundledNativePkg(pkg *ir.Package, nativePkg string) bool {
	if pkg == nil || nativePkg == "" {
		return false
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil {
			continue
		}
		if imp.Native.ImportPath != nativePkg {
			continue
		}
		return isBundledImport(imp)
	}
	return false
}

// registerNativeImport records a module → name binding on scope.NativeImports
// so the platform can emit a top-level ES `import * as` prelude for the module.
func registerNativeImport(scope *codegen.ExprScope, mod, name string) {
	if scope.NativeImports == nil {
		scope.NativeImports = map[string]map[string]bool{}
	}
	if scope.NativeImports[mod] == nil {
		scope.NativeImports[mod] = map[string]bool{}
	}
	scope.NativeImports[mod][name] = true
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

// isPluralKeyMapType reports whether t is map<i18n.PluralKey, V>.
// The JS translator lowers such maps to plain objects with string keys,
// because the JS i18n runtime uses string plural categories exclusively.
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
