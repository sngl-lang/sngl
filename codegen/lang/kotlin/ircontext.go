package kotlin

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	snglI18n "duckfam.us/sngl/codegen/i18n"
	"duckfam.us/sngl/codegen/irwalk"
	"duckfam.us/sngl/ir"
)

// ktImportSet is the shared import collector for a KtIRContext tree.
// Shared by pointer across all derived contexts (WithLocal, ForComponent)
// so any RequireImport call anywhere in the tree is visible to the root.
//
// Every import is explicit -- one name per line, never a star -- and each
// claims the simple name it brings into scope. An explicit import outranks a
// star import and a declaration of the file's own package, so a star leaves
// which declaration a name reaches to whatever the libraries hold today, and
// two explicit imports of one simple name do not compile. Claiming the name
// is what lets Name alias the second instead.
type ktImportSet struct {
	paths map[string]struct{}
	order []string
	// names is the spelling each fully-qualified name was given; taken is the
	// reverse, which simple names some import already brings into scope.
	names map[string]string
	taken map[string]string
}

func newKtImportSet() *ktImportSet {
	return &ktImportSet{paths: map[string]struct{}{}, names: map[string]string{}, taken: map[string]string{}}
}

func (s *ktImportSet) require(path string) {
	if path == "" {
		return
	}
	if _, seen := s.paths[path]; seen {
		return
	}
	s.paths[path] = struct{}{}
	s.order = append(s.order, path)
	fqn, alias, aliased := strings.Cut(path, " as ")
	if !aliased {
		alias = simpleName(fqn)
	}
	// A caller that requires an import spells the name bare, so a second
	// declaration claiming it would be read in its place -- or, two explicit
	// imports of one name, not compile. Go's forced aliases refuse the same
	// way; a caller that cannot know what is taken asks Name.
	if prev, ok := s.taken[alias]; ok {
		if prev == "" {
			panic(fmt.Sprintf("kotlin: import %q is spelled %s, which the file declares; spell it through Name", path, alias))
		}
		if prev != fqn {
			panic(fmt.Sprintf("kotlin: import %q is spelled %s, which %q already claims; spell it through Name", path, alias, prev))
		}
		return
	}
	s.taken[alias] = fqn
	s.names[fqn] = alias
}

// name registers fqn and returns what code writes for it: the simple name
// where no other import has claimed it, and otherwise the simple name
// suffixed, imported under that alias.
func (s *ktImportSet) name(fqn string) string {
	if n, ok := s.names[fqn]; ok {
		return n
	}
	simple := simpleName(fqn)
	if _, ok := s.taken[simple]; !ok {
		s.require(fqn)
		return simple
	}
	for i := 2; ; i++ {
		alias := fmt.Sprintf("%s_%d", simple, i)
		if _, ok := s.taken[alias]; !ok {
			s.require(fqn + " as " + alias)
			return alias
		}
	}
}

// nameAs is name with the simple name replaced by alias.
func (s *ktImportSet) nameAs(fqn, alias string) string {
	if n, ok := s.names[fqn]; ok {
		return n
	}
	for i := 1; ; i++ {
		try := alias
		if i > 1 {
			try = fmt.Sprintf("%s_%d", alias, i)
		}
		if _, ok := s.taken[try]; !ok {
			s.require(fqn + " as " + try)
			return try
		}
	}
}

// simpleName is the last segment of a fully-qualified name.
func simpleName(fqn string) string {
	return fqn[strings.LastIndexByte(fqn, '.')+1:]
}

// KtIRContext translates IR expressions and statements into Kotlin code.
type KtIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string // what the handler event parameter maps to in this scope
	// IdentRewrites remaps bare identifiers regardless of scope kind.
	// Used by the Android test-mode emit to route every component-level
	// var through a hoisted state object (`count` → `state.count`).
	IdentRewrites map[string]string
	// receiver is the parameter an extension function's body names its
	// receiver by, which Kotlin spells `this` whatever the declaration
	// called it. Matched by symbol: IdentRewrites skips parameters.
	receiver *ir.Param
	imports  *ktImportSet
	// returnTo is the label of the lambda a `return` in this scope leaves:
	// a bare return in a Kotlin lambda would return from the function the
	// lambda is written in, which Kotlin refuses for one not inlined.
	returnTo string
}

// NewIRContext creates a KtIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *KtIRContext {
	return &KtIRContext{Ctx: ctx, imports: newKtImportSet()}
}

