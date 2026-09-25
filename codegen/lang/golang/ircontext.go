package golang

import (
	"fmt"
	"path/filepath"
	"slices"
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

	// InstanceRecords says the host emits a component the build could not
	// inline as a record with a ctor of its own, rather than as a render
	// method on its Model. Set by the platform, because nothing in the IR
	// distinguishes the two: ir.Component.RuntimeInstance is stamped by the
	// inliner for every target, and whether that becomes a record is the
	// target's answer (see lower.hasInstanceRuntime).
	InstanceRecords bool

	// ModelIsValue says the Go scope this context emits into holds the Model
	// as a value rather than a pointer, so handing it to something that takes
	// a `*Model` needs its address. bubbletea's hand-written receivers are
	// `func (m Model)` -- Update returns the mutated copy -- while everything
	// EmitFuncDef writes takes a pointer, which is why it clears this for the
	// bodies it emits.
	ModelIsValue bool

	// ExtraParam is appended verbatim to the signature EmitFuncDef writes, for
	// a parameter whose Go type no ir.Type spells -- today the `m *Model` a
	// lifted type method takes. Cleared for the body, which is a scope of its
	// own.
	ExtraParam string

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
	op := n.Op.String()

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
	// Select on a RawFieldAccess Ident: `c.out` is an element ref and
	// `.value` is a prop of the element it names.
	//
	// Not when the inner reads a struct: `c.state.entry` is a field of a
	// value the component holds, and it rendered as the getter
	// `c.stateEntry()`, which is nobody's method.
	if inner, ok := n.Operand.(*ir.Select); ok {
		if id, ok := inner.Operand.(*ir.Ident); ok && gc.rawFieldAccess(id) && !isStructExpr(inner) {
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
	if field, ok := gc.modelField(n); ok {
		return operand + "." + field
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
	// A literal of a type the runtime defines is the only mention of it that
	// need not be reached through a call to that runtime, so it is the one
	// place the import can be missing.
	if ir.IsRemoteHTTPResultStruct(n.Type) {
		gc.RequireImport(remoteHTTPImportPath)
	}
	// A channel has no composite literal either, and for the same reason: the
	// only one that reaches here is the zero the checker synthesizes for an
	// uninitialised `var done go.chan<bool>`, and a channel's zero is nil.
	// `make` is what produces a usable one, which is go.makechan.
	if isChanLit(n) {
		return "nil"
	}
	gc.requireForeignStructImport(n)
	name := structLitTypeName(n)
	// A native type whose host spelling is a pointer has no composite literal:
	// `*time.Ticker{}` does not parse. Only the empty literal can reach here
	// for one -- it is the zero the checker synthesizes for `var t Ticker`,
	// since a program cannot build a value of a type it only names -- and nil
	// is what that zero is.
	//
	// The panic is the other half of that sentence. If a populated one ever
	// arrives the invariant has changed, and falling through would emit Go
	// that does not parse rather than say so.
	if strings.HasPrefix(name, "*") {
		if len(n.Fields) > 0 {
			panic("golang: composite literal for pointer-spelled native type " + name)
		}
		return "nil"
	}
	parts := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		if f.Spread {
			panic("golang: struct spread must be lowered by flatten_struct_spread")
		}
		parts[i] = ExportName(f.Name) + ": " + fieldStrs[i]
	}
	return name + "{" + strings.Join(parts, ", ") + "}"
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
	if lines, ok := gc.selectLines(n); ok {
		return lines
	}
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

// LocalVarText declares a local. A **numeric** one is declared with its type
// written out rather than left to `:=`, because that is where Go's answer and
// the program's can differ: `:=` takes the *default type* of an untyped
// constant, which is `int` for a whole number and `float64` for a fractional
// one, whatever the declaration said.
//
//	var acc float32 = 1.0   ->  acc := 1.0    // a float64; the next line,
//	                                          // adding a float32, does not compile
//	var acc float  = 2.0    ->  acc := 2      // an int, for the same reason
//
// Every other kind is left inferred, and not out of caution: a string, a bool,
// a struct, a slice each have exactly one Go type that `:=` reads off a
// correctly typed initialiser, so an annotation would restate it. It would
// also do harm -- gtk4 emits twice and keeps the run that mentions no `C.`
// symbol, so spelling a widget handle's type is what tips a program out of its
// cgo-free mode.
func (gc *GoIRContext) LocalVarText(n *ir.LocalVar, initStr string) string {
	goType := "any"
	if n.Type != nil {
		goType = IRTypeToGo(n.Type)
	}
	// `x := nil` is not Go -- an untyped nil has no type to infer one from --
	// and the declared form says the same thing, since nil is the zero of
	// every type that can hold it. The reconcile a reactive slot performs
	// opens each position that way: `var <id> <Comp> = null`, then one branch
	// or the other binds it.
	if n.Init == nil || initStr == "nil" {
		return "var " + n.Name + " " + goType
	}
	if n.Type != nil && n.Type.IsNumeric() {
		return "var " + n.Name + " " + goType + " = " + initStr
	}
	return n.Name + " := " + initStr
}
func (gc *GoIRContext) ReturnText(n *ir.Return, valueStr string) string {
	if n.Value != nil {
		return "return " + valueStr
	}
	return "return"
}

func (gc *GoIRContext) ForHead(n *ir.For, iter string) string {
	// A loop that declared no variable still needs a counter where Go has no
	// discard for one: `for range xs` covers the element and map forms, but a
	// counted loop counts, so it names a variable the condition reads -- which
	// is also what keeps Go from calling it unused.
	n = WithUnreadVarsDropped(n)
	key := n.Key
	if key == "" {
		key = "__i"
	}
	switch n.IterKind {
	case ir.IterForever:
		// Go's own spelling: `for {` is the condition form below with the
		// condition left out.
		return "for {"
	case ir.IterCondition:
		return "for " + iter + " {"
	case ir.IterCounted:
		c := n.Counted
		start, end := gc.EvalExpr(c.Start), gc.EvalExpr(c.End)
		// `seq.count(n)` -- from zero, by one -- is Go's range over an int
		// (1.22), which counts the same way including the empty case for a
		// bound of zero or less. No temp: range evaluates its operand once.
		if n.Value == "" && c.Step == 1 && start == "0" {
			if key == "__i" {
				return fmt.Sprintf("for range %s {", end)
			}
			return fmt.Sprintf("for %s := range %s {", key, end)
		}
		cmp, step := "<", fmt.Sprintf(" += %d", c.Step)
		switch {
		case c.Step == 1:
			step = "++"
		case c.Step < 0:
			cmp, step = ">", fmt.Sprintf(" -= %d", -c.Step)
		}
		if n.Value != "" {
			// Two variables: Key is the ordinal, Value the number. Both are
			// counters, so the ordinal costs an increment rather than the
			// sequence it would otherwise be indexing into. Go has no
			// compound assignment in a two-name post statement, so the step
			// is spelled as the sum it is.
			next := fmt.Sprintf("%s + %d", n.Value, c.Step)
			if c.Step < 0 {
				next = fmt.Sprintf("%s - %d", n.Value, -c.Step)
			}
			return fmt.Sprintf("for %s, %s, __end := 0, %s, %s; %s %s __end; %s, %s = %s+1, %s {",
				n.Key, n.Value, start, end, n.Value, cmp, n.Key, n.Value, n.Key, next)
		}
		// The end bound is bound to a temp in the init clause: it is an
		// arbitrary expression and the condition reads it once per iteration,
		// where SNGL evaluates the iterable once. A nested counted loop
		// declares its own __end in its own scope.
		return fmt.Sprintf("for %s, __end := %s, %s; %s %s __end; %s%s {",
			key, start, end, key, cmp, key, step)
	case ir.IterMapEntries:
		if n.Key == "" {
			return fmt.Sprintf("for range %s {", iter)
		}
		valueVar := n.Value
		if valueVar == "" {
			valueVar = "_"
		}
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, valueVar, iter)
	case ir.IterIndexed:
		if n.Key == "" {
			return fmt.Sprintf("for range %s {", iter)
		}
		return fmt.Sprintf("for %s, %s := range %s {", n.Key, n.Value, iter)
	default:
		// An iter<T> is a func, and `range` over one yields the element
		// alone; a slice yields (index, element). Which of the two the
		// iterable is, is a Go representation question rather than a shape
		// question, so IterKind does not classify it -- IRTypeToGo makes the
		// same choice from the same type.
		if lazyIter(n.Iter) {
			if n.Key == "" {
				return fmt.Sprintf("for range %s {", iter)
			}
			return fmt.Sprintf("for %s := range %s {", n.Key, iter)
		}
		if n.Key == "" {
			return fmt.Sprintf("for range %s {", iter)
		}
		return fmt.Sprintf("for _, %s := range %s {", n.Key, iter)
	}
}

// lazyIter reports whether e is spelled as a pull sequence in Go -- an
// iter<T>, which IRTypeToGo renders as func(func(T) bool). A list or map
// reaching an iter<T> position carries an ir.Conversion, so this reads the
// expression's own type and not the loop's element type.
func lazyIter(e ir.Expr) bool {
	if e == nil {
		return false
	}
	t := e.ExprType()
	// A channel ranges like a pull sequence and not like a slice: one variable,
	// the element.
	return t != nil && (t.Kind == ir.TypeIter || t.Kind == ir.TypeChan)
}

func (gc *GoIRContext) IfHead(_ *ir.If, cond string) string { return "if " + cond + " {" }
func (gc *GoIRContext) ElseHead() string                    { return "} else {" }
func (gc *GoIRContext) BlockEnd() string                    { return "}" }
func (gc *GoIRContext) Indent() string                      { return "\t" }

func (gc *GoIRContext) MutTargetIdent(n *ir.Ident) string {
	if host, ok := gc.hostValueIdent(n); ok {
		return host
	}
	sym, kind := gc.Ctx.Resolve(n.Name)
	if kind == codegen.NameStateVar {
		return gc.recvFor(sym) + "." + gc.StateFieldName(n.Name)
	}
	return n.Name
}
func (gc *GoIRContext) MutTargetField(n *ir.Select) string {
	if field, ok := gc.modelField(n); ok {
		return field
	}
	return ExportName(n.Field)
}

// modelField reports the Go name for a field selected straight off the model
// receiver, which is the name the platform declared it with.
//
// A platform declares Model fields verbatim -- a node id, a slot, a canvas
// context -- which is what codegen.ModelFieldRef means by `m.<name>`, and what
// an element-ref Ident already renders. ExportName here spelled the same field
// two different ways depending on which path reached it: read through Select,
// written through MutTargetField. It went unnoticed because ExportName is the
// identity on the synthesized ids (`__n1`), and broke on the first `#id` a
// program wrote -- `m.Inc = widget.NewButton(...)` against a field named
// `inc`, so no fyne or gtk4 program with a tagged widget compiled.
//
// One function for both paths, because two was the bug.
func (gc *GoIRContext) modelField(n *ir.Select) (string, bool) {
	id, ok := n.Operand.(*ir.Ident)
	if !ok || id.Name != gc.RecvName() {
		return "", false
	}
	// And it has to *be* the receiver. A binding that merely shares its name
	// shadows it -- `for var m = entry().typeDoc.methods` in a Model whose receiver
	// is `m` -- and its fields are its own type's, exported like any other Go
	// struct's, rather than the Model's unexported state.
	switch sym := id.Sym.(type) {
	case nil, *ir.Component:
		// A synthesized receiver read carries no symbol.
	case *ir.Param:
		if !sym.Receiver && sym.Name != ir.ReceiverParam {
			return "", false
		}
	default:
		return "", false
	}
	return n.Field, true
}

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

// goFloatLiteral spells a float literal so Go reads it as one. A float whose
// magnitude is whole folds to a spelling with no point in it, and an untyped
// Go constant with no point is an *integer* constant.
//
// Most positions survive that: an assignment to a float64 field, an argument
// to a float64 parameter, or arithmetic against a float64 operand each convert
// the constant from the other half of the expression, and a local now carries
// its declared type (LocalVarText). What is left is the position no type
// annotation reaches -- an element of an `any` composite -- where `[]any{2}`
// boxes an `int` that compares unequal to every float64 beside it, and
// compiles. testdata/go_untyped_constant_types.txtar holds both halves.
//
// It is *not* about `2 / 10`: two literals never reach a backend undivided,
// because the folder answers them first.
//
// A free function because three call sites in this package spell a literal and
// each used to do it for itself.
func goFloatLiteral(v string) string {
	if !strings.ContainsAny(v, ".eE") {
		return v + ".0"
	}
	return v
}

func (gc *GoIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Value)
	case ir.TypeFloat:
		return goFloatLiteral(n.Value)
	case ir.TypeInt, ir.TypeBool:
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
		return gc.RecvName()
	}

	name := n.Name
	// A node handle is stored as a struct field of whatever the scope
	// dispatches through, so it must be qualified: a bare ident would not
	// resolve in the method scope. Asked of both halves, because the two say it
	// in different places -- a lowering pass marks the `__nN` references it
	// synthesizes, while a program's own `#id` is marked on the declaration the
	// checker bound and its reads carry nothing. Neither is in Component.Vars,
	// so Ctx.Resolve below answers for neither.
	//
	// Ahead of Resolve, so it does not see Locals/Renames. That holds only
	// because the two places that deliberately spell a node ref as a local --
	// fyne's localElementRef and gtk4's qualifyNodeExpr -- each build a *fresh*
	// ir.Ident carrying no Sym. Copy one with its Sym instead and the local
	// silently becomes a field read.
	if v, ok := n.Sym.(*ir.Var); ok && v.NodeHandle {
		return gc.NodeRecv(name) + "." + name
	}
	if host, ok := gc.hostValueIdent(n); ok {
		return host
	}
	if n.IsElementRef && n.Synthesized {
		return gc.NodeRecv(name) + "." + name
	}
	sym, kind := gc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return gc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return gc.recvFor(sym) + "." + name + "()"
	case codegen.NameStateVar:
		return gc.recvFor(sym) + "." + gc.StateFieldName(name)
	case codegen.NameConst:
		// A const is a file-scope name in a free function and a field of the
		// receiver inside a method the Model dispatches through -- a window's
		// as much as a component's. Asking about the component alone left a
		// package const read as a bare name against the `m.blank` field the
		// same build declared, once a window was the scope instead.
		if gc.Ctx.Component != nil || gc.Ctx.Window != nil {
			return gc.recvFor(sym) + "." + name
		}
		return name
	case codegen.NameFunc:
		// Named as a value -- `xs.filter(keep)` -- this is a Go method value,
		// which carries its receiver and matches the callback's signature.
		//
		// Spelled the way whoever emits it names it. EmitFuncDef writes
		// fn.Name verbatim, so a reference is verbatim too -- exporting here
		// named a method that was never declared: `m.Inc_click_handler`
		// against `func (m *Model) inc_click_handler()`, which is every
		// promoted handler on a tagged widget.
		//
		// Route mode is the exception, and says so by asking for exported
		// names: writeRouteFuncs renames each func before emitting, because
		// there they are methods on a per-request State struct. Same split as
		// MutTargetIdent makes for a state var.
		return gc.recvFor(sym) + "." + gc.StateFieldName(name)
	case codegen.NameExternFunc, codegen.NameExternVar:
		return gc.recvFor(sym) + "." + ExportName(name)
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
	// Ahead of the registry, because make's spelling comes from the call's
	// return type and an emitter is handed only its arguments.
	if out, ok := gc.MakeChanText(n); ok {
		return out
	}
	// Dispatch by intrinsic ID, never by method name. An unregistered ID falls
	// through to the paths below.
	if out, imports, ok := codegen.EmitIntrinsicCall(langGo, gc.Ctx.Platform, n, gc.EvalExpr); ok {
		for _, p := range imports {
			gc.RequireImport(p)
		}
		return out
	}
	// Before either call path: a declaration that *is* a Go identifier is one
	// wherever the call site reached it. This used to sit inside the namespace
	// path alone, so `go.httpGet(url)` emitted http.Get and the same call
	// reached through an inlined body emitted `httpGet` -- a name nothing
	// declares.
	if out, ok := gc.nativeCall(n); ok {
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
		fsym, kind := gc.Ctx.Resolve(fname)
		if kind == codegen.NameComputed || kind == codegen.NameFunc {
			// A free-function scope has no Model receiver, so the call uses
			// the exported name. A func not emitted into the lib then yields
			// a clean "undefined" Go error rather than silent bad code.
			if gc.FreeFuncScope || (gc.Ctx != nil && gc.Ctx.FreeFuncs[fname]) {
				return ExportName(fname) + "(" + strings.Join(args, ", ") + ")"
			}
			// Route mode emits these as State methods under their exported
			// name, which is also what a reference to one as a value renders
			// (NameFunc below); the Model and instance-record paths keep the
			// name as written.
			return gc.recvFor(fsym) + "." + gc.StateFieldName(fname) + "(" + strings.Join(args, ", ") + ")"
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

// nativeCall renders a call to a declaration that *is* a Go identifier, and
// reports whether n was one.
//
// #[foreign] is excluded by Marked: that names a declaration this file also
// emits, under that declaration's own name. An unmarked foreign path is the
// other case -- the identifier already exists, so the call becomes a call to
// it and the package it lives in is required here, at the call site rather
// than at the top of whatever library package declared it.
//
// The name is spelled as Go spells it after the import, `http.Get`, which is
// what the path makes available. RequireImport adds the path; nothing here
// derives a qualifier from it, because a Go package's name is not a function
// of its import path.
func (gc *GoIRContext) nativeCall(n *ir.Call) (string, bool) {
	if n.Func == nil || n.Func.Foreign.Path == "" || n.Func.Foreign.Marked {
		return "", false
	}
	args := gc.evalCallArgs(n.Args)
	name := n.Func.Foreign.Name
	// The renderer adds the "C." prefix to a bare C identifier; a caller that
	// already prefixed it keeps what it wrote.
	if n.Func.Foreign.Path == "C" && !strings.HasPrefix(name, "C.") {
		name = "C." + name
	} else if n.Func.Foreign.Path != "C" {
		gc.RequireImport(n.Func.Foreign.Path)
	}
	if n.Func.Foreign.Path == "C" {
		args = wrapCArgs(n.Func.Params, args)
	}
	// Context-taking native call: inject the context expression as the first
	// argument. When the importer flagged HasContextArg, supply
	// gc.Ctx.ContextVar (e.g. "r.Context()"), defaulting to
	// context.Background() when unset.
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
	// `method` says the identifier is invoked on its first argument rather
	// than handed it: `c.Circle(1, 2)` where the default is
	// `canvas.Context.Circle(c, 1, 2)`. Both are valid Go for the same
	// method, and which one a host API wants is the API's to say.
	if n.Func.NativeMethod && len(args) > 0 {
		return args[0] + "." + methodTail(name) + "(" + strings.Join(args[1:], ", ") + ")", true
	}
	return name + "(" + strings.Join(args, ", ") + ")", true
}

// wrapCArgs converts each argument to the C type of the parameter it fills.
//
// cgo gives every C numeric type a defined Go type, so a `float64` is not
// assignable to a `C.double` and a call to a C function needs the conversion
// written out. A hand-written emitter does this itself -- gtk4's `dbl` helper
// wraps every cairo coordinate -- and a native declaration has no way to say
// it, so the backend that knows cgo does it from the declared parameter types.
//
// Aligned from the end: a native written in the receiver-first shape is called
// with the receiver as argument zero and no parameter declares it.
//
// A type with no C counterpart here is left alone, which makes the mismatch a
// Go compile error rather than a silent conversion -- `string` in particular,
// where the C type is a `char*` somebody has to allocate and free.
func wrapCArgs(params []*ir.Param, args []string) []string {
	off := len(args) - len(params)
	if off < 0 {
		return args
	}
	out := make([]string, len(args))
	copy(out, args)
	for i, p := range params {
		ct := cTypeFor(p.Type)
		if ct == "" {
			continue
		}
		out[off+i] = ct + "(" + out[off+i] + ")"
	}
	return out
}

// cTypeFor is the cgo type a SNGL type crosses as, or "" for one that has no
// single answer.
func cTypeFor(t *ir.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeFloat:
		return "C.double"
	case ir.TypeInt:
		return "C.int"
	case ir.TypeString:
		// C.CString allocates and the caller owns the result. Nothing here
		// frees it, which is the same trade every hand-written call in the
		// gtk4 emitter already makes (`C.CString("label")`): a widget label
		// and a text run outlive the call, and the alternative is a defer this
		// has no statement to put one in.
		return "C.CString"
	case ir.TypeBool:
		// C spells a boolean as 1 or 0 and Go will not convert one to the
		// other: `C.int(true)` does not compile, and passing the bool through
		// unconverted does not either. There is no inline Go expression for it
		// -- it needs a helper or an `if` -- so the declaration has to say what
		// it means, `#[cnative]` on a parameter typed `int` with the override
		// choosing the value. Loud, because silently returning "" here emitted
		// a call that failed in the user's own `go build` with no hint of
		// where it came from.
		panic(fmt.Sprintf("cnative: a bool parameter has no C spelling; declare it as int and convert in the override (parameter type %s)", t))
	default:
		// Everything else passes through, which is what an opaque handle
		// wants: a `#[cnative("*C.cairo_t")]` struct, a nullable pointer, an
		// enum already converted by a native of its own. A conversion is only
		// written where it is a representation detail and nothing else --
		// `C.double(x)` says how a float64 crosses and loses nothing, whereas
		// a float reaching an int parameter is a decision about rounding and
		// so is the override's to write in SNGL.
		return ""
	}
}

// methodTail is the last segment of a qualified native name, which is the
// method to call on the receiver: `canvas.Context.Circle` invoked on a
// receiver is `.Circle`, since the package and type come from the receiver's
// own type rather than from the call.
func methodTail(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (gc *GoIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := gc.EvalExpr(n.Receiver)
	args := gc.evalCallArgs(n.Args)

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

		// lower.CreateComponent(comp, props) → m.render<Comp>(propArgs...),
		// with the prop literal's fields reordered to the component's
		// declared prop order.
		if fname == "CreateComponent" && len(n.Args) == 2 {
			if compIdent, ok := n.Args[0].Value.(*ir.Ident); ok {
				if comp, ok := compIdent.Sym.(*ir.Component); ok {
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
					// A host that builds the component as a record calls
					// the record's ctor, which is a free function: there is
					// no receiver to reach it through, and the instance it
					// returns is what the caller holds.
					if gc.InstanceRecords && comp.RuntimeInstance {
						pargs = append([]string{gc.ModelRef()}, pargs...)
						return ComponentInstanceCtor(comp.Name) + "(" + strings.Join(pargs, ", ") + ")"
					}
					return gc.RecvName() + "." + ComponentRenderMethod(comp.Name) + "(" + strings.Join(pargs, ", ") + ")"
				}
			}
		}

		// A SNGL package's declarations are emitted into this very Go package,
		// so the alias at the call site names nothing Go has: `readout.cells(…)`
		// is `Cells(…)` or `m.cells(…)`, whichever the emitter chose for it.
		// The alias only survives for a native import, handled above.
		if gc.snglNamespace(n.Receiver) {
			if gc.Ctx != nil && gc.Ctx.FreeFuncs[fname] {
				return ExportName(fname) + "(" + strings.Join(args, ", ") + ")"
			}
			return gc.RecvName() + "." + fname + "(" + strings.Join(args, ", ") + ")"
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

// isStructExpr reports whether an expression reads a plain struct — a value a
// component holds, as opposed to an element ref, which reads as a component.
// An untyped expression is not one: hand-built IR carries no types, and the
// getter form is the older behaviour to fall back on.
func isStructExpr(e ir.Expr) bool {
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd.Builtin == ir.BuiltinNone
}

// snglNamespace reports whether an expression is the alias of an imported
// SNGL package — one whose declarations this build emits itself, as opposed to
// a scheme import naming a real Go package.
func (gc *GoIRContext) snglNamespace(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	ns, ok := id.Sym.(*ir.Namespace)
	return ok && ns.Pkg != nil
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

	// Resolved once here rather than by each branch's own name search.
	var pkg *ir.Package
	if gc.Ctx != nil {
		pkg = gc.Ctx.Pkg
	}
	_, resolved := codegen.OwnerMethod(pkg, receiverName, method)

	// Go allows no methods on int/float/string/bool, so a user-attached method
	// on one lifts to a free `TypeNameMethodName` function.
	if isPrimitiveTypeName(receiverName) && resolved {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(gc.withModelArg(n.Func, args), ", ") + ")"
	}

	// Bubbletea/Fyne emit an owner's funcs as methods on their Model, so a
	// call on one has to dispatch through `m`. The receiver naming the
	// component being emitted is the plain case; the other is a clone the
	// inliner hoisted onto main or onto a window, whose receiver still names
	// the component it was written in -- a declaration pkg.Components no
	// longer lists -- while the definition is a member of the emitting scope.
	//
	// What says so is the *receiver*, not which owner holds the func. It was
	// the owner while a window owned funcs: a clone hoisted into a window body
	// was a window's, and only a genuine top-level method on a user type was
	// the package's. A window owns nothing now, so both are pkg.Funcs and
	// `!owner.IsPackage()` could not tell them apart -- `step__inst0` came out
	// of the call site as the free `MainStep__inst0(m, n)` beside the
	// `func (m *Model) step__inst0` the same build emitted.
	//
	// LiftsToFreeFunc is that question and is already the emit loops' answer,
	// so the definition and the call site now read one rule. A receiver naming
	// nothing the package declares is the hoisted clone: the component it
	// names is gone, and there is no Go type to attach the method to.
	if gc.Ctx != nil && (gc.Ctx.Component != nil && gc.Ctx.Component.Name == receiverName ||
		resolved && !LiftsToFreeFunc(pkg, receiverName)) {
		// A method-form call threads the receiver as args[0]; a zero-arg
		// computed referenced by name carries none, and still dispatches
		// through `m` rather than lifting to a free func.
		//
		// Which receiver, though, is whichever one the call wrote. In an
		// ordinary body that is the Model; in a test body the instance is a
		// local the test named, so `screen.tick()` dispatches through
		// `screen` -- rawFieldAccess is the set the test lowering binds, and
		// asking only whether args[0] spells the Model gave `m.tick(screen)`
		// against a receiver no test file declares.
		name := gc.StateFieldName(method)
		if len(args) >= 1 && (args[0] == gc.RecvName() || gc.rawFieldAccess(n.Args[0].Value)) {
			return args[0] + "." + name + "(" + strings.Join(args[1:], ", ") + ")"
		}
		return gc.RecvName() + "." + name + "(" + strings.Join(args, ", ") + ")"
	}

	// In a test body the component instance is a local — `c := newTestComponent()`
	// — so a call on it dispatches through that local. Lifting it to a free
	// function instead gave `MainPress(c, k)` for what is a method on Model.
	if len(n.Args) > 0 && gc.rawFieldAccess(n.Args[0].Value) && len(args) > 0 {
		return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
	}

	// Lifted to a free `ReceiverName + MethodName(args...)`: otherwise a
	// static-form call like `S.helper(5)` falls through to `args[0].method()`,
	// where args[0] is the first explicit arg rather than a receiver.
	if resolved {
		goName := ExportName(receiverName) + ExportName(method)
		return goName + "(" + strings.Join(gc.withModelArg(n.Func, args), ", ") + ")"
	}

	codegen.RequireIntrinsicFallback(langGo, n.Func)
	if len(args) == 0 {
		return "/* unresolved method " + qualName + " */"
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// withModelArg appends the Model to a lifted type method's arguments when its
// signature declares the parameter. EmitTypeMethodDef reads the same map, so
// the two cannot disagree about the arity.
func (gc *GoIRContext) withModelArg(fn *ir.Func, args []string) []string {
	if gc.Ctx == nil || !gc.Ctx.ModelParamFuncs[fn] {
		return args
	}
	return append(append([]string{}, args...), gc.ModelArg())
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
	// A list flowing into an iter<T> position becomes the pull sequence that
	// type is. slices.Values is that, in the standard library: it yields the
	// slice's elements one at a time and copies nothing.
	if n.Type != nil && n.Type.Kind == ir.TypeIter && len(n.Type.Elems) == 1 {
		if src := n.Operand.ExprType(); src != nil && src.Kind == ir.TypeList {
			gc.RequireImport("slices")
			return "slices.Values(" + gc.EvalExpr(n.Operand) + ")"
		}
	}
	// A narrowed option: Go's option<T> is *T, so reading it as the T a null
	// test proved it to be is the star.
	// The whole deref is parenthesised, not just its operand: `*` binds looser
	// than a selector, so `*(m.h.Inner).Value` is a deref OF the field and Go
	// rejects it. The branch's own fixture read a narrowed path as a call
	// argument, where nothing follows the unwrap and either spelling compiles.
	if ir.IsOptionUnwrap(n) {
		return "(*(" + gc.EvalExpr(n.Operand) + "))"
	}
	// And the promotion the other way. Not `&(x)`: `&` needs an addressable
	// operand, so `&f()` does not compile -- and where it would compile it is
	// wrong, because it aliases. A SNGL struct is a value, so `h.inner = b`
	// must not let a later write to `b` reach h. The helper takes its argument
	// by value and addresses the copy, which answers both at once.
	if ir.IsOptionWrap(n) {
		return "snglSome(" + gc.EvalExpr(n.Operand) + ")"
	}
	goType := IRTypeToGo(n.Type)
	operand := gc.EvalExpr(n.Operand)
	// Go's string(int) builds a single-rune string.
	if n.Type != nil && n.Type.Kind == ir.TypeString {
		if ud := UnitStringConversion(n); ud != nil {
			return UnitStringFn(ud) + "(" + operand + ")"
		}
		gc.RequireImport("fmt")
		return "fmt.Sprint(" + operand + ")"
	}
	if expr := gc.durationToNumber(n, goType, operand); expr != "" {
		return expr
	}
	// And the other direction is not a cast at all: see StringToNumberHelper.
	if helper := StringToNumberHelper(n); helper != "" {
		gc.RequireImport("strconv")
		return helper + "(" + operand + ")"
	}
	// `*T(x)` is invalid; `(*T)(x)` is the valid form.
	if strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[") || strings.HasPrefix(goType, "map[") {
		return "(" + goType + ")(" + operand + ")"
	}
	return goType + "(" + operand + ")"
}

// durationToNumber converts a duration out of Go's representation of one, or
// returns "" for any other conversion. A time.Duration counts nanoseconds
// while `duration` declares its base as ms, so a cast here divides where every
// other target, holding a plain number of ms, does not.
func (gc *GoIRContext) durationToNumber(n *ir.Conversion, goType, operand string) string {
	if n.Type == nil || n.Operand == nil {
		return ""
	}
	if ClassifyUnit(unitDeclOf(n.Operand.ExprType())) != UnitDuration {
		return ""
	}
	switch n.Type.Kind {
	case ir.TypeInt:
		gc.RequireImport("time")
		return goType + "(" + operand + " / time.Millisecond)"
	case ir.TypeFloat:
		// Not `d / time.Millisecond` converted after: that is integer
		// division, so a duration finer than a millisecond would truncate to
		// zero where the other targets report a fraction.
		gc.RequireImport("time")
		return goType + "(" + operand + ") / " + goType + "(time.Millisecond)"
	}
	return ""
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

// isChanLit reports whether a literal's type is a channel, resolving the
// declaration the way structLitTypeName does -- a synthesized zero carries the
// type on n.Type and leaves Def nil.
func isChanLit(n *ir.StructLit) bool {
	if n == nil {
		return false
	}
	if n.Type != nil && n.Type.Kind == ir.TypeChan {
		return true
	}
	def := n.Def
	if def == nil && n.Type != nil {
		if sd, ok := n.Type.Decl.(*ir.StructDef); ok {
			def = sd
		}
	}
	return def != nil && def.Builtin == ir.BuiltinChan
}

// structLitTypeName picks the Go type prefix for a struct literal. An
// anonymous struct materializes an inline `struct { … }`, since Go rejects a
// bare `{…}` literal.
// requireForeignStructImport registers the package a literal of a Go type is
// spelled from: a folded `go:` value reaches the file as `pkg.Item{…}` with no
// call beside it to have registered pkg.
func (gc *GoIRContext) requireForeignStructImport(n *ir.StructLit) {
	sd := n.Def
	if sd == nil && n.Type != nil {
		sd, _ = n.Type.Decl.(*ir.StructDef)
	}
	if sd == nil || sd.Foreign.Name == "" || sd.Foreign.Marked {
		return
	}
	if p := sd.Foreign.Path; p != "" && p != "C" {
		gc.RequireImport(p)
	}
}

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
	// Everything else about how a struct type is spelled is IRTypeToGo's --
	// the stdlib types a backend maps onto its host language's own among them.
	// This used to answer for itself and knew only about color, so
	// `http.Result{…}` wrote a name the output did not declare while every
	// other mention of that type wrote `http.Response`.
	if n.Type != nil && n.Type.Kind == ir.TypeStruct && n.Type.Decl != nil {
		return IRTypeToGo(n.Type)
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

// goReturnType is the result clause of a Go signature, leading space
// included, or "" for a function that returns nothing.
//
// Nothing is the answer for a nil or void return, and also for a TypeDyn: that
// has no first-class Go representation, so the signature reads `func foo()`
// rather than `func foo() any`.
func goReturnType(ret *ir.Type) string {
	if ret == nil || ret.Kind == ir.TypeVoid {
		return ""
	}
	if ret.Kind == ir.TypeDyn {
		return ""
	}
	return " " + IRTypeToGo(ret)
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
	// Same rule as a named func's signature: a lambda that returns nothing
	// says nothing, so an event handler reads `func()` and can be assigned to
	// a host callback field of that type. It used to read `func() any`, which
	// Go rejected at the assignment and again for the missing return.
	retType := goReturnType(n.Func.Return)

	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := gc.EvalExpr(ret.Value)
			return "func(" + strings.Join(params, ", ") + ")" + retType + " { return " + body + " }"
		}
	}

	var b strings.Builder
	b.WriteString("func(" + strings.Join(params, ", ") + ")" + retType + " {\n")
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
		InstanceRecords:    gc.InstanceRecords,
		ModelIsValue:       gc.ModelIsValue,
		imports:            gc.imports, // shared so child writes propagate
	}
}

// WithRenamedLocal binds name as a local that renders as `as` — what a var
// handler's parameter needs, the setter having named the value already.
func (gc *GoIRContext) WithRenamedLocal(name, as string) *GoIRContext {
	c := gc.WithLocal(name)
	c.Ctx = gc.Ctx.WithRenamedLocal(name, as)
	return c
}

func (gc *GoIRContext) ForComponent(comp *ir.Component) *GoIRContext {
	return &GoIRContext{
		Ctx:                gc.Ctx.ForComponent(comp),
		AlertFunc:          gc.AlertFunc,
		FreeFuncScope:      gc.FreeFuncScope,
		EmitLineDirectives: gc.EmitLineDirectives,
		LineDirBase:        gc.LineDirBase,
		InstanceRecords:    gc.InstanceRecords,
		ModelIsValue:       gc.ModelIsValue,
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
	case ir.TypeRemote:
		// A pointer: two readers of one key hold the same box, and a settle has
		// to be visible to both.
		if len(t.Elems) > 0 {
			return "*" + remoteGoType + "[" + IRTypeToGo(t.Elems[0]) + "]"
		}
		return "*" + remoteGoType + "[any]"
	case ir.TypeStruct:
		// date/time/datetime are stdlib structs mapping to time.Time; detect
		// them by name before the generic struct path.
		if ir.IsDateStruct(t) || ir.IsTimeStruct(t) || ir.IsDateTimeStruct(t) {
			return "time.Time"
		}
		// The runtime defines the failure its boxes carry, so generated code
		// spells that rather than emitting a struct of its own that no box
		// could hold.
		if ir.IsRemoteFailureStruct(t) {
			return remoteFailureGo
		}
		// Likewise the value a request answers with: the transport returns it,
		// so a Result declared beside it would be a second type `http.Get`
		// could not hand back.
		if ir.IsRemoteHTTPResultStruct(t) {
			return remoteHTTPResultGo
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
		return "any"
	case ir.TypeEnum:
		return "string"
	case ir.TypeUnit:
		if ud := ir.UnitDeclOf(t); ud != nil {
			if ud.Builtin == ir.BuiltinDuration {
				return "time.Duration"
			}
			return ExportName(ud.Name)
		}
		if t.Decl != nil {
			return ExportName(t.Decl.SymName())
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
	case ir.TypeIter:
		// An iter<T> is a pull sequence, never a materialised one: the shape
		// `range` accepts over a function (Go 1.23), spelled structurally so
		// nothing has to import "iter" to name it. A list reaching an iter<T>
		// position is wrapped by evalConversion, and the wrapper yields the
		// elements one at a time -- so no list is ever built to be iterated.
		if len(t.Elems) == 1 {
			return "func(func(" + IRTypeToGo(t.Elems[0]) + ") bool)"
		}
		return "func(func(any) bool)"
	case ir.TypeChan:
		// Bidirectional, because SNGL does not spell a direction. A host
		// channel that is receive-only reaches a program as a field read and
		// is selected on rather than bound, so no Go type is written for it;
		// binding one to a var would spell this and not compile.
		if len(t.Elems) == 1 {
			return "chan " + IRTypeToGo(t.Elems[0])
		}
		return "chan any"
	case ir.TypeInstance:
		// A live instance of a component is the generated record every Go
		// platform allocates for it. Decl carries which component; without one
		// the handle is a platform node, which has no shared Go spelling.
		if c, ok := t.Decl.(*ir.Component); ok && c != nil {
			return "*" + ComponentInstanceType(c.Name)
		}
		return "any"
	case ir.TypeComponent, ir.TypeTypeParam, ir.TypeInvalid:
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
		case ir.TypeFloat:
			return goFloatLiteral(n.Value)
		case ir.TypeInt, ir.TypeBool:
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
	return op.String()
}

func irAssignOp(op ast.AssignOp) string {
	return op.String()
}

// EmitFuncDef renders a complete Go function definition from an *ir.Func.
// Includes the receiver clause (for Model methods), param list, return
// type, and body. Body statements flow through EvalStmt — Synthesized
// idents resolve to m.<name>, native funcs to their C identifier, etc.
//
// Returns the source as a slice of lines (each line WITHOUT trailing
// newline). The caller joins with "\n" or writes each line followed by
// its own newline.
// RecvName is the identifier a component's own members dispatch through. It is
// the Model receiver `m` in every host that emits a Model, the per-request
// State value in html's route mode, and the record itself inside a component
// instance's ctor and setters: in each, the component's funcs are methods on
// that struct and its vars are its fields, so one name answers for both.
//
// Exported because a platform's intrinsic translator writes node fields
// against the same struct -- see codegen.RecvFieldRef -- and hardcoding `m`
// there put a model reference inside an instance method.
// StateFieldName is the name a component's own member carries on the receiver
// this scope dispatches through. Verbatim, unless the projection asked for
// exported names -- see codegen.ExprCtx.StateFieldsExported.
func (gc *GoIRContext) StateFieldName(name string) string {
	if gc.Ctx != nil && gc.Ctx.StateFieldsExported {
		return ExportName(name)
	}
	return name
}

func (gc *GoIRContext) RecvName() string {
	if gc.Ctx != nil && gc.Ctx.StateReceiver != "" {
		return gc.Ctx.StateReceiver
	}
	return codegen.ModelReceiver
}

// recvFor is the receiver a resolved member is reached through: RecvName, or
// inside a component instance record the Model, for a member the component
// does not declare.
func (gc *GoIRContext) recvFor(sym ir.Symbol) string {
	if gc.Ctx == nil || gc.Ctx.OuterReceiver == "" || gc.Ctx.Component == nil {
		return gc.RecvName()
	}
	switch s := sym.(type) {
	case *ir.Var:
		if slices.Contains(gc.Ctx.Component.Vars, s) {
			return gc.RecvName()
		}
	case *ir.Func:
		if slices.Contains(gc.Ctx.Component.Funcs, s) {
			return gc.RecvName()
		}
	}
	return gc.Ctx.OuterReceiver
}

// NodeRecv is the receiver the node field name is reached through: the Model
// for one of the page's, the scope's own receiver for any other.
func (gc *GoIRContext) NodeRecv(name string) string {
	if gc.Ctx != nil && gc.Ctx.OuterReceiver != "" && gc.Ctx.OuterNodes[name] {
		return gc.Ctx.OuterReceiver
	}
	return gc.RecvName()
}

// ModelRef is the Model this scope belongs to, as a `*Model`: the one a
// component instance record holds, or the receiver.
func (gc *GoIRContext) ModelRef() string {
	if gc.Ctx != nil && gc.Ctx.OuterReceiver != "" {
		return gc.Ctx.OuterReceiver
	}
	return gc.ModelArg()
}

// ModelArg spells the Model where a call hands it to something taking a
// `*Model`. See ModelIsValue for why the two spellings exist.
func (gc *GoIRContext) ModelArg() string {
	if gc.ModelIsValue {
		return "&" + gc.RecvName()
	}
	return gc.RecvName()
}

// EmitTypeMethodDef emits a method on a user type as the free function its
// call sites name: `func GlyphRow(gl Glyph, row int) string`, with the
// receiver as the first parameter (passNoImplicitRecv already put it there).
func (gc *GoIRContext) EmitTypeMethodDef(fn *ir.Func) []string {
	fnCopy := *fn
	fnCopy.Name = ExportName(fn.Receiver) + ExportName(fn.Name)
	fnCopy.Receiver = ""
	sub := *gc
	if gc.Ctx != nil && gc.Ctx.ModelParamFuncs[fn] {
		sub.ExtraParam = gc.RecvName() + " *" + codegen.ModelTypeName
	}
	return sub.EmitFuncDef(&fnCopy)
}

func (gc *GoIRContext) EmitFuncDef(fn *ir.Func) []string {
	var lines []string

	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name + " " + IRTypeToGo(p.Type)
	}
	extra := gc.ExtraParam
	retType := goReturnType(fn.Return)

	sig := "func "
	switch {
	case gc.MethodRecvType != "":
		// Emitted as a method on a type the host names, with the receiver the
		// rest of this context dispatches through. html's route mode uses it
		// to put a component's funcs on the per-request State, which is the
		// only receiver that file has.
		sig += "(" + gc.RecvName() + " *" + gc.MethodRecvType + ") "
		if len(params) > 0 && fn.Receiver != "" {
			// The component receiver arrives as an ordinary first parameter
			// (passNoImplicitRecv). As a Go method it is the receiver, so it
			// is not also an argument.
			params = params[1:]
		}
		sig += fn.Name + "(" + appendParam(params, extra) + ")" + retType + " {"
		lines = append(lines, sig)
		return gc.emitFuncBody(lines, fn, params)
	case fn.Receiver != "":
		sig += "(m *" + fn.Receiver + ") "
		// The synthetic receiver passNoImplicitRecv prepended is this method's
		// Go receiver, so it is not also an argument. Emitting it as one gave
		// `func (m *Model) press(this any, k Key)` against call sites that pass
		// only `k`.
		if len(fn.Params) > 0 && fn.Params[0].Receiver {
			params = params[1:]
		}
	}
	sig += fn.Name + "(" + appendParam(params, extra) + ")" + retType + " {"
	lines = append(lines, sig)

	return gc.emitFuncBody(lines, fn, params)
}

// appendParam renders a parameter list with one extra entry whose Go type no
// ir.Type spells. Empty extra leaves the list as it was.
func appendParam(params []string, extra string) string {
	if extra == "" {
		return strings.Join(params, ", ")
	}
	return strings.Join(append(append([]string{}, params...), extra), ", ")
}

// emitFuncBody appends a function's statements and its closing brace.
func (gc *GoIRContext) emitFuncBody(lines []string, fn *ir.Func, _ []string) []string {
	// Nothing EmitFuncDef writes holds the Model by value, so the body starts
	// from the pointer answer rather than inheriting the caller's.
	inner := *gc
	inner.ModelIsValue = false
	inner.ExtraParam = ""
	bodyGC := &inner
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

// hostValueIdent resolves a reference to a const or var that *is* a Go
// identifier -- `math.Pi`, `os.Args` -- to that identifier, and requires its
// import. Nothing is emitted for the declaration.
//
// Asked on the read and the write path both: a host var is assignable, which
// is the whole reason a var may carry the mark, and answering only on the read
// side emitted `hostArgs = []string{}` against a name this package declares
// nowhere. Ahead of Ctx.Resolve in both, for the reason a node handle is --
// the names Resolve answers for are this package's, and this one is not.
func (gc *GoIRContext) hostValueIdent(n *ir.Ident) (string, bool) {
	v, ok := n.Sym.(*ir.Var)
	if !ok || !ir.IsHostValue(v) {
		return "", false
	}
	if v.Foreign.Path != "" {
		gc.RequireImport(v.Foreign.Path)
	}
	return v.Foreign.Name, true
}

func loopReadNames(n *ir.For) map[string]bool {
	read := map[string]bool{}
	for _, block := range [][]ir.Stmt{n.Body, n.Else} {
		_ = ir.Walk(block, func(nd ir.Node) error {
			if id, ok := nd.(*ir.Ident); ok {
				read[id.Name] = true
			}
			return nil
		})
	}
	return read
}

// WithUnreadVarsDropped is n with a loop variable its body never names left
// out, since Go refuses one declared and not used. A lowering can leave one: a
// slot matches its instances against the inner loop's element and never reads
// the outer loop's. Asked by name, so anything that might read it keeps it.
func WithUnreadVarsDropped(n *ir.For) *ir.For {
	read := loopReadNames(n)
	// A counted loop's variable is read by its own condition, which ForHead
	// names __i when the body does not name one.
	if n.IterKind == ir.IterCounted {
		if n.Value != "" || n.Key == "" || read[n.Key] {
			return n
		}
		cp := *n
		cp.Key = ""
		return &cp
	}
	if (n.Key == "" || read[n.Key]) && (n.Value == "" || read[n.Value]) {
		return n
	}
	cp := *n
	switch {
	case n.Value == "":
		cp.Key = ""
	case !read[n.Key] && !read[n.Value]:
		cp.Key, cp.Value = "", ""
	case !read[n.Key]:
		cp.Key = "_"
	default:
		cp.Value = "_"
	}
	return &cp
}
