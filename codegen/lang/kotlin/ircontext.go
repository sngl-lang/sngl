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
func (kc *KtIRContext) Literal(n *ir.Literal) string { return ktLiteral(n) }
func (kc *KtIRContext) Ident(n *ir.Ident) string     { return kc.evalIdent(n) }

func (kc *KtIRContext) Binary(n *ir.Binary, left, right string) string {
	if out, ok := multiBaseUnitBinaryKt(n, left, right); ok {
		return out
	}
	return "(" + left + " " + n.Op.String() + " " + right + ")"
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
	return target + " " + n.Op.String() + " " + value
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
// valueCopyFor is valueCopy for a local binding, skipping the copy when
// nothing writes the binding.
//
// The copy exists so a mutation cannot reach whoever else holds the value; a
// binding that is only read has no mutation to contain, and `passMutatedVars`
// is what says which is which. Absent the annotation -- a pipeline that did
// not lower -- every struct is copied, which is the old behaviour and the safe
// direction.
func (kc *KtIRContext) valueCopyFor(n *ir.LocalVar, rendered string) string {
	if n.Sym != nil && kc.Ctx != nil && kc.Ctx.Pkg != nil && kc.Ctx.Pkg.MutatedVars != nil {
		if !kc.Ctx.Pkg.MutatedVars[n.Sym] {
			return rendered
		}
	}
	return valueCopy(n.Init, n.Type, rendered)
}

func valueCopy(init ir.Expr, t *ir.Type, rendered string) string {
	if t == nil || t.Kind != ir.TypeStruct || t.Decl == nil {
		return rendered
	}
	if ir.StringReprStruct(t) {
		return rendered
	}
	// A native struct names a Kotlin class this build does not emit, so there
	// is no generated data class and no `copy()` to call -- Compose's `Path`
	// has none. It is a reference the host owns, which is also why copying it
	// would be wrong even if the method existed.
	if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Foreign.Name != "" {
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
		return "var " + n.Name + " = " + kc.valueCopyFor(n, initStr)
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
	// A loop that declared no variable still binds one: Kotlin's `_` is for
	// destructuring, and an unused loop variable is only a warning.
	key, valueVar := n.Key, n.Value
	if key == "" {
		key, valueVar = "__x", "__v"
	}
	switch n.IterKind {
	case ir.IterForever:
		// Kotlin has no bare `for`: its `for` takes an iterable, so a loop
		// over nothing is a `while`.
		return "while (true) {"
	case ir.IterCondition:
		return "while (" + iter + ") {"
	case ir.IterCounted:
		// A Kotlin range evaluates its bounds once, so there is no temp to
		// bind. `downTo end + 1` is how a descending range keeps the end
		// bound exclusive, which is what sngl:seq documents.
		c := n.Counted
		start, end := kc.EvalExpr(c.Start), kc.EvalExpr(c.End)
		var progression string
		switch {
		case c.Step == 1:
			progression = fmt.Sprintf("%s until %s", start, end)
		case c.Step > 0:
			progression = fmt.Sprintf("(%s until %s) step %d", start, end, c.Step)
		default:
			progression = fmt.Sprintf("(%s downTo %s + 1) step %d", start, end, -c.Step)
		}
		if n.Value != "" {
			// Two variables: Key is the ordinal, Value the number. withIndex
			// over a progression counts alongside it and builds nothing.
			return fmt.Sprintf("for ((%s, %s) in (%s).withIndex()) {", n.Key, n.Value, progression)
		}
		return fmt.Sprintf("for (%s in %s) {", key, progression)
	case ir.IterMapEntries:
		// Map iteration: for ((k, v) in m) { ... }
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for ((%s, %s) in %s) {", key, valueVar, iter)
	case ir.IterIndexed:
		// Two-var list/iter: Key is the index, Value the element.
		// withIndex() yields (index, element), matching that order.
		return fmt.Sprintf("for ((%s, %s) in %s.withIndex()) {", key, valueVar, iter)
	default:
		// Single-var list / iter<T>: for (x in list) { ... }
		return fmt.Sprintf("for (%s in %s) {", key, iter)
	}
}
func (kc *KtIRContext) IfHead(_ *ir.If, cond string) string { return "if (" + cond + ") {" }
func (kc *KtIRContext) ElseHead() string                    { return "} else {" }
func (kc *KtIRContext) BlockEnd() string                    { return "}" }
func (kc *KtIRContext) Indent() string                      { return "\t" }

func (kc *KtIRContext) MutTargetIdent(n *ir.Ident) string {
	if host, ok := kc.hostValueIdent(n); ok {
		return host
	}
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

// ktLiteral spells one IR literal. A free function because it reads nothing
// off the context and both entry points need it -- IRLiteralToKt used to carry
// its own copy, and the two had already drifted on the exponent case.
func ktLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
	}
	if s, ok := UnitLiteralKt(n); ok {
		return s
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
		if n.Type.Bits == 32 {
			// Kotlin has no implicit Double -> Float, so a 32-bit literal
			// carries the suffix that makes it the type IRTypeToKt already
			// calls it. Without it a folded `float32(0.0)` reached a Float
			// position as a Double and did not compile.
			s += "f"
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
	if host, ok := kc.hostValueIdent(n); ok {
		return host
	}
	if kc.IdentRewrites != nil {
		if rewritten, ok := kc.IdentRewrites[name]; ok {
			return rewritten
		}
	}
	if p, ok := n.Sym.(*ir.Param); ok && p.Receiver {
		return name
	}
	_, kind := kc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return SafeIdent(kc.Ctx.RenamedName(name))
	default:
		return SafeIdent(name)
	}
}

// nativeCall emits a call to the Kotlin identifier a #[kotlin.native]
// declaration names.
//
// Three shapes, as the Go and JavaScript backends have: the name as written
// with the arguments handed to it, the same with the receiver first, and
// `method` for a call *on* the first argument. Compose's drawing functions
// take the first -- inside a `DrawScope` the receiver is the scope and the
// call names none.
func (kc *KtIRContext) nativeCall(n *ir.Call) (string, bool) {
	if n.Func == nil || n.Func.Foreign.Name == "" || n.Func.Foreign.Scheme != "kotlin" {
		return "", false
	}
	if n.Func.Foreign.Path != "" {
		kc.RequireImport(n.Func.Foreign.Path)
	}
	name := n.Func.Foreign.Name
	args := kc.evalCallArgs(n.Args)
	if n.Func.NativeMethod && len(args) > 0 {
		return args[0] + "." + ktMethodTail(name) + "(" + strings.Join(args[1:], ", ") + ")", true
	}
	if n.Func.NativeNamedArgs {
		args = ktNameArgs(n.Func.Params, args)
	}
	return name + "(" + strings.Join(args, ", ") + ")", true
}

// ktNameArgs prefixes each argument with the parameter it fills, which is what
// lets a call reach a host parameter that sits after one with a default.
//
// An argument with no parameter to name -- there should be none -- is left
// positional rather than dropped.
func ktNameArgs(params []*ir.Param, args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, p := range params {
		if i >= len(out) || p.Name == "" {
			continue
		}
		out[i] = p.Name + " = " + out[i]
	}
	return out
}

// ktMethodTail is the last segment of a dotted native name: the method to
// invoke on the receiver, with the type or namespace prefix dropped because
// the receiver supplies it.
func ktMethodTail(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (kc *KtIRContext) evalCall(n *ir.Call) string {
	// Intrinsic dispatch by ID — never by method name. Backends register only
	// the intrinsics they emit; unregistered IDs fall through.
	if out, imports, ok := codegen.EmitIntrinsicCall(langKt, kc.Ctx.Platform, n, kc.EvalExpr); ok {
		for _, p := range imports {
			kc.RequireImport(p)
		}
		return out
	}
	// A declaration that *is* a Kotlin identifier: the call becomes a call to
	// it and nothing is emitted for the declaration. Before the receiver and
	// the scope lookups, because those answer for a function this build emits
	// and a native is not one.
	if out, ok := kc.nativeCall(n); ok {
		return out
	}
	if n.Receiver != nil {
		return kc.evalNamespaceCall(n)
	}
	// Before the receiver: the enclosing scope's own declaration wins over the
	// name the receiver spells, because a method the inliner hoisted onto the
	// window still names a component that is gone from the package by then.
	if n.Func != nil {
		if name, ok := kc.scopeFuncName(n.Func); ok {
			if codegen.IsComputed(n.Func) {
				return name
			}
			return name + "(" + strings.Join(kc.evalCallArgs(n.Args), ", ") + ")"
		}
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
				if kc.isScopeComputed(fname, n.Func) {
					return rewritten
				}
				return rewritten + "(" + strings.Join(args, ", ") + ")"
			}
		}
		if kc.isScopeComputed(fname, n.Func) {
			return fname
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
		// The receiver is an import alias when the declaration carries no
		// receiver of its own: a directory import qualifies the call in SNGL
		// and names nothing in the emitted Kotlin, where the function is a
		// top-level `fun` of its own name. `readout.cells("1.5")` reached the
		// android test runner verbatim, against a `cells` declared beside it.
		if n.Func.Receiver == "" {
			name := fname
			// Through IdentRewrites like every other bare call: test mode
			// reaches a hoisted state member as `state.<name>`.
			if kc.IdentRewrites != nil {
				if rw, ok := kc.IdentRewrites[name]; ok {
					name = rw
				}
			}
			if kc.isScopeComputed(fname, n.Func) {
				return name
			}
			codegen.RequireIntrinsicFallback(langKt, n.Func)
			return name + "(" + strings.Join(args, ", ") + ")"
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

// isScopeComputed reports whether a bare call names derived state of the
// enclosing scope, which Compose emits as a `val <name> by remember {
// derivedStateOf { … } }` and so reads by name rather than calling.
func (kc *KtIRContext) isScopeComputed(name string, fn *ir.Func) bool {
	if fn == nil || !codegen.IsComputed(fn) || kc.Ctx == nil {
		return false
	}
	sym, kind := kc.Ctx.Resolve(name)
	return kind == codegen.NameComputed && sym == ir.Symbol(fn)
}

// scopeFuncName is the name a call reaches fn by when the enclosing scope
// declares it. Compose puts such a declaration inside the composable, so the
// call is by bare name and no receiver is involved.
func (kc *KtIRContext) scopeFuncName(fn *ir.Func) (string, bool) {
	if fn == nil || kc.Ctx == nil {
		return "", false
	}
	// A method on a struct or an enum is emitted as an extension function and
	// is called as one, so the scope must not claim its bare name: the package
	// scope answers for every func it holds and a method is one of them, so
	// `func Item.label()` resolved here and came out as `label(it)` against a
	// declaration spelled `fun Item.label()`. A component's method is the case
	// this lookup exists for and is left alone -- there is no Kotlin type to
	// extend, and the composable calls it by bare name.
	if fn.Receiver != "" && ReceiverIsUserType(kc.pkg(), fn.Receiver) {
		return "", false
	}
	sym, kind := kc.Ctx.Resolve(fn.Name)
	if sym != ir.Symbol(fn) || (kind != codegen.NameComputed && kind != codegen.NameFunc) {
		return "", false
	}
	name := fn.Name
	if kc.IdentRewrites != nil {
		if rw, ok := kc.IdentRewrites[name]; ok {
			name = rw
		}
	}
	return name, true
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
	// A narrowed option: Kotlin's option<T> is T?, and the null test is what
	// makes the assertion safe.
	if ir.IsOptionUnwrap(n) {
		return operand + "!!"
	}
	// And the promotion the other way needs nothing: T is a subtype of T?.
	if ir.IsOptionWrap(n) {
		return operand
	}
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
			if s, ok := UnitToStringKt(n, operand); ok {
				return s
			}
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
		// A color is the emitted Color data class, matching the
		// `Color(r=…, g=…, b=…, a=…)` every color *value* already renders as
		// and what `color.hex` and composeColorExpr read. Answering String
		// here left a signature disagreeing with the values crossing it.
		if ir.IsColorStruct(t) {
			return colorKtType
		}
		// date/time/datetime are string-representable stdlib structs; the
		// Kotlin runtime carries them as String (quoted ISO strings, matching
		// their literal emission).
		if ir.StringReprStruct(t) {
			return "String"
		}
		if t.Decl != nil {
			// i18n.PluralKey is represented as String in Kotlin — the Kotlin i18n
			// runtime uses string plural categories exclusively.
			if t.Decl.SymName() == "PluralKey" {
				return "String"
			}
			// An unmarked native struct *is* the Kotlin type, so the
			// declaration's own name names nothing this file emits: android's
			// `#[kt.native("androidx.compose.ui.graphics.StrokeCap")]` came
			// out as the undeclared `StrokeCap`. The same rule IRTypeToGo
			// already applies.
			if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Foreign.Name != "" && !sd.Foreign.Marked {
				return sd.Foreign.Name
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
		return UnitKtType(ir.UnitDeclOf(t))
	case ir.TypeFunc:
		return "Any" // TODO: proper function types
	case ir.TypeNull, ir.TypeDyn:
		// dyn holds anything including null (recursive structs like TreeNode
		// use `dyn = null` for absent children).
		return "Any?"
	case ir.TypeIter:
		// Iterable<T>, not List<T>: an iter<T> is a sequence something pulls
		// from, and Iterable is the widest spelling of that -- a List is one,
		// and so is the IntProgression sngl:seq produces. It is also why a
		// list reaching an iter<T> position needs no conversion emitted.
		if len(t.Elems) == 1 {
			return "Iterable<" + IRTypeToKt(t.Elems[0]) + ">"
		}
		return "Iterable<Any>"
	case ir.TypeVoid, ir.TypeComponent, ir.TypeInstance, ir.TypeTypeParam,
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
		return ktLiteral(n)
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

// SplitFn names the helper `string.split` lowers to, and SplitDecl is its
// declaration. Kotlin's own split returns a leading and a trailing empty
// string for an empty separator; Go and JavaScript return the characters.
const SplitFn = "_snglSplit"

// The character walk matches JavaScript, which splits by UTF-16 unit. Go
// splits an empty separator by rune, so a non-BMP character already differs
// between those two; this follows the nearer of the pair.
const SplitDecl = `fun ` + SplitFn + `(s: String, sep: String): List<String> =
    if (sep.isEmpty()) s.map { it.toString() } else s.split(sep)
`

const FloatStringDecl = `fun ` + FloatStringFn + `(v: Double): String =
    if (v.isFinite() && v == kotlin.math.floor(v) && kotlin.math.abs(v) < 9.007199254740992E15) v.toLong().toString() else v.toString()
`

// colorKtType names the Kotlin class SNGL's `color` is carried as, and
// ColorDecl is its declaration -- the counterpart of Go's
// pkg/go/snglcolor.Color. Channels are Int, not the UByte the SNGL
// declaration's uint8 would give, so that the stdlib helpers (color.rgb,
// color.lighten, ...) and Compose's own Int channel constructor both reach it
// without a conversion per channel.
const colorKtType = "Color"

// toString mirrors snglcolor.Color.String: a colour is its complete value, so
// a translucent one keeps its alpha rather than printing as an opaque one.
const ColorDecl = `data class ` + colorKtType + `(
    var r: Int = 0,
    var g: Int = 0,
    var b: Int = 0,
    var a: Int = 255
) {
    override fun toString(): String =
        if (a < 255) "rgba($r,$g,$b,${a / 255.0})" else String.format("#%02x%02x%02x", r, g, b)
}
`

// pkg is the package being emitted, or nil.
func (kc *KtIRContext) pkg() *ir.Package {
	if kc.Ctx == nil {
		return nil
	}
	return kc.Ctx.Pkg
}

// ReceiverIsUserType reports whether name is a struct or enum the package
// declares, as opposed to a component. It is what decides that a method is
// emitted as a Kotlin extension function, and so has to be asked at the call
// site too -- the declaration and the call disagreeing is what left
// `fun Item.label()` beside a call to `label(it)`.
func ReceiverIsUserType(pkg *ir.Package, name string) bool {
	if pkg == nil || name == "" {
		return false
	}
	for _, sd := range pkg.Structs {
		if sd.Name == name {
			return true
		}
	}
	for _, ed := range pkg.Enums {
		if ed.Name == name {
			return true
		}
	}
	return false
}

// hostValueIdent resolves a reference to a const or var that *is* a Kotlin
// identifier -- `StrokeCap.Round`, a property with no call form at all -- to
// that identifier, and requires its import. Nothing is emitted for the
// declaration. Asked on the read and the write path both, and ahead of the
// rewrites and of Resolve in each: the names those answer for are this
// package's, and this one is not.
func (kc *KtIRContext) hostValueIdent(n *ir.Ident) (string, bool) {
	v, ok := n.Sym.(*ir.Var)
	if !ok || !ir.IsHostValue(v) {
		return "", false
	}
	if v.Foreign.Path != "" {
		kc.RequireImport(v.Foreign.Path)
	}
	return v.Foreign.Name, true
}