// Unrewritten is a context for the file kc writes that rewrites no
// identifier: the imports its code requires are the file's, which a context
// of its own would require of nobody. Android's hoisted state class is
// written through one, since its members reach each other through `this`.
func (kc *KtIRContext) Unrewritten() *KtIRContext {
	return &KtIRContext{Ctx: kc.Ctx, imports: kc.imports}
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

// Name registers the import of a fully-qualified name and returns what code
// writes for it: `Card` for androidx.compose.material3.Card, or `Card_2`,
// imported under that alias, where another import already brought a `Card`
// into scope. The Kotlin counterpart of Go's AliasFor: a caller writing the
// simple name itself could not know what another import already claimed.
//
// Assignment is in registration order, which is the walk order, so the result
// is stable across runs of one build. An operator a convention finds by name
// -- a `by` delegate's getValue -- cannot be renamed and goes through
// RequireImport.
func (kc *KtIRContext) Name(fqn string) string {
	if kc.imports == nil {
		return simpleName(fqn)
	}
	return kc.imports.name(fqn)
}

// NameAs is Name with the alias to prefer: the fqn is imported as alias
// unless something else holds that name, and then as alias_2, alias_3 and so
// on. For a name the file means to spell other than its simple name --
// Compose's Color is ComposeColor here, beside the Color data class SNGL's
// own color type becomes. A name already registered keeps its spelling.
func (kc *KtIRContext) NameAs(fqn, alias string) string {
	if kc.imports == nil {
		return alias
	}
	return kc.imports.nameAs(fqn, alias)
}

// NativeName is what the file writes for the name a `#[kotlin.native(name,
// path)]` declaration gives, importing what reaches it. Where the import names
// the declaration itself (`Card` from androidx.compose.material3.Card), or the
// name is reached through it (`Alignment.CenterVertically` beside an import of
// androidx.compose.ui.Alignment), the import goes through Name, so it is
// aliased around whatever else claims the simple name. Any other name is
// written as the mark gave it, beside the import as it is. A declaration with
// no path names a global or a fully-qualified name, and imports nothing.
func (kc *KtIRContext) NativeName(path, name string) string {
	if path == "" {
		return name
	}
	simple := simpleName(path)
	if name == simple {
		return kc.Name(path)
	}
	if rest, ok := strings.CutPrefix(name, simple+"."); ok {
		return kc.Name(path) + "." + rest
	}
	kc.RequireImport(path)
	return name
}

// Reserve says the file declares name at its top level. An explicit import
// outranks a declaration of the file's own package, so an import bringing the
// same name into scope would take every reference to the declaration with it;
// Name aliases around a reserved name instead. One an import already claimed
// is left to the import -- passReservedNames is what keeps a program from
// declaring the names a target imports unasked.
func (kc *KtIRContext) Reserve(name string) {
	if kc.imports == nil || name == "" {
		return
	}
	if _, ok := kc.imports.taken[name]; !ok {
		kc.imports.taken[name] = ""
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
	// unit / unit is the dimensionless float the two magnitudes divide to, and
	// a duration is carried as a Long here -- so the bare `/` is Kotlin's
	// integer division and yields a Long where a Double is declared.
	if ir.IsUnitRatio(n) {
		return "(" + left + ".toDouble() / " + right + ".toDouble())"
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

// selectField spells the member n reads: a list's or a map's length is its
// size.
func selectField(n *ir.Select) string {
	if n.Field == "length" {
		if t := n.Operand.ExprType(); t != nil && (t.Kind == ir.TypeList || t.Kind == ir.TypeMap) {
			return "size"
		}
	}
	return SafeIdent(n.Field)
}

func (kc *KtIRContext) Select(n *ir.Select, operand string) string {
	if t := n.Operand.ExprType(); ir.PayloadEnum(t) != nil {
		return "(" + operand + " as? " + enumVariant(t, n.Field) + ")?.value"
	}
	return operand + "." + selectField(n)
}

func (kc *KtIRContext) Index(n *ir.Index, operand, idx string) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
		valZero := ktMapValZero(t)
		return "(" + operand + "[" + idx + "] ?: " + valZero + ")"
	}
	return operand + "[" + idx + "]"
}
func (kc *KtIRContext) ListLit(n *ir.ListLit, elems []string) string {
	// An empty literal says nothing Kotlin can infer its element from.
	if len(elems) == 0 && n != nil && n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) == 1 && !n.Type.Elems[0].HasTypeParam() {
		return "listOf<" + IRTypeToKt(n.Type.Elems[0]) + ">()"
	}
	return "listOf(" + strings.Join(elems, ", ") + ")"
}
func (kc *KtIRContext) MapLit(n *ir.MapLitIR, keys, vals []string) string {
	if len(keys) == 0 && n != nil && n.Type != nil && n.Type.Kind == ir.TypeMap && len(n.Type.Elems) == 2 && !slices.ContainsFunc(n.Type.Elems, (*ir.Type).HasTypeParam) {
		return "mapOf<" + IRTypeToKt(n.Type.Elems[0]) + ", " + IRTypeToKt(n.Type.Elems[1]) + ">()"
	}
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
	if member, _, ok := ir.EnumLitMember(n); ok {
		return enumLitKt(n.Type, member, fieldStrs[0])
	}
	if ir.IsBuilder(n.Type) {
		return "StringBuilder()"
	}
	name := "Any"
	if n.Def != nil {
		name = exportName(n.Def.Name)
	}
	parts := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		if f.Spread {
			panic("kotlin: struct spread must be lowered by flatten_struct_spread")
		}
		parts[i] = SafeIdent(f.Name) + " = " + fieldStrs[i]
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}
func (kc *KtIRContext) Spread(_ *ir.Spread, operand string) string {
	return "*" + operand + ".toTypedArray()"
}

func (kc *KtIRContext) Call(n *ir.Call) string             { return kc.evalCall(n) }
func (kc *KtIRContext) Conversion(n *ir.Conversion) string { return kc.evalConversion(n) }
func (kc *KtIRContext) Lambda(n *ir.Lambda) string         { return kc.evalLambda(n) }

func (kc *KtIRContext) AssignText(n *ir.Assign, target, value string) string {
	// A map is a read-only `Map` on Kotlin, so an entry write rebuilds it --
	// which is also the write Compose sees, the map being held by a
	// `mutableStateOf` that recomposes on reassignment.
	if idx, ok := n.Target.(*ir.Index); ok && isMapExpr(idx.Operand) {
		recv := irwalk.EvalMutTarget(kc, idx.Operand)
		key := kc.EvalExpr(idx.Idx)
		if n.Op != ast.AssignSet {
			op := strings.TrimSuffix(n.Op.String(), "=")
			value = "(" + recv + "[" + key + "] ?: " + ktMapValZero(idx.Operand.ExprType()) + ") " + op + " " + value
		}
		return recv + " = " + recv + " + (" + key + " to " + value + ")"
	}
	// A list the program keeps as state is a `SnapshotStateList` held by a
	// val, so it cannot be reassigned; its contents are replaced instead. The
	// value is bound first because it may read the list being cleared.
	if n.Op == ast.AssignSet && kc.isStateList(n.Target) {
		return "(" + value + ").let { __v -> " + target + ".clear(); " + target + ".addAll(__v) }"
	}
	// A struct held by a `mutableStateOf` is a data class, and writing one of
	// its fields in place changes nothing the snapshot sees, so the view never
	// recomposed. The write rebuilds the path with copy() and reassigns the
	// state, as the map write above does.
	if sel, ok := n.Target.(*ir.Select); ok && kc.isStateStructField(sel) {
		if n.Op != ast.AssignSet {
			value = target + " " + strings.TrimSuffix(n.Op.String(), "=") + " (" + value + ")"
		}
		return kc.rebuildStructPath(sel, value)
	}
	// An element of a list in such a field: the list is a read-only `List`, so
	// it is rebuilt with the element written and the path copied back.
	if idx, ok := n.Target.(*ir.Index); ok && !isMapExpr(idx.Operand) {
		if sel, ok := idx.Operand.(*ir.Select); ok && kc.isListFieldPath(sel) {
			key := kc.WithLocal("it").EvalExpr(idx.Idx)
			return kc.rebuildStructPath(sel, irwalk.EvalMutTarget(kc, sel)+".toMutableList().also { it["+key+"] "+n.Op.String()+" "+value+" }")
		}
	}
	return target + " " + n.Op.String() + " " + value
}

// rebuildStructPath writes value at a field path through data classes held by
// a state var: each level is copied with the level below it replaced, and the
// var reassigned, which is the write Compose's snapshot sees.
func (kc *KtIRContext) rebuildStructPath(sel *ir.Select, value string) string {
	var e ir.Expr = sel
	for {
		s, ok := e.(*ir.Select)
		if !ok {
			break
		}
		value = irwalk.EvalMutTarget(kc, s.Operand) + ".copy(" + kc.MutTargetField(s) + " = " + value + ")"
		e = s.Operand
	}
	return irwalk.EvalMutTarget(kc, e) + " = " + value
}

// fieldListMutation is a list mutation whose receiver is a list held in such a
// field -- `h.items.push(2)` -- written as a rebuild of the list with the
// mutation applied to a copy, or "" for any other call. A data class's field
// is a read-only `List`, which has no `add`.
func (kc *KtIRContext) fieldListMutation(call *ir.Call) string {
	if call == nil || call.Func == nil || call.Func.Intrinsic == "" || len(call.Args) == 0 {
		return ""
	}
	if def, ok := ir.IntrinsicByName(call.Func.Intrinsic); !ok || !def.MutatesReceiver {
		return ""
	}
	sel, ok := call.Args[0].Value.(*ir.Select)
	if !ok || !kc.isListFieldPath(sel) {
		return ""
	}
	t := sel.ExprType()
	if t == nil || t.Kind != ir.TypeList {
		return ""
	}
	onCopy := *call
	onCopy.Args = append([]ir.CallArg{{Value: &ir.Ident{Name: "it", Type: t}}}, call.Args[1:]...)
	return kc.rebuildStructPath(sel, irwalk.EvalMutTarget(kc, sel)+".toMutableList().also { "+kc.WithLocal("it").EvalExpr(&onCopy)+" }")
}

// isStateStructField reports whether sel is a field path through emitted data
// classes down from a state var.
func (kc *KtIRContext) isStateStructField(sel *ir.Select) bool {
	var e ir.Expr = sel
	for {
		s, ok := e.(*ir.Select)
		if !ok {
			break
		}
		t := s.Operand.ExprType()
		if t == nil || t.Kind != ir.TypeStruct || ir.StringReprStruct(t) {
			return false
		}
		if sd, ok := t.Decl.(*ir.StructDef); !ok || sd.Foreign.Name != "" {
			return false
		}
		e = s.Operand
	}
	// An element of a state list is a root too: the list is a
	// SnapshotStateList, whose `set` is the write the snapshot sees.
	if idx, ok := e.(*ir.Index); ok && !isMapExpr(idx.Operand) {
		return kc.isStateList(idx.Operand)
	}
	id, ok := e.(*ir.Ident)
	if !ok || kc.Ctx == nil {
		return false
	}
	_, kind := kc.Ctx.Resolve(id.Name)
	return kind == codegen.NameStateVar
}

// isListFieldPath is isStateStructField, or a field path down from a local:
// a list in a data class's field is a read-only `List` wherever the struct is
// held, so writing it rebuilds the path, and a local is a Kotlin `var` that
// can be reassigned. A parameter is a `val` and cannot. A scalar field of a
// local's struct is written in place.
func (kc *KtIRContext) isListFieldPath(sel *ir.Select) bool {
	if kc.isStateStructField(sel) {
		return true
	}
	var e ir.Expr = sel
	for {
		s, ok := e.(*ir.Select)
		if !ok {
			break
		}
		t := s.Operand.ExprType()
		if t == nil || t.Kind != ir.TypeStruct || ir.StringReprStruct(t) {
			return false
		}
		if sd, ok := t.Decl.(*ir.StructDef); !ok || sd.Foreign.Name != "" {
			return false
		}
		e = s.Operand
	}
	id, ok := e.(*ir.Ident)
	if !ok || kc.Ctx == nil {
		return false
	}
	_, kind := kc.Ctx.Resolve(id.Name)
	v, ok := id.Sym.(*ir.Var)
	return ok && !v.IsConst && (kind == codegen.NameLocal || kind == codegen.NameUnknown)
}

func isMapExpr(e ir.Expr) bool {
	t := e.ExprType()
	return t != nil && t.Kind == ir.TypeMap
}

// isStateList reports whether e names a list-typed state var, which every
// Compose owner declares as a `SnapshotStateList`.
func (kc *KtIRContext) isStateList(e ir.Expr) bool {
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeList {
		return false
	}
	switch n := e.(type) {
	case *ir.Ident:
		if kc.Ctx == nil {
			return false
		}
		_, kind := kc.Ctx.Resolve(n.Name)
		return kind == codegen.NameStateVar
	case *ir.Select:
		ot := n.Operand.ExprType()
		return ot != nil && ot.Kind == ir.TypeComponent
	}
	return false
}

// valueCopyFor is a local's initializer as Kotlin binds it. passValueCopy has
// already made the copy a store needs (ir/valuecopy.go), which matters here
// more than anywhere: Compose decides whether to recompose by structural
// equality, so a state value mutated through a binding that shared it looked
// unchanged and no button did anything. A binding nothing writes needs no copy
// to contain a mutation, and passMutatedVars says which those are, so its
// copy is dropped and it reads the value it was given.
func (kc *KtIRContext) valueCopyFor(n *ir.LocalVar, rendered string) string {
	if n.Sym == nil || kc.Ctx == nil || kc.Ctx.Pkg == nil || kc.Ctx.Pkg.MutatedVars == nil || kc.Ctx.Pkg.MutatedVars[n.Sym] {
		return rendered
	}
	if c, ok := n.Init.(*ir.Call); ok && ir.IsValueCopy(c) && len(c.Args) == 1 {
		return kc.EvalExpr(c.Args[0].Value)
	}
	return rendered
}

func (kc *KtIRContext) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target
}
func (kc *KtIRContext) CallStmtLines(n *ir.CallStmt) []string {
	if n.Call != nil && n.Call.ErrorMode != ir.ErrorNone {
		if lines := kc.evalErrorAwareCall(n.Call); lines != nil {
			return lines
		}
		if lines := kc.catchAtCall(n.Call); lines != nil {
			return lines
		}
		if lines := kc.raiseFailure(n.Call); lines != nil {
			return lines
		}
	}
	if line := kc.fieldListMutation(n.Call); line != "" {
		return []string{line}
	}
	return []string{kc.EvalExpr(n.Call)}
}

