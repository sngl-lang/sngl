package golang

import (
	"fmt"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/irwalk"
	"git.duckfam.us/jonathan/sngl/ir"
)

// SNGL's built-in `color` maps to the shared pkg/go/snglcolor.Color struct
// (int channels), so both string contexts (via Color.String()) and canvas
// contexts (reading .R/.G/.B/.A) work off one type. colorImportPath is
// registered on demand at the emit sites that actually reference the type:
// StructLit (a color value) and the canvas emitters (CanvasStyle decls).
const (
	colorImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/snglcolor"
	colorGoType     = "snglcolor.Color"
)

// GoIRContext translates IR expressions and statements into Go code.
type GoIRContext struct {
	Ctx *codegen.ExprCtx

	// AlertFunc translates Alert.toast/info/warn/error calls.
	// If nil, a default "m.toasts = append(...)" implementation is used.
	AlertFunc func(ctx *GoIRContext, method string, args []ir.CallArg) []string

	// FreeFuncScope makes calls to component/window-scoped user funcs and
	// computeds render as free, exported package-level functions
	// (`Fib(n-1)`) rather than Model-receiver methods (`m.fib(n-1)`). Set by
	// hosts that emit user funcs as free functions in a separate package —
	// today only the android `go { android() }` go-lib (gomobile bind)
	// module. Without it a recursive or mutually-recursive go-lib func
	// renders its call with an `m.` receiver that has no binding in the free
	// function, producing Go that does not compile.
	FreeFuncScope bool

	// MethodRecvType, when set, emits EmitFuncDef's output as a method on that
	// Go type rather than as a free function or a Model method.
	MethodRecvType string

	// EmitLineDirectives prepends `//line file:line` at statement boundaries,
	// so the Go compiler attributes errors back to the SNGL source.
	EmitLineDirectives bool

	// LineDirBase is the directory the emitted .go file will live in. gc
	// resolves a relative `//line` path against the directory of the file
	// carrying the directive, not the build's working directory. Empty
	// leaves the path as the compiler saw it.
	LineDirBase string

	// imports is shared across forked contexts (WithLocal, ForComponent), so a
	// child contributes to the parent's set.
	imports *importSet
}

// importSet is keyed by import path; the bool tracks whether the import is
// blank ("_").
type importSet struct {
	paths   map[string]bool // path → blank?
	order   []string
	aliases map[string]string // path → forced alias (overrides goAliasFor)
}

func newImportSet() *importSet {
	return &importSet{paths: map[string]bool{}, aliases: map[string]string{}}
}

func NewIRContext(ctx *codegen.ExprCtx) *GoIRContext {
	gc := &GoIRContext{Ctx: ctx, imports: newImportSet()}
	if ctx != nil {
		gc.EmitLineDirectives = ctx.Maps
		gc.LineDirBase = ctx.OutDir
	}
	return gc
}

// RequireImport is safe to call repeatedly; first insertion wins for ordering.
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

// RequireImportAs forces an explicit alias, for when the conventional one
// would collide and the call sites reference a fixed selector.
//
// A forced alias must be unique across paths: call sites qualify with the
// literal alias, so renderImports cannot suffix it to de-conflict without
// dangling the reference. Two paths forced to the same alias therefore panic.
// Re-forcing the same (path, alias) is idempotent.
func (gc *GoIRContext) RequireImportAs(path, alias string) {
	if path == "" || gc.imports == nil {
		return
	}
	gc.RequireImport(path)
	if gc.imports.aliases == nil || alias == "" {
		return
	}
	for p, a := range gc.imports.aliases {
		if a == alias && p != path {
			panic(fmt.Sprintf("golang: forced import alias %q claimed by two paths %q and %q", alias, p, path))
		}
	}
	gc.imports.aliases[path] = alias
}

