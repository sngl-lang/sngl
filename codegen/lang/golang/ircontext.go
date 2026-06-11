package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/irwalk"
	"git.duckfam.us/jonathan/sngl/ir"
)

// GoIRContext translates IR expressions and statements into Go code.
// It replaces GoContext for platforms that have been ported to IR.
type GoIRContext struct {
	Ctx *codegen.ExprCtx

	// AlertFunc translates Alert.toast/info/warn/error calls.
	// If nil, a default "m.toasts = append(...)" implementation is used.
	AlertFunc func(ctx *GoIRContext, method string, args []ir.CallArg) []string

	// EmitLineDirectives controls whether EvalStmt prepends `//line file:line`
	// directives at statement boundaries. Set by platforms when req.Maps is
	// true. Go's compiler reads //line natively and attributes errors/panics
	// back to the SNGL source. Gofmt preserves these directives.
	EmitLineDirectives bool

	// imports records native Go import paths the translator decided it
	// needed while emitting expressions and statements. Populated by
	// emit-site calls to RequireImport; read post-translation by platforms
	// via Imports() so they no longer maintain their own goImports map.
	// Shared across forked contexts (WithLocal, ForComponent) so child
	// contexts contribute to the parent's set.
	imports *importSet
}

// importSet is the shared collector backing GoIRContext.imports. Keyed by
// import path; the bool value tracks whether the import is blank ("_") so
// platforms can render `_ "path"` when needed.
type importSet struct {
	paths   map[string]bool // path → blank?
	order   []string
	aliases map[string]string // path → forced alias (overrides goAliasFor)
}

func newImportSet() *importSet {
	return &importSet{paths: map[string]bool{}, aliases: map[string]string{}}
}

// NewIRContext creates a GoIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *GoIRContext {
	gc := &GoIRContext{Ctx: ctx, imports: newImportSet()}
	if ctx != nil {
		gc.EmitLineDirectives = ctx.Maps
	}
	return gc
}

// RequireImport records that the emitted Go file needs the given import
// path. Safe to call repeatedly; first insertion wins for ordering.
// Called from emit sites whenever a native package reference is rendered
// (e.g. `time.Now()`, `fmt.Fprintln(...)`). Platforms read the result
// after all translation via Imports().
func (gc *GoIRContext) RequireImport(path string) {
	if path == "" || gc.imports == nil {
		return
	}
	if _, seen := gc.imports.paths[path]; seen {
		return
	}
	gc.imports.paths[path] = false
	gc.imports.order = append(gc.imports.order, path)
}

// RequireImportAs records an import path that must render under an explicit
// alias (e.g. `alias "path"`), overriding the path-derived default. Used when
// the conventional alias would collide with another import and the call sites
// reference a fixed selector (e.g. snglcanvas for pkg/go/canvas alongside
// fyne's canvas). Safe to call repeatedly.
//
// Precondition: forced aliases must be unique across paths. renderImports
// de-conflicts path-derived defaults against forced aliases, but two distinct
// paths forced to the SAME alias would both render under it (invalid Go).
func (gc *GoIRContext) RequireImportAs(path, alias string) {
	if path == "" || gc.imports == nil {
		return
	}
	gc.RequireImport(path)
	if gc.imports.aliases != nil && alias != "" {
		gc.imports.aliases[path] = alias
	}
}

// ForcedAlias returns the explicit alias registered for path via
// RequireImportAs, or "" if none.
func (gc *GoIRContext) ForcedAlias(path string) string {
	if gc.imports == nil || gc.imports.aliases == nil {
		return ""
	}
	return gc.imports.aliases[path]
}

// RequireBlankImport records a side-effect-only import (`_ "path"`).
func (gc *GoIRContext) RequireBlankImport(path string) {
	if path == "" || gc.imports == nil {
		return
	}
	if _, seen := gc.imports.paths[path]; seen {
		return
	}
	gc.imports.paths[path] = true
	gc.imports.order = append(gc.imports.order, path)
}

// Imports returns the recorded import paths in insertion order.
func (gc *GoIRContext) Imports() []string {
	if gc.imports == nil {
		return nil
	}
	out := make([]string, len(gc.imports.order))
	copy(out, gc.imports.order)
	return out
}

// IsBlankImport reports whether the given path was registered as a
// blank import.
func (gc *GoIRContext) IsBlankImport(path string) bool {
	if gc.imports == nil {
		return false
	}
	return gc.imports.paths[path]
}