// raiseFailure turns a bubbling `fails` native's exception into a SnglRaise, so
// the catch block around it takes it for one.
func (kc *KtIRContext) raiseFailure(call *ir.Call) []string {
	if call.ErrorMode != ir.ErrorBubble || call.Func == nil || !call.Func.HasErrorReturn {
		return nil
	}
	return []string{
		"try {",
		"\t" + kc.EvalExpr(call),
		"} catch (__err: SnglRaise) {",
		"\tthrow __err",
		"} catch (__err: Exception) {",
		"\tthrow SnglRaise(ErrorEvent(__err.message ?: \"\", \"\"))",
		"}",
	}
}

// catchAtCall emits a call to a fallible function under a try whose catch is
// the handler this site resolved to. A raise arrives as SnglRaise carrying
// its event, and only that is caught, as Go re-panics what is not an
// ErrorEvent -- except from a `fails` native called here, whose failure is
// any exception and has no kind.
func (kc *KtIRContext) catchAtCall(call *ir.Call) []string {
	handler := ir.CatchingHandler(call)
	if handler == nil || handler.Func == nil {
		return nil
	}
	caught := "SnglRaise"
	if call.Func != nil && call.Func.HasErrorReturn {
		caught = "Exception"
	}
	lines := []string{
		"try {",
		"\t" + kc.EvalExpr(call),
		"} catch (__err: " + caught + ") {",
	}
	if len(handler.Func.Params) > 0 {
		event := "__err.event"
		if caught != "SnglRaise" {
			event = "ErrorEvent(__err.message ?: \"\", \"\")"
		}
		lines = append(lines, "\tval "+handler.Func.Params[0].Name+" = "+event)
	}
	lines = append(lines, kc.handlerBody(handler)...)
	return append(lines, "}")
}