// AliasFor registers path and returns the alias its symbols must be qualified
// with. A caller spelling the selector itself would be guessing at a Go
// package name that lives in the module's source, not in its path, and could
// not know what another import already claimed.
//
// Assignment happens in registration order, which is the walk order, so the
// result is stable across runs of one build; nothing depends on map iteration.
func (gc *GoIRContext) AliasFor(path string) string {
	if path == "" || path == "C" || gc.imports == nil {
		return ""
	}
	gc.RequireImport(path)
	if a := gc.imports.aliases[path]; a != "" {
		return a
	}
	if gc.imports.aliases == nil {
		gc.imports.aliases = map[string]string{}
	}
	taken := make(map[string]bool, len(gc.imports.aliases))
	for _, a := range gc.imports.aliases {
		taken[a] = true
	}
	base := goAliasFor(path)
	alias := base
	for i := 2; taken[alias]; i++ {
		alias = fmt.Sprintf("%s%d", base, i)
	}
	gc.imports.aliases[path] = alias
	return alias
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

func (gc *GoIRContext) EvalExpr(e ir.Expr) string { return irwalk.EvalExpr(gc, e) }

func (gc *GoIRContext) EvalStmt(s ir.Stmt) []string { return irwalk.EvalStmt(gc, s) }

func (gc *GoIRContext) NilExpr() string              { return "nil" }
func (gc *GoIRContext) Literal(n *ir.Literal) string { return gc.evalLiteral(n) }
func (gc *GoIRContext) Ident(n *ir.Ident) string     { return gc.evalIdent(n) }

func (gc *GoIRContext) Binary(n *ir.Binary, left, right string) string {
	if out, ok := goMultiBaseUnitBinary(n, left, right); ok {
		return out
	}
	return "(" + left + " " + irBinaryOp(n.Op) + " " + right + ")"
}

// goMultiBaseUnitBinary expands a binary op over each base field of a
// multi-base unit struct, projecting a unit operand's field and using a scalar
// one verbatim. Returns ("", false) when neither operand is such a unit.
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
	// A Select on a RawFieldAccess ident reads the unexported field directly,
	// so a generated `_test.go` in the same package can touch Model fields.
	if gc.rawFieldAccess(n.Operand) {
		// A MethodFields field is surfaced by platform codegen as a zero-arg
		// method, so the raw read lowers to a method call.
		if gc.methodField(n.Field) {
			return operand + "." + n.Field + "()"
		}
		return operand + "." + n.Field
	}
	// `c.<id>.<prop>` → `c.<id><Prop>()`, the getter platforms emit per
	// (id, prop) binding. Triggered by the outer Select's operand being a
	// Select on a RawFieldAccess Ident.
	if inner, ok := n.Operand.(*ir.Select); ok {
		if id, ok := inner.Operand.(*ir.Ident); ok && gc.rawFieldAccess(id) {
			return fmt.Sprintf("%s.%s%s()", id.Name, inner.Field, ExportName(n.Field))
		}
	}
	// `c.<id>[idx].<prop>` → `c.<id>()[idx].<Prop>()`, for an <id> inside a
	// `for` loop, whose per-iteration widgets come back from `<id>()`.
	if idxExpr, ok := n.Operand.(*ir.Index); ok {
		if inner, ok := idxExpr.Operand.(*ir.Select); ok {
			if id, ok := inner.Operand.(*ir.Ident); ok && gc.rawFieldAccess(id) && gc.methodField(inner.Field) {
				return fmt.Sprintf("%s.%s()[%s].%s()", id.Name, inner.Field, gc.EvalExpr(idxExpr.Idx), ExportName(n.Field))
			}
		}
	}
	// Go's `any` has no fields, so a bare `.<F>` on a `dyn` operand will not
	// compile; when exactly one user struct declares the field, assert to it.
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
	if isColorStructLit(n) {
		gc.RequireImport(colorImportPath)
	}
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
	switch n.IterKind {
	case ir.IterMapEntries:
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, valueVar, iter)
	case ir.IterIndexed:
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, n.Value, iter)
	default:
		return fmt.Sprintf("for _, %s := range %s {", n.Key, iter)
	}
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
func (gc *GoIRContext) MutTargetField(n *ir.Select) string { return ExportName(n.Field) }