// EvalExpr translates an IR expression into a Go expression string.
func (gc *GoIRContext) EvalExpr(e ir.Expr) string { return irwalk.EvalExpr(gc, e) }

// EvalStmt translates an IR statement into Go statement strings.
func (gc *GoIRContext) EvalStmt(s ir.Stmt) []string { return irwalk.EvalStmt(gc, s) }

// --- irwalk.Renderer implementation ---

func (gc *GoIRContext) NilExpr() string              { return "nil" }
func (gc *GoIRContext) Literal(n *ir.Literal) string { return gc.evalLiteral(n) }
func (gc *GoIRContext) Ident(n *ir.Ident) string     { return gc.evalIdent(n) }

func (gc *GoIRContext) Binary(n *ir.Binary, left, right string) string {
	if out, ok := goMultiBaseUnitBinary(n, left, right); ok {
		return out
	}
	return "(" + left + " " + irBinaryOp(n.Op) + " " + right + ")"
}

// goMultiBaseUnitBinary handles binary ops whose result type is a multi-base
// unit struct (e.g. `Measurement + Measurement`, `2 * Measurement`) by
// expanding the operation component-wise over each base field. The operand
// strings `left`/`right` are already-rendered; `n.Left`/`n.Right` carry the IR
// types used to decide whether each side is a unit struct (field-projected) or
// a scalar (used verbatim). Returns ("", false) when neither operand is a
// multi-base unit. Mirrors legacy translateMultiBaseUnitBinary byte-for-byte.
func goMultiBaseUnitBinary(n *ir.Binary, left, right string) (string, bool) {
	ud, ok := multiBaseUnitOperand(n.Left, n.Right)
	if !ok {
		return "", false
	}
	leftIsStruct := isMultiBaseUnitType(n.Left.ExprType())
	rightIsStruct := isMultiBaseUnitType(n.Right.ExprType())
	op := binaryOpStr(n.Op)

	// Equality on two unit structs is fine via Go struct equality.
	if n.Op == ast.BinEq || n.Op == ast.BinNeq {
		return "(" + left + " " + op + " " + right + ")", true
	}

	bases := UnitBases(ud)
	parts := make([]string, 0, len(bases))
	for _, base := range bases {
		field := ExportName(base.Name)
		lhs := left + "." + field
		rhs := right + "." + field
		if !leftIsStruct {
			lhs = left
		}
		if !rightIsStruct {
			rhs = right
		}
		parts = append(parts, fmt.Sprintf("%s: %s %s %s", field, lhs, op, rhs))
	}
	return fmt.Sprintf("%s{%s}", ExportName(ud.Name), strings.Join(parts, ", ")), true
}

func (gc *GoIRContext) Unary(n *ir.Unary, operand string) string {
	if n.Op == ast.UnaryNot {
		return "!" + operand
	}
	return "-" + operand
}

func (gc *GoIRContext) Ternary(_ *ir.Ternary, _, _, _ string) string {
	panic("ir.Ternary reached Go codegen — NoTernary cap must be set for all Go platforms")
}