// handlerBody renders an inlined @error body one indent in. A `return` there
// ends the handler, not the function it was inlined into, so a body holding
// one runs as a local fun of its own.
func (kc *KtIRContext) handlerBody(handler *ir.EventHandler) []string {
	returns := ir.BlockReturns(handler.Func.Block)
	inner := kc
	if returns {
		// The local fun's own return, not the lambda's it is written in.
		c := *kc
		c.returnTo = ""
		inner = &c
	}
	var body []string
	for _, stmt := range handler.Func.Block {
		body = append(body, inner.EvalStmt(stmt)...)
	}
	if returns {
		head := "fun __onError() {"
		if ir.BlockHasAsyncCall(handler.Func.Block) {
			head = "suspend " + head
		}
		wrapped := []string{head}
		for _, l := range body {
			wrapped = append(wrapped, "\t"+l)
		}
		body = append(wrapped, "}", "__onError()")
	}
	out := make([]string, len(body))
	for i, l := range body {
		out[i] = "\t" + l
	}
	return out
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
		// A variant's class is narrower than the sealed class a later write
		// may hold another variant of.
		if ir.PayloadEnum(n.Type) != nil {
			return "var " + n.Name + ": " + IRTypeToKt(n.Type) + " = " + kc.valueCopyFor(n, initStr)
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
	ret := "return"
	if kc.returnTo != "" {
		ret += "@" + kc.returnTo
	}
	if n.Value != nil {
		return ret + " " + valueStr
	}
	return ret
}

// BreakText is a bare break whatever its label: Kotlin's when takes no break
// of its own, so one in a when leaves the loop around it, and the loop needs
// no label (ForHead writes none).
func (kc *KtIRContext) BreakText(*ir.Break) string { return "break" }

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
		// Single-var list / iter[T]: for (x in list) { ... }
		return fmt.Sprintf("for (%s in %s) {", key, iter)
	}
}
func (kc *KtIRContext) IfHead(_ *ir.If, cond string) string { return "if (" + cond + ") {" }
func (kc *KtIRContext) ElseHead() string                    { return "} else {" }
func (kc *KtIRContext) BlockEnd() string                    { return "}" }
func (kc *KtIRContext) Indent() string                      { return "\t" }

// isParamRef reports whether n reads a parameter. IdentRewrites maps a state
// var's name to its hoisted cell, and a parameter spelled the same is not
// that var: android's checkbox override hands `onCheckedChange` a lambda
// taking `on`, and a program's `var on` made it `state.on = state.on`.
func isParamRef(n *ir.Ident) bool {
	_, ok := n.Sym.(*ir.Param)
	return ok
}

func (kc *KtIRContext) MutTargetIdent(n *ir.Ident) string {
	if host, ok := kc.hostValueIdent(n); ok {
		return host
	}
	if isParamRef(n) {
		if kc.receiver != nil && n.Sym == ir.Symbol(kc.receiver) {
			return "this"
		}
		return n.Name
	}
	// A write names the var it writes, so a same-named lambda parameter in
	// scope (android's checkbox hands its lambda `on`) does not shadow it --
	// and a function's own local is not the page's var of its name:
	// sngl:http's `checked()` wrote its `kind` as `state.kind`.
	if v, isVar := n.Sym.(*ir.Var); isVar && kc.declared(v) {
		if rewritten, ok := kc.IdentRewrites[n.Name]; ok {
			return rewritten
		}
	}
	if rewritten, ok := kc.identRewrite(n.Name); ok {
		return rewritten
	}
	return n.Name
}

// declared reports whether v is a var the package or one of its components
// declares, rather than a local a body binds.
func (kc *KtIRContext) declared(v *ir.Var) bool {
	if kc.Ctx == nil || kc.Ctx.Pkg == nil {
		return true
	}
	if slices.Contains(kc.Ctx.Pkg.Vars, v) {
		return true
	}
	for _, c := range kc.Ctx.Pkg.Components {
		if slices.Contains(c.Vars, v) {
			return true
		}
	}
	return false
}