func (gc *GoIRContext) StmtPrefix(s ir.Stmt) []string {
	if !gc.EmitLineDirectives {
		return nil
	}
	pos := ir.StmtPos(s)
	if !pos.IsValid() || pos.File == "" {
		return nil
	}
	return []string{fmt.Sprintf("//line %s:%d", lineDirPath(pos.File, gc.LineDirBase), pos.Line)}
}

// lineDirPath rewrites a SNGL source path to resolve from base, the
// directory the emitted .go file lands in. Relative rather than absolute so
// golden output does not vary per machine; anything Rel cannot express is
// left alone.
func lineDirPath(file, base string) string {
	if base == "" {
		return file
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return file
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return file
	}
	rel, err := filepath.Rel(absBase, absFile)
	if err != nil {
		return file
	}
	return rel
}

func (gc *GoIRContext) Scoped(name string) irwalk.Renderer { return gc.WithLocal(name) }

func (gc *GoIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Value)
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return n.Value
	case ir.TypeNull:
		return "nil"
	case ir.TypeUnit:
		if out, ok := LowerUnitLiteralGo(n); ok {
			return out
		}
		return n.Value
	case ir.TypeStruct:
		if out, ok := LowerTimeLiteralGo(n); ok {
			return out
		}
		if ir.StringReprStruct(n.Type) {
			return fmt.Sprintf("%q", n.Value)
		}
		return n.Value
	default:
		return n.Value
	}
}

func (gc *GoIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}

	// passNoImplicitRecv's synthesized receiver renders as the Model's `m`.
	if _, ok := n.Sym.(*ir.Component); ok {
		return gc.recvName()
	}

	name := n.Name
	// A synthesized element ref is stored as a Model struct field, so it must
	// be qualified: a bare ident would not resolve in the method scope.
	if n.IsElementRef && n.Synthesized {
		return "m." + name
	}
	_, kind := gc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return gc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return gc.recvName() + "." + name + "()"
	case codegen.NameStateVar:
		if gc.Ctx.StateReceiver != "" {
			return gc.Ctx.StateReceiver + "." + ExportName(name)
		}
		return "m." + name
	case codegen.NameConst:
		// A const is a file-scope name in a free function and a Model field
		// inside a component method.
		if gc.Ctx.Component != nil {
			return "m." + name
		}
		return name
	case codegen.NameFunc:
		// Named as a value -- `xs.filter(keep)` -- this is a Go method value,
		// which carries its receiver and matches the callback's signature.
		return gc.recvName() + "." + ExportName(name)
	case codegen.NameExternFunc, codegen.NameExternVar:
		return "m." + ExportName(name)
	default:
		if n.Type != nil && n.Type.Kind == ir.TypeEnum {
			return fmt.Sprintf("%q", name)
		}
		return name
	}
}

// maybeWrapErrorReturn wraps a native call whose imported signature is
// (T, error), which would otherwise be a 2-value RHS against a 1-value LHS and
// fail to compile. The Go importer records this as `HasErrorReturn`.
//
// The wrap is an IIFE rather than a top-level helper, to avoid threading
// "needs helper" plumbing through every platform's emit pipeline. Errors are
// silently discarded; http.go's `nativeMustOK` logs them instead.
func (gc *GoIRContext) maybeWrapErrorReturn(n *ir.Call, raw string) string {
	if n.Func == nil || !n.Func.HasErrorReturn || n.Func.Return == nil {
		return raw
	}
	rt := IRTypeToGo(n.Func.Return)
	return fmt.Sprintf("func() %s { v, _ := %s; return v }()", rt, raw)
}