func (gc *GoIRContext) Select(n *ir.Select, operand string) string {
	if n.Field == "length" {
		return "len(" + operand + ")"
	}
	// i18n plural-category constants: map SNGL names (zero/one/…/other) to
	// the qualified Go runtime names (i18n.PluralZero/PluralOne/…/PluralOther).
	if operand == "i18n" {
		if goName := i18nPluralKeyGoName(n.Field); goName != "" {
			return "i18n." + goName
		}
	}
	// Test-scope raw field reads: a Select whose operand is a
	// RawFieldAccess ident reads the unexported field directly (no
	// ExportName capitalization) so a generated `_test.go` in the same Go
	// package can touch unexported Model fields. Mirrors legacy
	// translateIRExpr's Select case.
	if gc.rawFieldAccess(n.Operand) {
		// A field flagged in MethodFields is surfaced by platform codegen as
		// a zero-arg method (e.g. gtk4's nilable conditional/loop refs), so
		// the raw read lowers to a method call.
		if gc.methodField(n.Field) {
			return operand + "." + n.Field + "()"
		}
		return operand + "." + n.Field
	}
	// Test-scope property read on an id'd child node:
	// `c.<id>.<prop>` → `c.<id><Prop>()`. Platforms emit one getter method
	// per (id, prop) reactive binding; the test runner consumes them to
	// read widget state. The trigger is the outer Select's Operand being a
	// Select on a RawFieldAccess Ident — i.e. `c.<id>` after testlower set
	// RawFieldAccess for `c`.
	if inner, ok := n.Operand.(*ir.Select); ok {
		if id, ok := inner.Operand.(*ir.Ident); ok && gc.rawFieldAccess(id) {
			return fmt.Sprintf("%s.%s%s()", id.Name, inner.Field, ExportName(n.Field))
		}
	}
	// Test-scope list-ref prop read: `c.<id>[idx].<prop>` →
	// `c.<id>()[idx].<Prop>()`. Used when <id> sits inside a `for` loop;
	// gtk4 surfaces the per-iteration widgets as a `[]*<id>Ref` returned by
	// the `<id>()` method. Mirrors legacy.
	if idxExpr, ok := n.Operand.(*ir.Index); ok {
		if inner, ok := idxExpr.Operand.(*ir.Select); ok {
			if id, ok := inner.Operand.(*ir.Ident); ok && gc.rawFieldAccess(id) && gc.methodField(inner.Field) {
				return fmt.Sprintf("%s.%s()[%s].%s()", id.Name, inner.Field, gc.EvalExpr(idxExpr.Idx), ExportName(n.Field))
			}
		}
	}
	// Field access on a `dyn` operand: Go's `any` has no fields, so a
	// bare `.<F>` won't compile. If exactly one user struct in the
	// package declares this field, emit a type assertion to that
	// struct. Covers recursive-component patterns where a struct
	// field is typed `dyn` for self-reference (TreeNode.left/right).
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeDyn {
		if name := gc.uniqueStructWithField(n.Field); name != "" {
			return "(" + operand + ").(" + name + ")." + ExportName(n.Field)
		}
	}
	return operand + "." + ExportName(n.Field)
}

func (gc *GoIRContext) Index(_ *ir.Index, operand, idx string) string {
	return operand + "[" + idx + "]"
}

func (gc *GoIRContext) ListLit(n *ir.ListLit, elems []string) string {
	elemType := "any"
	if n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) > 0 {
		elemType = IRTypeToGo(n.Type.Elems[0])
	}
	return "[]" + elemType + "{" + strings.Join(elems, ", ") + "}"
}

func (gc *GoIRContext) MapLit(n *ir.MapLitIR, keys, vals []string) string {
	keyType := "any"
	valType := "any"
	if n.Type != nil && n.Type.Kind == ir.TypeMap && len(n.Type.Elems) == 2 {
		keyType = IRTypeToGo(n.Type.Elems[0])
		valType = IRTypeToGo(n.Type.Elems[1])
	}
	parts := make([]string, len(keys))
	for i := range keys {
		parts[i] = keys[i] + ": " + vals[i]
	}
	return "map[" + keyType + "]" + valType + "{" + strings.Join(parts, ", ") + "}"
}

func (gc *GoIRContext) StructLit(n *ir.StructLit, fieldStrs []string) string {
	parts := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		if f.Spread {
			panic("golang: struct spread must be lowered by flatten_struct_spread")
		}
		parts[i] = ExportName(f.Name) + ": " + fieldStrs[i]
	}
	return structLitTypeName(n) + "{" + strings.Join(parts, ", ") + "}"
}

func (gc *GoIRContext) Spread(_ *ir.Spread, operand string) string { return operand + "..." }

func (gc *GoIRContext) Call(n *ir.Call) string             { return gc.maybeWrapErrorReturn(n, gc.evalCall(n)) }
func (gc *GoIRContext) Conversion(n *ir.Conversion) string { return gc.evalConversion(n) }
func (gc *GoIRContext) Lambda(n *ir.Lambda) string         { return gc.evalLambda(n) }

func (gc *GoIRContext) AssignText(n *ir.Assign, target, value string) string {
	return target + " " + irAssignOp(n.Op) + " " + value
}
func (gc *GoIRContext) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target
}
func (gc *GoIRContext) CallStmtLines(n *ir.CallStmt) []string {
	if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
		if lines := gc.evalErrorAwareCall(n.Call); lines != nil {
			return lines
		}
	}
	return []string{gc.EvalExpr(n.Call)}
}
func (gc *GoIRContext) EmitText(n *ir.Emit, argStrs []string) string {
	return "emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"
}
func (gc *GoIRContext) LocalVarText(n *ir.LocalVar, initStr string) string {
	if n.Init != nil {
		return n.Name + " := " + initStr
	}
	goType := "any"
	if n.Type != nil {
		goType = IRTypeToGo(n.Type)
	}
	return "var " + n.Name + " " + goType
}
func (gc *GoIRContext) ReturnText(n *ir.Return, valueStr string) string {
	if n.Value != nil {
		return "return " + valueStr
	}
	return "return"
}