// identRewrite is the rewrite for a name the page's state declares, unless
// the scope binds the name itself: a prop or loop variable of the same name
// in another composable is not the page's.
func (kc *KtIRContext) identRewrite(name string) (string, bool) {
	rewritten, ok := kc.IdentRewrites[name]
	if !ok {
		return "", false
	}
	if kc.Ctx != nil {
		if _, kind := kc.Ctx.Resolve(name); kind == codegen.NameLocal {
			return "", false
		}
	}
	return rewritten, true
}
func (kc *KtIRContext) MutTargetField(n *ir.Select) string { return SafeIdent(n.Field) }

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
		return Quote(n.Value)
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
			return Quote(n.Value)
		}
		return n.Value
	default:
		return n.Value
	}
}

func (kc *KtIRContext) evalIdent(n *ir.Ident) string {
	// The Kotlin i18n runtime keys plural forms by string category exclusively.
	if s := snglI18n.PluralKeyConstString(n); s != "" {
		return s
	}
	if n.Member != "" {
		if ir.PayloadEnum(n.Type) != nil {
			return enumLitKt(n.Type, n.Member, "")
		}
		// Enum member: emit qualified Kotlin enum value (Gender.female)
		// so the value matches the declared enum type at the use site.
		if n.Type != nil && n.Type.Kind == ir.TypeEnum && n.Type.Decl != nil {
			return EnumName(n.Type.Decl) + "." + EnumEntry(n.Member)
		}
		return fmt.Sprintf("%q", n.Member)
	}
	// A name the emitted code bound itself (codegen.HostValue).
	if _, ok := n.Sym.(*codegen.Receiver); ok {
		return n.Name
	}
	name := n.Name
	if host, ok := kc.hostValueIdent(n); ok {
		return host
	}
	if rewritten, ok := kc.identRewrite(name); ok && !isParamRef(n) {
		return funcReference(n.Sym, rewritten)
	}
	if p, ok := n.Sym.(*ir.Param); ok {
		if kc.receiver != nil && p == kc.receiver {
			return "this"
		}
		if p.Receiver {
			return name
		}
	}
	_, kind := kc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return SafeIdent(kc.Ctx.RenamedName(name))
	default:
		return funcReference(n.Sym, SafeIdent(name))
	}
}

// funcReference spells a function named as a value as Kotlin's reference to
// it, `::f` or `state::f`; anything else is returned as spelled.
func funcReference(sym ir.Symbol, spelled string) string {
	if !takesArgs(sym) {
		return spelled
	}
	if i := strings.LastIndexByte(spelled, '.'); i >= 0 {
		return spelled[:i] + "::" + spelled[i+1:]
	}
	return "::" + spelled
}

// takesArgs reports whether sym is a function a bare name can only be a
// reference to: one with a parameter past its receiver, which no implicit
// call reads as a getter.
func takesArgs(sym ir.Symbol) bool {
	fn, ok := sym.(*ir.Func)
	if !ok {
		return false
	}
	params := fn.Params
	if len(params) > 0 && params[0].Receiver {
		params = params[1:]
	}
	return len(params) > 0
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
	name := kc.NativeName(n.Func.Foreign.Path, n.Func.Foreign.Name)
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
		out[i] = SafeIdent(p.Name) + " = " + out[i]
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
	// A codec call reads the type it reads or writes, which is the call's.
	if out, ok := kc.codecCall(n); ok {
		return out
	}
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
	if n.Qualifier != nil && n.Func != nil {
		if out, ok := kc.evalQualifiedCall(n); ok {
			return out
		}
	}
	// A member nothing declares -- an import that resolved to no package --
	// is named as the call site wrote it.
	if n.Qualifier != nil && n.Func == nil {
		return n.QualifiedName() + "(" + strings.Join(kc.evalCallArgs(n.Args), ", ") + ")"
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
			_, args := carriedReceiver(n)
			return name + "(" + strings.Join(kc.evalCallArgs(args), ", ") + ")"
		}
	}
	if n.Func != nil && n.Func.IsMethod() {
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
	if lam, ok := n.Callee.(*ir.Lambda); ok && lam.Func != nil && ir.BlockHasAsyncCall(lam.Func.Block) {
		return kc.waitingLiteralCall(lam, args)
	}
	if n.Callee != nil {
		return kc.EvalExpr(n.Callee) + "(" + strings.Join(args, ", ") + ")"
	}
	return "(" + strings.Join(args, ", ") + ")"
}

// waitingLiteralCall is a function literal that waits, called where it
// stands -- an adapter's `fetch=func(k) => call(k)` spliced into the mount
// that calls it. A Kotlin lambda is no suspend function, so the body is
// written in place as a block its parameters are bound in, which waits
// wherever its caller does, and whose `return` leaves the block.
func (kc *KtIRContext) waitingLiteralCall(lam *ir.Lambda, args []string) string {
	inner := *kc
	inner.returnTo = "run"
	lines := []string{"run {"}
	for i, a := range args {
		lines = append(lines, fmt.Sprintf("\tval __arg%d = %s", i, a))
	}
	for i, p := range lam.Func.Params {
		if i < len(args) {
			lines = append(lines, fmt.Sprintf("\tval %s = __arg%d", SafeIdent(p.Name), i))
		}
	}
	body := &inner
	for _, p := range lam.Func.Params {
		body = body.WithLocal(p.Name)
	}
	for _, name := range codegen.BodyLocals(lam.Func.Block) {
		body = body.WithLocal(name)
	}
	for _, stmt := range lam.Func.Block {
		for _, line := range body.EvalStmt(stmt) {
			lines = append(lines, "\t"+line)
		}
	}
	return strings.Join(append(lines, "}"), "\n")
}