func (gc *GoIRContext) evalCall(n *ir.Call) string {
	// Dispatch by intrinsic ID, never by method name. An unregistered ID falls
	// through to the paths below.
	if out, imports, ok := codegen.EmitIntrinsicCall(langGo, n, gc.EvalExpr); ok {
		for _, p := range imports {
			gc.RequireImport(p)
		}
		return out
	}
	if n.Receiver != nil {
		return gc.evalNamespaceCall(n)
	}

	if n.Func != nil && n.Func.Receiver != "" {
		return gc.evalTypeMethodCall(n)
	}

	if n.Func != nil {
		fname := n.Func.Name
		args := gc.evalCallArgs(n.Args)

		// size(x) is a length builtin, not a type conversion; the primitive
		// casts arrive as ir.Conversion instead.
		if fname == "size" && len(args) == 1 {
			return "len(" + args[0] + ")"
		}

		// A component-scope func is a method on Model, so it must be called
		// through the receiver or it references an undefined identifier.
		_, kind := gc.Ctx.Resolve(fname)
		if kind == codegen.NameComputed || kind == codegen.NameFunc {
			// A free-function scope has no Model receiver, so the call uses
			// the exported name. A func not emitted into the lib then yields
			// a clean "undefined" Go error rather than silent bad code.
			if gc.FreeFuncScope {
				return ExportName(fname) + "(" + strings.Join(args, ", ") + ")"
			}
			// Route mode emits these as State methods under their exported
			// name, which is also what a reference to one as a value renders
			// (NameFunc below); the Model path keeps the name as written.
			if gc.Ctx != nil && gc.Ctx.StateReceiver != "" {
				return gc.recvName() + "." + ExportName(fname) + "(" + strings.Join(args, ", ") + ")"
			}
			return gc.recvName() + "." + fname + "(" + strings.Join(args, ", ") + ")"
		}

		codegen.RequireIntrinsicFallback(langGo, n.Func)
		return fname + "(" + strings.Join(args, ", ") + ")"
	}

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
		// A native call emits the native name, ignoring the SNGL import alias
		// that ended up as the receiver. #[foreign] is excluded: it names a
		// declaration this file also emits, under that declaration's name.
		if n.Func.Foreign.Path != "" && !n.Func.Foreign.Marked {
			name := n.Func.Foreign.Name
			// The renderer adds the "C." prefix to a bare C identifier; a
			// caller that already prefixed it keeps what it wrote.
			if n.Func.Foreign.Path == "C" && !strings.HasPrefix(name, "C.") {
				name = "C." + name
			} else if n.Func.Foreign.Path != "C" {
				gc.RequireImport(n.Func.Foreign.Path)
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

		// lower.CreateComponent(comp, props) → m.render<Comp>(propArgs...),
		// with the prop literal's fields reordered to the component's
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

		// An i18n.* namespace receiver is the module object, not a value
		// argument, so a(0) must be the first semantic argument.
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

		codegen.RequireIntrinsicFallback(langGo, n.Func)
		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}

	return receiver + "(" + strings.Join(args, ", ") + ")"
}

func (gc *GoIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.Receiver
	qualName := receiverName + "." + method

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

	// Go allows no methods on int/float/string/bool, so a user-attached method
	// on one lifts to a free `TypeNameMethodName` function.
	if isPrimitiveTypeName(receiverName) && gc.userMethodKnown(receiverName, method) {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(args, ", ") + ")"
	}

	// Bubbletea/Fyne emit component methods as Model methods, so a call on the
	// current instance must dispatch through `m`.
	if gc.Ctx != nil && gc.Ctx.Component != nil && gc.Ctx.Component.Name == receiverName {
		// A method-form call threads the receiver as args[0]; a zero-arg
		// computed referenced by name carries none, and still dispatches
		// through `m` rather than lifting to a free func.
		name := method
		if gc.Ctx != nil && gc.Ctx.StateReceiver != "" {
			name = ExportName(method)
		}
		if len(args) >= 1 && args[0] == gc.recvName() {
			return args[0] + "." + name + "(" + strings.Join(args[1:], ", ") + ")"
		}
		return gc.recvName() + "." + name + "(" + strings.Join(args, ", ") + ")"
	}

	// Lifted to a free `ReceiverName + MethodName(args...)`: otherwise a
	// static-form call like `S.helper(5)` falls through to `args[0].method()`,
	// where args[0] is the first explicit arg rather than a receiver.
	if gc.userMethodKnown(receiverName, method) {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(args, ", ") + ")"
	}

	codegen.RequireIntrinsicFallback(langGo, n.Func)
	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// rawFieldAccess reports whether e is an ident flagged in gc.Ctx.RawFieldAccess
// (nil-safe). A test runner sets it for the receiver ident so field accesses
// bypass ExportName / method-getter lowering.
func (gc *GoIRContext) rawFieldAccess(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	return gc.Ctx != nil && gc.Ctx.RawFieldAccess != nil && gc.Ctx.RawFieldAccess[id.Name]
}

// methodField reports whether a field name is flagged in gc.Ctx.MethodFields
// (nil-safe); such a field lowers to a zero-arg method call.
func (gc *GoIRContext) methodField(field string) bool {
	return gc.Ctx != nil && gc.Ctx.MethodFields != nil && gc.Ctx.MethodFields[field]
}

// uniqueStructWithField returns the Go type name of the sole package struct
// declaring this field, or "" if zero or several do.
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

// userMethodKnown decides whether a primitive-receiver call is emitted as a
// free function.
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
// handler effect analysis resolved. Only error.raise is recognised; anything
// else returns nil and the caller falls back to the expression path.
func (gc *GoIRContext) evalErrorAwareCall(call *ir.Call) []string {
	if call == nil || call.Func == nil {
		return nil
	}
	if !ir.IsErrorRaiseFunc(call.Func) {
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
		// ErrorBubble wants a fallible-signature return channel, which no
		// lowering produces yet, so this panics and — with no recover
		// emitted either — aborts. Put error.raise inside the handler.
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

// emitHandlerInvoke declares the event variable and inlines the handler body,
// wrapped in a Go lexical block so the variable does not leak.
//
// It emits no return, so statements following a raise still execute: place
// error.raise at the end of a handler body.
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

func (gc *GoIRContext) evalAlertCall(method string, args []ir.CallArg) string {
	if gc.AlertFunc != nil {
		return strings.Join(gc.AlertFunc(gc, method, args), "; ")
	}
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
	if ir.IsNullToFuncConv(n) {
		return nullFuncStubGo(n.Type)
	}
	if n.Type != nil && n.Type.Kind == ir.TypeNative {
		if ref, ok := n.Type.Meta.(ir.NativeTypeRef); ok && ref.CgoC {
			// An empty Name gives a bare unsafe.Pointer(y), for void* args.
			gc.RequireImport("unsafe")
			if ref.Name == "" {
				return "unsafe.Pointer(" + gc.EvalExpr(n.Operand) + ")"
			}
			return "(" + IRTypeToGo(n.Type) + ")(unsafe.Pointer(" + gc.EvalExpr(n.Operand) + "))"
		}
		return IRTypeToGo(n.Type) + "(" + gc.EvalExpr(n.Operand) + ")"
	}
	// Go's `time.Time("...")` cast does not compile, so route a temporal
	// conversion through the parse helpers.
	if n.Type != nil {
		if lit, ok := n.Operand.(*ir.Literal); ok {
			if s, ok2 := LowerTypedLiteralGo(lit, n.Type); ok2 {
				return s
			}
		}
	}
	// `null` into a nillable target takes Go's untyped nil directly: plain
	// conversion would give `*string(nil)`, which is not valid Go.
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
	// Go's string(int) builds a single-rune string.
	if n.Type != nil && n.Type.Kind == ir.TypeString {
		gc.RequireImport("fmt")
		return "fmt.Sprint(" + operand + ")"
	}
	// `*T(x)` is invalid; `(*T)(x)` is the valid form.
	if strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[") || strings.HasPrefix(goType, "map[") {
		return "(" + goType + ")(" + operand + ")"
	}
	return goType + "(" + operand + ")"
}

// nullFuncStubGo is a callable substitute for a null func: it returns the
// declared return type's zero value.
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

// isColorStructLit reports whether a struct literal is a `color{r,g,b,a}`
// (including the lowered form of a #rrggbb literal), by type or by Def name.
func isColorStructLit(n *ir.StructLit) bool {
	if n == nil {
		return false
	}
	if ir.IsColorStruct(n.Type) {
		return true
	}
	return n.Def != nil && n.Def.Name == "color"
}

// structLitTypeName picks the Go type prefix for a struct literal. An
// anonymous struct materializes an inline `struct { … }`, since Go rejects a
// bare `{…}` literal.
func structLitTypeName(n *ir.StructLit) string {
	if n == nil {
		return "struct{}"
	}
	// An anonymous literal keeps Def nil but carries the target type in
	// n.Type, which is where the named type the call expects comes from.
	def := n.Def
	if def == nil && n.Type != nil {
		if sd, ok := n.Type.Decl.(*ir.StructDef); ok {
			def = sd
		}
	}
	if def == nil {
		return "struct{}"
	}
	if def.Name == "color" || ir.IsColorStruct(n.Type) {
		return colorGoType
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

	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := gc.EvalExpr(ret.Value)
			return "func(" + strings.Join(params, ", ") + ") " + retType + " { return " + body + " }"
		}
	}

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

func (gc *GoIRContext) WithLocal(name string) *GoIRContext {
	return &GoIRContext{
		Ctx:                gc.Ctx.WithLocal(name),
		AlertFunc:          gc.AlertFunc,
		FreeFuncScope:      gc.FreeFuncScope,
		EmitLineDirectives: gc.EmitLineDirectives,
		LineDirBase:        gc.LineDirBase,
		imports:            gc.imports, // shared so child writes propagate
	}
}

func (gc *GoIRContext) ForComponent(comp *ir.Component) *GoIRContext {
	return &GoIRContext{
		Ctx:                gc.Ctx.ForComponent(comp),
		AlertFunc:          gc.AlertFunc,
		FreeFuncScope:      gc.FreeFuncScope,
		EmitLineDirectives: gc.EmitLineDirectives,
		LineDirBase:        gc.LineDirBase,
		imports:            gc.imports, // shared so child writes propagate
	}
}

// IRTypeToGo converts an IR type to a Go type string.
func IRTypeToGo(t *ir.Type) string {
	if t == nil {
		return "any"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "bool"
	case ir.TypeInt:
		if t.Bits != 0 {
			if t.Unsigned {
				return fmt.Sprintf("uint%d", t.Bits)
			}
			return fmt.Sprintf("int%d", t.Bits)
		}
		return "int"
	case ir.TypeFloat:
		if t.Bits != 0 {
			return fmt.Sprintf("float%d", t.Bits)
		}
		return "float64"
	case ir.TypeString:
		return "string"
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
		// date/time/datetime are stdlib structs mapping to time.Time; detect
		// them by name before the generic struct path.
		if ir.IsDateStruct(t) || ir.IsTimeStruct(t) || ir.IsDateTimeStruct(t) {
			return "time.Time"
		}
		if ir.IsColorStruct(t) {
			return colorGoType
		}
		if sd, ok := t.Decl.(*ir.StructDef); ok {
			// A mark names a type this file also declares, so using the mark's
			// name here would leave two names for one type and no import.
			if sd.Foreign.Name != "" && !sd.Foreign.Marked {
				return sd.Foreign.Name
			}
			if name := ExportName(sd.Name); name != "" {
				return name
			}
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
		// Rendered empty so a caller building `func name(params) <T>` gets
		// `func name(params)`.
		return ""
	case ir.TypeIter, ir.TypeComponent, ir.TypeTypeParam, ir.TypeInvalid:
		// These have no first-class Go representation, and fall back to `any`.
		// Listed explicitly so the default arm catches a new TypeKind.
		return "any"
	case ir.TypeNative:
		if ref, ok := t.Meta.(ir.NativeTypeRef); ok {
			if ref.Bare {
				return ref.Name
			}
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

func IRLiteralToGo(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	switch n := e.(type) {
	case *ir.Conversion:
		// A zero-value callable lambda, so a call through the var returns the
		// return type's zero instead of panicking on a nil func.
		if ir.IsNullToFuncConv(n) {
			return nullFuncStubGo(n.Type)
		}
		return IRLiteralToGo(n.Operand)
	case *ir.Literal:
		if n.Type == nil {
			return n.Value
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Value)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Value
		case ir.TypeNull:
			return "nil"
		case ir.TypeUnit:
			if out, ok := LowerUnitLiteralGo(n); ok {
				return out
			}
			return n.Value
		case ir.TypeStruct:
			if out, ok := LowerTimeLiteralGo(n); ok {
				return out
			}
			if ir.StringReprStruct(n.Type) {
				return fmt.Sprintf("%q", n.Value)
			}
			return n.Value
		default:
			return n.Value
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
		if isColorStructLit(n) {
			name = colorGoType
		} else if n.Def != nil {
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
	// Callers may pass a non-literal expr (a component prop default), so ""
	// is a deliberate fallback rather than a missing case.
	return `""`
}

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

func irBinaryOp(op ast.BinaryOp) string {
	return binaryOpStr(op)
}

func irAssignOp(op ast.AssignOp) string {
	return assignOpStr(op)
}

// EmitFuncDef renders a complete Go function definition from an *ir.Func.
// Includes the receiver clause (for Model methods), param list, return
// type, and body. Body statements flow through EvalStmt — Synthesized
// idents resolve to m.<name>, native funcs to their C identifier, etc.
//
// Returns the source as a slice of lines (each line WITHOUT trailing
// newline). The caller joins with "\n" or writes each line followed by
// its own newline.
// recvName is the identifier a component's own members dispatch through. It
// is the Model receiver `m` in every host that emits a Model, and the
// per-request State value in html's route mode, which has none: there the
// component's funcs are methods on State and its vars are State fields, so
// one name answers for both.
func (gc *GoIRContext) recvName() string {
	if gc.Ctx != nil && gc.Ctx.StateReceiver != "" {
		return gc.Ctx.StateReceiver
	}
	return "m"
}

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
	switch {
	case gc.MethodRecvType != "":
		// Emitted as a method on a type the host names, with the receiver the
		// rest of this context dispatches through. html's route mode uses it
		// to put a component's funcs on the per-request State, which is the
		// only receiver that file has.
		sig += "(" + gc.recvName() + " *" + gc.MethodRecvType + ") "
		if len(params) > 0 && fn.Receiver != "" {
			// The component receiver arrives as an ordinary first parameter
			// (passNoImplicitRecv). As a Go method it is the receiver, so it
			// is not also an argument.
			params = params[1:]
		}
		sig += fn.Name + "(" + strings.Join(params, ", ") + ")" + retType + " {"
		lines = append(lines, sig)
		return gc.emitFuncBody(lines, fn, params)
	case fn.Receiver != "":
		sig += "(m *" + fn.Receiver + ") "
	}
	sig += fn.Name + "(" + strings.Join(params, ", ") + ")" + retType + " {"
	lines = append(lines, sig)

	return gc.emitFuncBody(lines, fn, params)
}

// emitFuncBody appends a function's statements and its closing brace.
func (gc *GoIRContext) emitFuncBody(lines []string, fn *ir.Func, _ []string) []string {
	bodyGC := gc
	for _, p := range fn.Params {
		bodyGC = bodyGC.WithLocal(p.Name)
	}
	for _, stmt := range fn.Block {
		for _, line := range bodyGC.EvalStmt(stmt) {
			lines = append(lines, "\t"+line)
		}
	}
	return append(lines, "}")
}