func (gc *GoIRContext) ForHead(n *ir.For, iter string) string {
	iterType := n.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		// Map iteration: for k, v := range m { ... }
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, valueVar, iter)
	}
	if n.Value != "" {
		// Two-var list/iter: Key is the index, Value the element.
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, n.Value, iter)
	}
	// Single-var list / iter<T>: for _, x := range list { ... }
	return fmt.Sprintf("for _, %s := range %s {", n.Key, iter)
}

func (gc *GoIRContext) IfHead(_ *ir.If, cond string) string { return "if " + cond + " {" }
func (gc *GoIRContext) ElseHead() string                    { return "} else {" }
func (gc *GoIRContext) BlockEnd() string                    { return "}" }
func (gc *GoIRContext) Indent() string                      { return "\t" }

func (gc *GoIRContext) MutTargetIdent(n *ir.Ident) string {
	_, kind := gc.Ctx.Resolve(n.Name)
	if kind == codegen.NameStateVar {
		if gc.Ctx.StateReceiver != "" {
			return gc.Ctx.StateReceiver + "." + ExportName(n.Name)
		}
		return "m." + n.Name
	}
	return n.Name
}
func (gc *GoIRContext) MutTargetField(field string) string { return ExportName(field) }

func (gc *GoIRContext) StmtPrefix(s ir.Stmt) []string {
	if !gc.EmitLineDirectives {
		return nil
	}
	pos := stmtIRPos(s)
	if !pos.IsValid() || pos.File == "" {
		return nil
	}
	return []string{fmt.Sprintf("//line %s:%d", pos.File, pos.Line)}
}

func (gc *GoIRContext) Scoped(name string) irwalk.Renderer { return gc.WithLocal(name) }

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
	case ir.TypeUnit:
		if out, ok := LowerUnitLiteralGo(n); ok {
			return out
		}
		return n.Raw
	case ir.TypeDate, ir.TypeTime, ir.TypeDateTime:
		if out, ok := LowerTimeLiteralGo(n); ok {
			return out
		}
		return fmt.Sprintf("%q", n.Raw)
	default:
		return n.Raw
	}
}