// evalQualifiedCall is a call named through an import, where the import still
// means something in Kotlin. A SNGL package's declarations are top-level funs
// of this build, so for one of those the alias names nothing and the call is
// an ordinary one: false. `readout.cells("1.5")` reached the android test
// runner verbatim, against a `cells` declared beside it.
func (kc *KtIRContext) evalQualifiedCall(n *ir.Call) (string, bool) {
	// An i18n.* entry point maps to the runtime's I18n object.
	if ir.IsI18nCall(n) {
		if result := kotlinBuiltinMethodFromArgs("i18n."+n.Func.Name, kc.evalCallArgs(n.Args)); result != "" {
			kc.RequireImport(SnglI18nKotlinPackage + ".I18n")
			return result, true
		}
	}
	return "", false
}

func (kc *KtIRContext) evalNamespaceCall(n *ir.Call) string {
	receiver := kc.EvalExpr(n.Receiver)
	args := kc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		allArgs := append([]string{receiver}, args...)
		if result := kotlinBuiltinMethodFromArgs(n.Func.RecvName()+"."+fname, allArgs); result != "" {
			return result
		}
		// Only for a receiver whose type the checker could not resolve. With an id
		// in hand the registry is the answer, and its absence has to reach
		// RequireIntrinsicFallback rather than be hidden by a name match.
		if n.Func.Intrinsic == "" {
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
		return []string{"throw SnglRaise(" + evt + ")"}
	case ir.ErrorInvokeAndTerminate:
		if call.ResolvedHandler == nil || call.ResolvedHandler.Func == nil {
			return []string{"throw SnglRaise(" + evt + ")"}
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

// Catch renders a catch block as try/catch over SnglRaise, the exception a
// raise throws, so a host exception is not taken for one.
func (kc *KtIRContext) Catch(n *ir.If, body []string) []string {
	h := n.Catch
	lines := []string{"try {"}
	for _, l := range body {
		lines = append(lines, "\t"+l)
	}
	lines = append(lines, "} catch (__e: SnglRaise) {")
	if h.Func != nil && len(h.Func.Params) > 0 {
		lines = append(lines, "\tval "+h.Func.Params[0].Name+" = __e.event")
	}
	if h.Func != nil {
		lines = append(lines, kc.handlerBody(h)...)
	}
	return append(lines, "}")
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
	lines = append(lines, kc.handlerBody(handler)...)
	lines = append(lines, "}")
	return lines
}

func (kc *KtIRContext) evalTypeMethodCall(n *ir.Call) string {
	method := n.Func.Name
	receiverName := n.Func.RecvName()
	qualName := receiverName + "." + method

	// Component-self method reference (a method on a component, by its
	// declaration) — e.g. a computed or 0-arg func referenced from the view. The
	// Compose backend emits a computed as a `val <name> by remember {
	// derivedStateOf {...} }` property (read by bare name) and a plain func as
	// `<name>()`. Resolve to the bare name here rather than falling through to
	// the unresolved-method marker below. Routes through IdentRewrites so
	// test-mode `state.` prefixing still applies.
	if n.Func.OnComponent() {
		name := method
		rewritten := false
		if kc.IdentRewrites != nil {
			if rw, ok := kc.IdentRewrites[name]; ok {
				name, rewritten = rw, true
			}
		}
		// A rewrite already names the method through the instance, and
		// otherwise the receiver is spelled before the dot.
		recv, args := carriedReceiver(n)
		if recv != nil && !rewritten {
			name = kc.EvalExpr(recv) + "." + name
		}
		if codegen.IsComputed(n.Func) {
			return name
		}
		callArgs := kc.evalCallArgs(args)
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
	if len(args) == 1 && codegen.IsComputed(n.Func) {
		return args[0] + "." + method
	}
	return args[0] + "." + method + "(" + strings.Join(args[1:], ", ") + ")"
}

// carriedReceiver splits off the instance a call of a component's method
// carries in front of its arguments, which a test's call through the
// instance does -- `c.rows().size` -- and the composable's own call leaves
// implicit. The func takes no such parameter: it is a member of what holds
// the state. recv is nil for a call carrying none.
func carriedReceiver(n *ir.Call) (recv ir.Expr, args []ir.CallArg) {
	fn := n.Func
	if fn != nil && fn.OnComponent() && len(n.Args) > 0 && len(n.Args) == len(fn.Params) && fn.Params[0].Receiver {
		return n.Args[0].Value, n.Args[1:]
	}
	return nil, n.Args
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
	// A method on a type is emitted as an extension function and
	// is called as one, so the scope must not claim its bare name: the package
	// scope answers for every func it holds and a method is one of them, so
	// `func Item.label()` resolved here and came out as `label(it)` against a
	// declaration spelled `fun Item.label()`. A component's method is the case
	// this lookup exists for and is left alone -- there is no Kotlin type to
	// extend, and the composable calls it by bare name.
	if fn.OnType() {
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

func ktNumberConvMethod(t *ir.Type) string {
	if t.Kind != ir.TypeFloat {
		return ktIntConvMethod(t)
	}
	if t.Bits == 32 {
		return "toFloat()"
	}
	return "toDouble()"
}

func (kc *KtIRContext) evalConversion(n *ir.Conversion) string {
	if ir.IsNullToFuncConv(n) {
		return nullFuncStubKt(n.Type)
	}
	operand := kc.EvalExpr(n.Operand)
	// A narrowed option: Kotlin's option[T] is T?, and the null test is what
	// makes the assertion safe.
	if ir.IsOptionUnwrap(n) {
		return operand + "!!"
	}
	// And the promotion the other way needs nothing: T is a subtype of T?.
	if ir.IsOptionWrap(n) {
		return operand
	}
	if _, dst, ok := ir.NumericListConversion(n); ok {
		return operand + ".map { it." + ktNumberConvMethod(dst.Elems[0]) + " }"
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt, ir.TypeFloat:
			return operand + "." + ktNumberConvMethod(n.Type)
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
		if ir.PayloadEnum(t) != nil {
			return enumZeroKt(t)
		}
		// An enum's zero is its first member, as it is on every other target.
		// `null` is not a value of a non-nullable Kotlin enum, so a struct
		// field left at its default did not compile.
		if ed, ok := t.Decl.(*ir.EnumDef); ok && len(ed.Members) > 0 {
			return EnumName(ed) + "." + EnumEntry(ed.Members[0].Name)
		}
	case ir.TypeFunc:
		// A function that does nothing, returning its result's zero.
		if t.Sig != nil {
			params := make([]string, len(t.Sig.Params))
			for i, p := range t.Sig.Params {
				params[i] = "_: " + IRTypeToKt(p.Type)
			}
			body := ""
			if t.Sig.Return != nil && t.Sig.Return.Kind != ir.TypeVoid {
				body = " " + ktZeroFor(t.Sig.Return)
			}
			return "{ " + strings.Join(params, ", ") + " ->" + body + " }"
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

// EnumName is the Kotlin name of an enum declaration. A library enum is
// prefixed because its SNGL names are the ones Compose uses for its own types
// -- FontWeight, TextAlign, Alignment -- and the file imports those by name.
func EnumName(decl ir.Symbol) string {
	ed, ok := decl.(*ir.EnumDef)
	if !ok {
		return exportName(decl.SymName())
	}
	if ed.PkgPath() != "" {
		return "Sngl" + exportName(ed.Name)
	}
	return exportName(ed.Name)
}

// EnumDecl declares ed as a Kotlin enum class. Each entry carries the member's
// SNGL name as its string form, because the entry's own name is upper-cased
// and a program printing a member prints it as declared on every target.
//
// An enum carrying payloads is a sealed class instead (PayloadEnumDecl), and a
// generic one is declared per instantiation passMonomorphize made.
func EnumDecl(ed *ir.EnumDef) string {
	if len(ed.TypeParams) > 0 {
		return ""
	}
	if ed.Carries() {
		return PayloadEnumDecl(ed)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "enum class %s(private val sngl: String) {\n", EnumName(ed))
	entries := make([]string, len(ed.Members))
	for i, m := range ed.Members {
		entries[i] = fmt.Sprintf("    %s(%q)", EnumEntry(m.Name), m.Name)
	}
	b.WriteString(strings.Join(entries, ",\n"))
	b.WriteString(";\n\n    override fun toString(): String = sngl\n}\n")
	return b.String()
}

func (kc *KtIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "{ }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
		if p.Type != nil {
			params[i] += ": " + IRTypeToKt(p.Type)
		}
	}
	for _, name := range codegen.BodyLocals(n.Func.Block) {
		kc = kc.WithLocal(name)
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := kc.EvalExpr(ret.Value)
			return "{ " + strings.Join(params, ", ") + " -> " + body + " }"
		}
	}
	// A handler that waits is started rather than run: the host calls it
	// with nowhere to wait, so its body is a coroutine of its own.
	if ir.BlockHasAsyncCall(n.Func.Block) && (n.Func.Return == nil || n.Func.Return.Kind == ir.TypeVoid) {
		var b strings.Builder
		b.WriteString("{ " + strings.Join(params, ", ") + " ->\n")
		for _, line := range kc.Coroutine(n.Func.Block) {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("}")
		return b.String()
	}
	var b strings.Builder
	body := kc
	if LambdaReturns(n.Func.Block) {
		inner := *kc
		inner.returnTo = kc.returnTo + "fn"
		body = &inner
		b.WriteString(body.returnTo + "@")
	}
	b.WriteString("{ " + strings.Join(params, ", ") + " ->\n")
	for _, stmt := range n.Func.Block {
		for _, line := range body.EvalStmt(stmt) {
			b.WriteString("    " + line + "\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

// Coroutine is a body that waits, started on the thread the target draws on
// where nothing waits for it -- a handler, an effect's mount -- so what it
// writes after a wait lands where every write does. A `return` in it leaves
// the coroutine.
func (kc *KtIRContext) Coroutine(block []ir.Stmt) []string {
	inner := *kc
	inner.returnTo = "start"
	out := []string{"SnglCodec.start {"}
	for _, stmt := range block {
		for _, line := range inner.EvalStmt(stmt) {
			out = append(out, "\t"+line)
		}
	}
	return append(out, "}")
}

// Labeled is a body run where it stands whose `return` leaves the body
// alone, as a handler's does: an effect's mount inlined into the bracket.
func (kc *KtIRContext) Labeled(block []ir.Stmt) []string {
	inner := *kc
	inner.returnTo = "run"
	out := []string{"run {"}
	for _, stmt := range block {
		for _, line := range inner.EvalStmt(stmt) {
			out = append(out, "\t"+line)
		}
	}
	return append(out, "}")
}

// LambdaReturns reports whether a lambda's body returns, outside a lambda of
// its own.
func LambdaReturns(stmts []ir.Stmt) bool {
	found := false
	_ = ir.Walk(stmts, func(n ir.Node) error {
		switch n.(type) {
		case *ir.Lambda:
			return ir.SkipDir
		case *ir.Return:
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
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
		receiver:      kc.receiver,
		imports:       kc.imports,
		returnTo:      kc.returnTo,
	}
}

// WithReceiver returns a context in which p, the parameter a method's body
// names its receiver by, renders as the extension's `this`.
func (kc *KtIRContext) WithReceiver(p *ir.Param) *KtIRContext {
	nc := *kc
	nc.receiver = p
	return &nc
}

// ForComponent returns a new context scoped to a component.
func (kc *KtIRContext) ForComponent(comp *ir.Component) *KtIRContext {
	return &KtIRContext{
		Ctx:           kc.Ctx.ForComponent(comp),
		EventVar:      kc.EventVar,
		IdentRewrites: kc.IdentRewrites,
		receiver:      kc.receiver,
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
		if ir.IsBuilder(t) {
			return "StringBuilder"
		}
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
			return EnumName(t.Decl)
		}
		return "String"
	case ir.TypeUnit:
		return UnitKtType(ir.UnitDeclOf(t))
	case ir.TypeFunc:
		// A function type is its parameters and its result, which is what a
		// record's event field holds and what calling one needs: `Any` cannot
		// be invoked.
		if t.Sig == nil {
			return "Any"
		}
		params := make([]string, len(t.Sig.Params))
		for i, p := range t.Sig.Params {
			params[i] = IRTypeToKt(p.Type)
		}
		ret := "Unit"
		if t.Sig.Return != nil && t.Sig.Return.Kind != ir.TypeVoid {
			ret = IRTypeToKt(t.Sig.Return)
		}
		// One that waits is a suspend function, as an async function is
		// declared: a query's fetch prop is handed `::fetchBook`.
		if t.Sig.Color == ir.ColorAsync {
			return "suspend (" + strings.Join(params, ", ") + ") -> " + ret
		}
		return "(" + strings.Join(params, ", ") + ") -> " + ret
	case ir.TypeNull:
		return "Any?"
	case ir.TypeIter:
		// Iterable<T>, not List<T>: an iter[T] is a sequence something pulls
		// from, and Iterable is the widest spelling of that -- a List is one,
		// and so is the IntProgression sngl:seq produces. It is also why a
		// list reaching an iter[T] position needs no conversion emitted.
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
		if member, payload, ok := ir.EnumLitMember(n); ok {
			return enumLitKt(n.Type, member, IRLiteralToKt(payload))
		}
		if ir.IsBuilder(n.Type) {
			return "StringBuilder()"
		}
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
			if ir.PayloadEnum(n.Type) != nil {
				return enumLitKt(n.Type, n.Member, "")
			}
			if n.Type != nil && n.Type.Kind == ir.TypeEnum && n.Type.Decl != nil {
				return EnumName(n.Type.Decl) + "." + EnumEntry(n.Member)
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
	// An i18n style is an enum, which the runtime takes as the member's name:
	// a Kotlin enum class's toString.
	str := func(i int) string { return a(i) + ".toString()" }

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
			return "I18n.numberInt(" + a(2) + ", " + a(0) + ", " + str(1) + ")"
		}
		return "I18n.getTranslator().numberInt(" + a(0) + ", " + str(1) + ")"
	case "i18n.numberFloat":
		if len(argExprs) >= 3 {
			return "I18n.numberFloat(" + a(2) + ", " + a(0) + ", " + str(1) + ")"
		}
		return "I18n.getTranslator().numberFloat(" + a(0) + ", " + str(1) + ")"
	case "i18n.date":
		if len(argExprs) >= 3 {
			return "I18n.date(" + a(2) + ", " + a(0) + ", " + str(1) + ")"
		}
		return "I18n.getTranslator().date(" + a(0) + ", " + str(1) + ")"
	case "i18n.time":
		if len(argExprs) >= 3 {
			return "I18n.time(" + a(2) + ", " + a(0) + ", " + str(1) + ")"
		}
		return "I18n.getTranslator().time(" + a(0) + ", " + str(1) + ")"
	case "i18n.datetime":
		// Wrapper params: (dt, dateStyle, timeStyle, __ctx_locale).
		if len(argExprs) >= 4 {
			return "I18n.datetime(" + a(3) + ", " + a(0) + ", " + str(1) + ", " + str(2) + ")"
		}
		return "I18n.getTranslator().datetime(" + a(0) + ", " + str(1) + ", " + str(2) + ")"
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
	// A struct the program declares is a data class whose every field has a
	// default, so its zero is the class called with none. `null` was no value
	// of it: `(facts[m] ?: null).kind` did not compile.
	if v := t.Elems[1]; v.Kind == ir.TypeStruct && !ir.IsColorStruct(v) && !ir.StringReprStruct(v) {
		if sd, ok := v.Decl.(*ir.StructDef); ok && sd.Foreign.Name == "" {
			if name := IRTypeToKt(v); name != "String" {
				return name + "()"
			}
		}
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
	return kc.NativeName(v.Foreign.Path, v.Foreign.Name), true
}

// Match writes a match statement as a when. The last arm of an exhaustive
// match keeps its patterns, so Kotlin checks the when covers its subject as
// the checker did; an else arm is the when's else.
func (kc *KtIRContext) Match(n *ir.Match, subject string, patterns, bodies [][]string) []string {
	in := kc.Indent()
	lines := []string{kc.whenHead(&n.MatchArms, subject) + " {"}
	for i := range n.Arms {
		lines = append(lines, in+kc.whenCondition(&n.MatchArms, i, patterns[i])+" -> {")
		lines = append(lines, irwalk.IndentLines(in+in, bodies[i])...)
		lines = append(lines, in+"}")
	}
	return append(lines, "}")
}

// MatchExpr writes a match expression as a when expression.
func (kc *KtIRContext) MatchExpr(n *ir.MatchExpr) string {
	subject := ""
	if n.Subject != nil {
		subject = irwalk.EvalExpr(kc, n.Subject)
	}
	arms := make([]string, len(n.Arms))
	for i, arm := range n.Arms {
		var pats []string
		for _, p := range arm.Patterns {
			pats = append(pats, irwalk.EvalExpr(kc, p))
		}
		arms[i] = kc.whenCondition(&n.MatchArms, i, pats) + " -> " + irwalk.EvalExpr(kc, arm.Value)
	}
	return "(" + kc.whenHead(&n.MatchArms, subject) + " { " + strings.Join(arms, "; ") + " })"
}

// WhenHead opens a when over m, for a platform rendering a match's arms
// itself -- Compose's in a composable.
func (kc *KtIRContext) WhenHead(m *ir.MatchArms) string {
	subject := ""
	if m.Subject != nil {
		subject = irwalk.EvalExpr(kc, m.Subject)
	}
	return kc.whenHead(m, subject) + " {"
}

// WhenArm opens arm i of a when WhenHead opened.
func (kc *KtIRContext) WhenArm(m *ir.MatchArms, i int) string {
	var pats []string
	for _, p := range m.Arms[i].Patterns {
		pats = append(pats, irwalk.EvalExpr(kc, p))
	}
	return kc.whenCondition(m, i, pats) + " -> {"
}

func (kc *KtIRContext) whenHead(m *ir.MatchArms, subject string) string {
	if m.Subject == nil {
		return "when"
	}
	return "when (" + subject + ")"
}

// whenCondition is what selects arm i: its patterns, or else. Headless, the
// conditions are joined into one, which a subjectless when takes.
func (kc *KtIRContext) whenCondition(m *ir.MatchArms, i int, patterns []string) string {
	if m.Arms[i].Else || len(patterns) == 0 || (m.Exhaustive && i == len(m.Arms)-1 && m.Subject == nil) {
		return "else"
	}
	if m.Subject == nil {
		if len(patterns) == 1 {
			return patterns[0]
		}
		return "(" + strings.Join(patterns, ") || (") + ")"
	}
	return strings.Join(patterns, ", ")
}