func (gc *GoIRContext) evalIdent(n *ir.Ident) string {
	// Bare enum member — emit as string literal
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}

	// Component-self ident: synthesized by passNoImplicitRecv as the
	// implicit receiver of a desugared component method. Go emission uses
	// `m` for the Bubbletea/Fyne Model receiver.
	if _, ok := n.Sym.(*ir.Component); ok {
		return "m"
	}

	name := n.Name
	// Synthesized element refs (e.g. `__n3` for a widget the lowering
	// passes created, or platform-emitted widget field names like
	// `label0`) are stored as Model struct fields. Qualify them here
	// so call sites emit `m.<name>` rather than a bare ident that
	// won't resolve in the generated method scope.
	if n.IsElementRef && n.Synthesized {
		return "m." + name
	}
	_, kind := gc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return gc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return "m." + name + "()"
	case codegen.NameStateVar:
		if gc.Ctx.StateReceiver != "" {
			return gc.Ctx.StateReceiver + "." + ExportName(name)
		}
		return "m." + name
	case codegen.NameConst:
		// Top-level free-function bodies aren't methods on Model; consts
		// live at file scope there. Inside a component method, every
		// const (top-level or component-level) is also a Model field, so
		// emit `m.<name>` for direct field access.
		if gc.Ctx.Component != nil {
			return "m." + name
		}
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
	// Intrinsic dispatch by ID — never by method name — and uniform across the
	// type-method and inlined call shapes. Backends register only the
	// intrinsics they can emit; unregistered IDs fall through to the paths
	// below (and ultimately goBuiltinMethodFromArgs).
	if out, imports, ok := codegen.EmitIntrinsicCall(langGo, n, gc.EvalExpr); ok {
		for _, p := range imports {
			gc.RequireImport(p)
		}
		return out
	}
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
				gc.RequireImport("fmt")
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

		// Component-scope funcs (including computeds) live as methods on
		// Model — call via the model receiver. Without this, an implicit
		// zero-arg call like `text(value=greeting)` lowered to a bare
		// `greeting()` referencing an undefined package-level identifier.
		_, kind := gc.Ctx.Resolve(fname)
		if kind == codegen.NameComputed || kind == codegen.NameFunc {
			return "m." + fname + "(" + strings.Join(args, ", ") + ")"
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
		// Native (e.g. C/cgo) call: emit NativeName(args) directly, ignoring the
		// SNGL import alias that ended up as the receiver.
		if n.Func.NativePkg != "" {
			name := n.Func.NativeName
			// Cgo C-API call: NativeName carries the bare C identifier
			// (e.g. "gtk_label_new"); renderer adds "C." prefix. Backwards-
			// compat: if NativeName already starts with "C." (legacy
			// callers), leave untouched so existing pre-baked NativeNames
			// continue to work during the migration.
			if n.Func.NativePkg == "C" && !strings.HasPrefix(name, "C.") {
				name = "C." + name
			} else if n.Func.NativePkg != "C" {
				// Non-cgo native call (e.g. fmt.Println, time.Now) — record
				// the import so platforms reading gc.Imports() see it.
				gc.RequireImport(n.Func.NativePkg)
			}
			// Context-taking native call: inject the context expression as the
			// first argument. Mirrors legacy translateIRNativeCall — when the
			// importer flagged HasContextArg, supply gc.Ctx.ContextVar (e.g.
			// "r.Context()"), defaulting to context.Background() when unset.
			if n.Func.HasContextArg {
				ctxVar := ""
				if gc.Ctx != nil {
					ctxVar = gc.Ctx.ContextVar
				}
				if ctxVar == "" {
					ctxVar = "context.Background()"
					gc.RequireImport("context")
				}
				args = append([]string{ctxVar}, args...)
			}
			return name + "(" + strings.Join(args, ", ") + ")"
		}

		fname := n.Func.Name
		receiverName := n.Func.Receiver

		qualName := receiverName + "." + fname

		// lower.CreateComponent(comp, props) → m.render<Comp>(propArgs...).
		// Recursive (non-inlinable) user components are left in place by
		// passNoInlineComponents as this intrinsic; the Go backends emit a
		// `render<Comp>` Model method taking the component's props positionally.
		// The prop struct literal's fields are reordered to the component's
		// declared prop order.
		if fname == "CreateComponent" && len(n.Args) == 2 {
			if compIdent, ok := n.Args[0].Value.(*ir.Ident); ok {
				if comp, ok := compIdent.Sym.(*ir.Component); ok {
					recv := gc.Ctx.StateReceiver
					if recv == "" {
						recv = "m"
					}
					var pargs []string
					lit, _ := n.Args[1].Value.(*ir.StructLit)
					for _, p := range comp.Props {
						var val ir.Expr
						if lit != nil {
							for _, f := range lit.Fields {
								if f.Name == p.Name {
									val = f.Value
									break
								}
							}
						}
						switch {
						case val != nil:
							pargs = append(pargs, gc.EvalExpr(val))
						case p.Default != nil:
							pargs = append(pargs, gc.EvalExpr(p.Default))
						default:
							pargs = append(pargs, "nil")
						}
					}
					return recv + "." + ComponentRenderMethod(comp.Name) + "(" + strings.Join(pargs, ", ") + ")"
				}
			}
		}

		// Intrinsic dispatch: stdlib intrinsics that map to per-locale runtime
		// entry points. After NoContext + InlinePure, i18n.* wrapper calls
		// have been lowered to direct intl.* intrinsic calls with the locale
		// threaded as the first arg.
		if result := goEvalIntlIntrinsic(n.Func, args); result != "" {
			// i18n intrinsics emit `i18n.<Func>(...)` which references
			// the sngl-i18n runtime package.
			gc.RequireImport(SnglI18nImportPath)
			return result
		}

		// For i18n.* calls the namespace receiver is the module object, not a
		// value argument. Pass only the real call args to the builtin dispatcher
		// so that a(0) is the first semantic argument (matches type-method path).
		if receiverName == "i18n" {
			if result := goBuiltinMethodFromArgs(qualName, args); result != "" {
				return result
			}
		}

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

	// Typed inline fallback for generic list methods that take a
	// lambda. The proper fix is passNoListLambdas, which expands these
	// calls into hoisted for-loops at the IR level. Callers that bypass
	// Lower (the test runner does, today) still need *some* working
	// emission, so fall back to a typed IIFE here when the IR retains
	// the original filter/map Call shape.
	if (method == "filter" || method == "map") && len(args) >= 2 {
		if expr := gc.evalListLambdaCallFallback(n, method, args); expr != "" {
			return expr
		}
	}

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

	// Component method called on the current component instance:
	// keep the Go-idiomatic `m.<method>(rest)` form. Bubbletea/Fyne
	// emit component method definitions as Model methods, so call sites
	// must dispatch through `m`.
	if gc.Ctx != nil && gc.Ctx.Component != nil && gc.Ctx.Component.Name == receiverName {
		// Method-form call (`v.method()`) threads the receiver as args[0];
		// strip it. A zero-arg computed referenced by name (e.g. `greeting`
		// in an interpolation) carries no receiver arg — still dispatch
		// through `m` rather than lifting to a `MainGreeting()` free func.
		if len(args) >= 1 && args[0] == "m" {
			return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
		}
		return "m." + method + "(" + strings.Join(args, ", ") + ")"
	}

	// User-attached method on a user-defined struct/enum/component type:
	// lift to a free function `ReceiverName + MethodName(args...)`. Without
	// this, static-form calls like `S.helper(5)` and method-form calls like
	// `v.method()` both fall through to the `args[0].method()` shape, which
	// is wrong for static calls (where args[0] is the first explicit arg,
	// not the receiver value).
	if gc.userMethodKnown(receiverName, method) {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(args, ", ") + ")"
	}

	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// evalListLambdaCallFallback emits a typed Go IIFE for `xs.filter(f)` /
// `xs.map(f)` when the IR still carries the original method-call shape.
// passNoListLambdas (a lowering pass enabled for Go targets) expands
// these into explicit for-loops in the IR; this fallback exists for
// code paths that don't run Lower (the in-process test runner).
func (gc *GoIRContext) evalListLambdaCallFallback(n *ir.Call, method string, args []string) string {
	xsExpr := args[0]
	fnExpr := args[1]
	var elemGo string
	if t := n.Args[0].Value.ExprType(); t != nil && t.Kind == ir.TypeList && len(t.Elems) > 0 {
		elemGo = IRTypeToGo(t.Elems[0])
	}
	if elemGo == "" {
		return ""
	}
	switch method {
	case "filter":
		return "func() []" + elemGo + " { var out []" + elemGo + "; for _, item := range " + xsExpr +
			" { if (" + fnExpr + ")(item) { out = append(out, item) } }; return out }()"
	case "map":
		var outGo string
		if n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) > 0 {
			outGo = IRTypeToGo(n.Type.Elems[0])
		}
		if outGo == "" {
			return ""
		}
		return "func() []" + outGo + " { out := make([]" + outGo + ", len(" + xsExpr + ")); for i, item := range " + xsExpr +
			" { out[i] = (" + fnExpr + ")(item) }; return out }()"
	}
	return ""
}

// rawFieldAccess reports whether e is an ident flagged in
// gc.Ctx.RawFieldAccess (nil-safe). Test runners set this for the receiver
// ident (e.g. `c`) so its Select-field accesses bypass ExportName /
// method-getter lowering and touch the unexported Model field directly.
func (gc *GoIRContext) rawFieldAccess(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	return gc.Ctx != nil && gc.Ctx.RawFieldAccess != nil && gc.Ctx.RawFieldAccess[id.Name]
}

// methodField reports whether a field name is flagged in
// gc.Ctx.MethodFields (nil-safe). Such fields, when read off a
// RawFieldAccess recv, lower to a zero-arg method call rather than a raw
// field read.
func (gc *GoIRContext) methodField(field string) bool {
	return gc.Ctx != nil && gc.Ctx.MethodFields != nil && gc.Ctx.MethodFields[field]
}

// uniqueStructWithField returns the Go type name of the sole package
// struct declaring a field with this SNGL field name, or "" if zero
// or multiple structs match. Used to pick a type assertion target
// when reading a field off a `dyn`-typed operand.
func (gc *GoIRContext) uniqueStructWithField(field string) string {
	if gc.Ctx == nil || gc.Ctx.Pkg == nil {
		return ""
	}
	var match *ir.StructDef
	for _, sd := range gc.Ctx.Pkg.Structs {
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
	return ExportName(match.Name)
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
	if n.Type != nil && n.Type.Kind == ir.TypeNative {
		if ref, ok := n.Type.Meta.(ir.NativeTypeRef); ok && ref.CgoC {
			// Cgo pointer cast: (*C.X)(unsafe.Pointer(y))
			// Empty Name → bare unsafe.Pointer(y) (used for void* args).
			gc.RequireImport("unsafe")
			if ref.Name == "" {
				return "unsafe.Pointer(" + gc.EvalExpr(n.Operand) + ")"
			}
			return "(" + IRTypeToGo(n.Type) + ")(unsafe.Pointer(" + gc.EvalExpr(n.Operand) + "))"
		}
		// Go-package native cast: plain type conversion.
		return IRTypeToGo(n.Type) + "(" + gc.EvalExpr(n.Operand) + ")"
	}
	// Temporal conversions from a string literal: route through the
	// parse helpers — Go's `time.Time("...")` cast doesn't compile.
	if n.Type != nil {
		if lit, ok := n.Operand.(*ir.Literal); ok {
			if s, ok2 := LowerTypedLiteralGo(lit, n.Type); ok2 {
				return s
			}
		}
	}
	// `null` flowing into a nillable target (option<T>, ref<T>, map, list,
	// func) — Go's untyped nil takes the field type directly. Emitting
	// `*string(nil)` (which is what plain conversion produces for option
	// targets, because the goType is `*string`) is not valid Go.
	if lit, ok := n.Operand.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull {
		if n.Type != nil {
			switch n.Type.Kind {
			case ir.TypeOption, ir.TypeRef, ir.TypeMap, ir.TypeList, ir.TypeFunc:
				return "nil"
			}
		}
	}
	goType := IRTypeToGo(n.Type)
	operand := gc.EvalExpr(n.Operand)
	// Go's string(int) builds a single-rune string; use fmt.Sprint for numeric
	// and general stringification.
	if n.Type != nil && n.Type.Kind == ir.TypeString {
		gc.RequireImport("fmt")
		return "fmt.Sprint(" + operand + ")"
	}
	// Pointer/composite Go types need parens around the cast target:
	// `*T(x)` is invalid; `(*T)(x)` is the valid form.
	if strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[") || strings.HasPrefix(goType, "map[") {
		return "(" + goType + ")(" + operand + ")"
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

// structLitTypeName picks the Go type prefix for a struct literal. Named
// structs lower to ExportName(sd.Name); anonymous structs (no name on
// the StructDef) materialize an inline `struct { Field Type; ... }` so
// the literal parses where Go would otherwise reject a bare `{…}`.
func structLitTypeName(n *ir.StructLit) string {
	if n == nil {
		return "struct{}"
	}
	// Anonymous literals (`{value="x"}`) keep Def nil but carry the
	// target type via n.Type — extract the StructDef from there so
	// the emitted Go uses the named type the call expects.
	def := n.Def
	if def == nil && n.Type != nil {
		if sd, ok := n.Type.Decl.(*ir.StructDef); ok {
			def = sd
		}
	}
	if def == nil {
		return "struct{}"
	}
	if name := ExportName(def.Name); name != "" {
		return name
	}
	if len(def.Fields) == 0 {
		return "struct{}"
	}
	var fb strings.Builder
	fb.WriteString("struct{ ")
	for i, f := range def.Fields {
		if i > 0 {
			fb.WriteString("; ")
		}
		fb.WriteString(ExportName(f.Name))
		fb.WriteString(" ")
		fb.WriteString(IRTypeToGo(f.Type))
	}
	fb.WriteString(" }")
	return fb.String()
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

// WithLocal returns a new context with an additional local variable.
func (gc *GoIRContext) WithLocal(name string) *GoIRContext {
	return &GoIRContext{
		Ctx:                gc.Ctx.WithLocal(name),
		AlertFunc:          gc.AlertFunc,
		EmitLineDirectives: gc.EmitLineDirectives,
		imports:            gc.imports, // shared so child writes propagate
	}
}

// ForComponent returns a new context scoped to a component.
func (gc *GoIRContext) ForComponent(comp *ir.Component) *GoIRContext {
	return &GoIRContext{
		Ctx:                gc.Ctx.ForComponent(comp),
		AlertFunc:          gc.AlertFunc,
		EmitLineDirectives: gc.EmitLineDirectives,
		imports:            gc.imports, // shared so child writes propagate
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
	case ir.TypeMap:
		if len(t.Elems) == 2 {
			return "map[" + IRTypeToGo(t.Elems[0]) + "]" + IRTypeToGo(t.Elems[1])
		}
		return "map[any]any"
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
			if name := ExportName(sd.Name); name != "" {
				return name
			}
			// Anonymous struct (no source name) — inline the shape so it
			// can appear in a Go param/return/var type position.
			if len(sd.Fields) == 0 {
				return "struct{}"
			}
			var fb strings.Builder
			fb.WriteString("struct{ ")
			for i, f := range sd.Fields {
				if i > 0 {
					fb.WriteString("; ")
				}
				fb.WriteString(ExportName(f.Name))
				fb.WriteString(" ")
				fb.WriteString(IRTypeToGo(f.Type))
			}
			fb.WriteString(" }")
			return fb.String()
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
			if name == "duration" {
				return "time.Duration"
			}
			return ExportName(name)
		}
		return "any"
	case ir.TypeFunc:
		if t.Sig != nil {
			return irFuncSigToGo(t.Sig)
		}
		return "func()"
	case ir.TypeNull:
		return "any"
	case ir.TypeVoid:
		// Used as the return slot of intrinsic-call Funcs (e.g.
		// __renderSlot<N>) emitted by passReactivity. Render as empty so
		// callers building `func name(params) <T>` get `func name(params)`.
		return ""
	case ir.TypeIter, ir.TypeComponent, ir.TypeTypeParam, ir.TypeInvalid:
		// Iter/component/typeparam/invalid have no first-class Go
		// representation in emitted code; falling back to `any` matches
		// the previous behavior. Listed explicitly so the default arm
		// can catch genuinely new TypeKinds.
		return "any"
	case ir.TypeNative:
		if ref, ok := t.Meta.(ir.NativeTypeRef); ok {
			if ref.CgoC {
				return "*C." + ref.Name
			}
			return "*" + ref.Name
		}
		return "unsafe.Pointer"
	default:
		panic(fmt.Sprintf("IRTypeToGo: unhandled ir.TypeKind %v", t.Kind))
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
		case ir.TypeUnit:
			if out, ok := LowerUnitLiteralGo(n); ok {
				return out
			}
			return n.Raw
		case ir.TypeDate, ir.TypeTime, ir.TypeDateTime:
			if out, ok := LowerTimeLiteralGo(n); ok {
				return out
			}
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
	case *ir.MapLitIR:
		keyType := "any"
		valType := "any"
		if n.Type != nil && n.Type.Kind == ir.TypeMap && len(n.Type.Elems) == 2 {
			keyType = IRTypeToGo(n.Type.Elems[0])
			valType = IRTypeToGo(n.Type.Elems[1])
		}
		var parts []string
		for _, e := range n.Entries {
			parts = append(parts, IRLiteralToGo(e.Key)+": "+IRLiteralToGo(e.Value))
		}
		return "map[" + keyType + "]" + valType + "{" + strings.Join(parts, ", ") + "}"
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
	// IRLiteralToGo is called from places that may pass non-literal
	// exprs (e.g. component prop defaults that aren't literals). Returning
	// "" here is a deliberate best-effort fallback rather than an
	// exhaustive-case-missing bug, so we don't panic.
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

// EmitFuncDef renders a complete Go function definition from an *ir.Func.
// Includes the receiver clause (for Model methods), param list, return
// type, and body. Body statements flow through EvalStmt — Synthesized
// idents resolve to m.<name>, native funcs to C.<NativeName>, etc.
//
// Returns the source as a slice of lines (each line WITHOUT trailing
// newline). The caller joins with "\n" or writes each line followed by
// its own newline.
func (gc *GoIRContext) EmitFuncDef(fn *ir.Func) []string {
	var lines []string

	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name + " " + IRTypeToGo(p.Type)
	}
	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
		// TypeDyn without a raw-type hint (Meta) has no first-class Go
		// representation — skip emission so the signature reads "func foo()"
		// rather than "func foo() any". TypeDyn WITH Meta is a raw Go
		// type ref (e.g. "fyne.CanvasObject") and must round-trip.
		if fn.Return.Kind == ir.TypeDyn {
			if meta, ok := fn.Return.Meta.(string); ok && meta != "" {
				retType = " " + meta
			}
		} else {
			retType = " " + IRTypeToGo(fn.Return)
		}
	}

	sig := "func "
	if fn.Receiver != "" {
		sig += "(m *" + fn.Receiver + ") "
	}
	sig += fn.Name + "(" + strings.Join(params, ", ") + ")" + retType + " {"
	lines = append(lines, sig)

	bodyGC := gc
	for _, p := range fn.Params {
		bodyGC = bodyGC.WithLocal(p.Name)
	}
	for _, stmt := range fn.Block {
		for _, line := range bodyGC.EvalStmt(stmt) {
			lines = append(lines, "\t"+line)
		}
	}

	lines = append(lines, "}")
	return lines
}
