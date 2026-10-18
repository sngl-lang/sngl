package android

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/internal/androidtc"
	"duckfam.us/sngl/ir"
)

// irComposeContext tracks state during IR-based Compose code generation.
type irComposeContext struct {
	kc     *kotlin.KtIRContext
	ctx    *codegen.CodegenCtx
	buf    *strings.Builder
	indent int
	// screen is the window the view draws, which the system back closes.
	screen *codegen.Screen
	// atRoot marks the window's own content, which is the node the display
	// cutout has to be kept out of.
	atRoot bool
	// combo is the selected Android toolchain (versions/SDK). Available so
	// emitters can branch where a real version difference changes output;
	// there is no such divergence between the current combos, so nothing
	// branches on it yet — it's the wired hook, not dead weight.
	combo androidtc.Combo
	// nav is the stacks the program renders, answered here rather than
	// lowered (nav.go).
	nav *androidNav
	// present are the ids a test null-tests, in a test build: an instance
	// built at run time among them is in SnglPresent while it is composed,
	// having no node of its own a tag could be on.
	present map[string]bool
	// reads are what a test reads through an instance built at run time: one
	// a call site renders is handed the entry it registers under
	// (emitInstanceRegistration).
	reads []codegen.InstanceRead
}

func (cc *irComposeContext) line(format string, args ...any) {
	fmt.Fprintf(cc.buf, "%s"+format+"\n", append([]any{strings.Repeat("    ", cc.indent)}, args...)...)
}

func (cc *irComposeContext) renderStmt(stmt ir.Stmt) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		// A schedule is not a composable. The timer primitive stays in the
		// tree so the branch around it is answered there; AnalyzeCommon reads
		// it and the LaunchedEffect it becomes is emitted with the model,
		// not here.
		if ir.IsTimerPrimitive(s.Component) {
			return
		}
		if cc.screen != nil && s == cc.screen.Node {
			cc.renderScreen(s)
			return
		}
		cc.renderNode(s)
	case *ir.If:
		cc.renderIf(s)
	case *ir.For:
		cc.renderFor(s)
	case *ir.SlotInst:
		cc.renderSlotInst(s)
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			cc.renderStmt(child)
		}
	case *ir.LocalVar, *ir.Assign:
		// A local the lowering synthesized -- the counter passIndexedIter puts
		// beside a loop over a held sequence -- which the loop's body reads;
		// any other write has no rendering.
		if counterStmt(s) {
			for _, l := range cc.kc.EvalStmt(s) {
				cc.line("%s", l)
			}
		}
	case *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		// Imperative stmts have no Compose rendering.
	default:
		panic(fmt.Sprintf("android.renderStmt: unhandled ir.Stmt %T", s))
	}
}

func (cc *irComposeContext) renderIf(s *ir.If) {
	// Defer the conditional syntax to the Kotlin language driver.
	cc.line("%s", cc.kc.IfHead(s, cc.kc.EvalExpr(s.Cond)))
	cc.indent++
	for _, child := range s.Body {
		cc.renderStmt(child)
	}
	cc.indent--
	if len(s.Else) > 0 {
		cc.line("%s", cc.kc.ElseHead())
		cc.indent++
		for _, child := range s.Else {
			cc.renderStmt(child)
		}
		cc.indent--
	}
	cc.line("%s", cc.kc.BlockEnd())
}

func (cc *irComposeContext) renderFor(s *ir.For) {
	iterExpr := cc.kc.EvalExpr(s.Iter)

	// Defer the loop header to the Kotlin language driver so loop semantics
	// (single/two-var ordering, map iteration) live in one place rather than
	// being re-implemented per platform.
	loopKC := cc.kc
	if s.Key != "" && s.Key != "_" {
		loopKC = loopKC.WithLocal(s.Key)
	}
	if s.Value != "" && s.Value != "_" {
		loopKC = loopKC.WithLocal(s.Value)
	}
	savedKC := cc.kc
	cc.kc = loopKC

	cc.line("%s", cc.kc.ForHead(s, iterExpr))
	cc.indent++
	for _, child := range s.Body {
		cc.renderStmt(child)
	}
	cc.indent--
	cc.line("%s", cc.kc.BlockEnd())

	cc.kc = savedKC
}

func (cc *irComposeContext) renderNode(n *ir.NodeInst) {
	if n.Component != nil {
		switch n.Component.Builtin {
		case ir.BuiltinNavStack:
			cc.renderNavStack(n)
			return
		}
		if n.Component.Intrinsic == intrinsicNS+"Link" {
			cc.renderNavLink(n)
			return
		}
	}
	if isEffectNode(n) {
		cc.renderEffect(n)
		return
	}
	if ir.IsShapeContainer(n) {
		cc.renderCanvas(n)
		return
	}
	if n.Component != nil && cc.isUserComponent(n.Component) {
		cc.renderUserComponent(n)
		return
	}
	if comp, composable := composeIntrinsic(n); comp != nil {
		// A flow is one Text whose argument is built from the span tree
		// written inside it, and a span is a piece of that value rather than
		// a composable -- neither of which the generic emitter can say, since
		// it reads a declaration's props and children and nothing else. Both
		// tags are answered before it. A Span reaching here is one written
		// outside a flow, which the family's own membership rule refuses.
		switch composable {
		case flowTag:
			cc.renderFlow(n, comp)
			return
		case spanTag:
			return
		}
		cc.renderIntrinsic(n, comp, composable)
		return
	}
	if n.Component != nil {
		if !codegen.DeclinesNode(n.Component) {
			// A user component the optimizer eliminated — its body was empty,
			// so there is no composable to call and nothing to draw. Lowering
			// drops such a node now, so this is unreachable through a compile;
			// it stays because the alternative below is a panic, and a panic is
			// the wrong answer for a program that is merely empty.
			return
		}
	}
	// A library declaration with no android override and no body here: another
	// platform's element, or a component this one never implemented.
	cc.ctx.Fail(codegen.UnimplementedNode(n.Component, n.AST, n.Name, "android"))
}

func (cc *irComposeContext) isUserComponent(comp *ir.Component) bool {
	return !comp.IsNative() && slices.Contains(cc.ctx.Pkg.Components, comp)
}

func (cc *irComposeContext) renderUserComponent(n *ir.NodeInst) {
	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			propVal := codegen.NodeProp(n, p.Name)
			if propVal != nil {
				args = append(args, p.Name+" = "+cc.kc.EvalExpr(propVal))
			}
		}
	}
	if n.Component != nil {
		args = append(args, cc.populationArgs(n)...)
		args = append(args, cc.handlerArgs(n)...)
	}
	for _, r := range cc.reads {
		if r.ID != n.ID {
			continue
		}
		entry := fmt.Sprintf("%q", n.ID)
		if k := n.Prop(ir.CopyProp); k != nil && r.Keyed {
			entry = kotlin.PresentCopyEntry(n.ID, cc.kc.EvalExpr(k))
		}
		args = append(args, kotlin.InstanceKeyParam+" = "+entry)
		break
	}
	if cc.present[n.ID] {
		// One copy is registered under its key, and again when a reused
		// instance is handed another.
		key, entry := "Unit", fmt.Sprintf("%q", n.ID)
		if k := n.Prop(ir.CopyProp); k != nil {
			key = cc.kc.EvalExpr(k)
			entry = kotlin.PresentCopyEntry(n.ID, key)
		}
		cc.line("%s(%s) {", cc.kc.Name(ktDisposableEffect), key)
		cc.line("    val __token = Any()")
		cc.line("    %s[__token] = %s", kotlin.PresentRegistry, entry)
		cc.line("    onDispose { %s.remove(__token) }", kotlin.PresentRegistry)
		cc.line("}")
	}
	cc.line("%s(%s)", exportName(n.Name), strings.Join(args, ", "))
}

// --- Helpers ---

// nodeModifier is the Modifier a node is drawn with: what its `modifier`
// prop holds -- the Style an override translated in SNGL, nil where it has
// none -- after the test tag and presence a test build reads, then tail.
func (cc *irComposeContext) nodeModifier(n *ir.NodeInst, styled ir.Expr, tail string) string {
	empty := cc.kc.Name(ktModifier)
	parts := append([]string{empty}, cc.testModifiers(n)...)
	head := strings.Join(parts, ".")
	if styled == nil {
		return head + tail
	}
	s := cc.kc.EvalExpr(styled)
	switch {
	case s == empty:
	case len(parts) > 1:
		head += ".then(" + s + ")"
	case tail != "" && !chainable(s):
		head = "(" + s + ")"
	default:
		head = s
	}
	return head + tail
}

// chainable reports whether a call may be appended to the Kotlin expression
// s as it stands: no space outside its brackets, so it is not an `if` or an
// operator.
func chainable(s string) bool {
	depth := 0
	quoted := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted:
			if c == '\\' {
				i++
			} else if c == '"' {
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ' ' && depth == 0:
			return false
		}
	}
	return true
}

// testModifiers are the Modifier calls a test build puts on a node: the test
// tag of a user-authored #id (not the synthetic __nN ids passReactivity
// assigns) and the presence of the instance a node is a root of.
func (cc *irComposeContext) testModifiers(n *ir.NodeInst) []string {
	var parts []string
	if id := userTestTag(n); id != "" {
		parts = append(parts, fmt.Sprintf("%s(%q)", cc.kc.Name(ktTestTag), id))
		// The key is read where the modifier is built: the semantics block runs
		// later, by when a loop's counter has moved on.
		if key := n.Prop(ir.CopyProp); key != nil {
			parts = append(parts, fmt.Sprintf("then(%s.let { __key -> %s.%s { this[%s] = __key } })", cc.kc.EvalExpr(key), cc.kc.Name(ktModifier), cc.kc.Name(ktSemantics), kotlin.CopySemanticsKey))
		}
	}
	// A root of a spliced instance a test null-tests counts the instance in
	// PresentRegistry while it is composed, as an instance built at run time
	// does at its call site.
	for _, p := range n.Props {
		id, ok := codegen.InstanceRootID(p.Name)
		if !ok || !cc.present[id] {
			continue
		}
		entry := fmt.Sprintf("%q", id)
		if lit, isLit := p.Value.(*ir.Literal); !isLit || lit.Value != "" {
			entry = kotlin.PresentCopyEntry(id, cc.kc.EvalExpr(p.Value))
		}
		parts = append(parts, fmt.Sprintf("then((%s).let { __entry -> %s.%s { %s(__entry) { val __token = Any(); %s[__token] = __entry; onDispose { %s.remove(__token) } }; %s } })",
			entry, cc.kc.Name(ktModifier), cc.kc.Name(ktComposed), cc.kc.Name(ktDisposableEffect), kotlin.PresentRegistry, kotlin.PresentRegistry, cc.kc.Name(ktModifier)))
	}
	return parts
}

// styleValue is a run's style field as a Compose argument: a measurement is
// one number of dp, an em being emDp of them (codegen.MeasurementPx), to which
// the caller appends the unit its argument takes.
func (cc *irComposeContext) styleValue(e ir.Expr) string {
	if e != nil && ir.UnitDeclOf(e.ExprType()) != nil {
		if px, ok := codegen.MeasurementPx(e, emDp); ok {
			return ir.FormatUnitMagnitude(px)
		}
	}
	val := cc.kc.EvalExpr(e)
	if e != nil {
		if ud := ir.UnitDeclOf(e.ExprType()); ud != nil && !ud.IsSingleBase() {
			return codegen.MeasurementSum(ud, "("+val+")", func(b string) string { return b }, ir.FormatUnitMagnitude(emDp))
		}
	}
	return val
}

// emDp is an em on this platform: Material's body text is 16sp.
const emDp = 16

// userTestTag returns n.ID when it's a user-authored #id and worth
// surfacing as a Compose testTag. Synthetic __nN ids assigned by
// passReactivity are skipped — they aren't addressable from test
// source anyway.
func userTestTag(n *ir.NodeInst) string {
	if n == nil || n.ID == "" {
		return ""
	}
	if strings.HasPrefix(n.ID, "__n") {
		return ""
	}
	return n.ID
}

// composeColorExpr converts an evaluated SNGL color value to a Compose Color
// expression. SNGL `color` values lower to a `Color(r=.., g=.., b=.., a=..)`
// struct (0..255 ints); hex string literals ("#rrggbb") are also accepted.
// Compose's ComposeColor has an Int (0..255) constructor:
// ComposeColor(red, green, blue, alpha). Anything else is assumed to be a
// runtime Color-struct expression and is built from its r/g/b/a fields.
func composeColorExpr(kc *kotlin.KtIRContext, val string) string {
	color := composeColor(kc)
	val = strings.TrimSpace(val)
	// Named-arg struct literal: Color(r = 102, g = 102, b = 102, a = 255)
	if rest, ok := strings.CutPrefix(val, "Color("); ok && strings.HasSuffix(rest, ")") {
		inner := strings.TrimSuffix(rest, ")")
		fields := map[string]string{"r": "0", "g": "0", "b": "0", "a": "255"}
		for part := range strings.SplitSeq(inner, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			fields[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
		return fmt.Sprintf("%s(red = %s, green = %s, blue = %s, alpha = %s)",
			color, fields["r"], fields["g"], fields["b"], fields["a"])
	}
	// Hex string literal: "#rrggbb" or "#rgb".
	if r, g, b, ok := parseHexColorLiteral(val); ok {
		return fmt.Sprintf("%s(red = %d, green = %d, blue = %d, alpha = 255)", color, r, g, b)
	}
	// Runtime Color-struct expression: build from its fields, reading the
	// expression once. Interpolating it four times meant four calls per
	// iteration of whatever loop the widget sits in -- `bgOf(entry.tone())`
	// in the calculator's keypad -- and no compiler can undo that, because
	// nothing here promises the call is pure. `let` binds it instead.
	return fmt.Sprintf("(%s).let { %s(it.r, it.g, it.b, it.a) }", val, color)
}

// parseHexColorLiteral parses a Kotlin string literal holding a hex color
// ("#abc" / "#aabbcc"); returns the 0..255 channel values.
func parseHexColorLiteral(val string) (r, g, b int, ok bool) {
	if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
		return 0, 0, 0, false
	}
	s := val[1 : len(val)-1]
	if len(s) == 0 || s[0] != '#' {
		return 0, 0, 0, false
	}
	hex := s[1:]
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseInt(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

// --- Declared Compose intrinsics ---

// intrinsicNS prefixes every intrinsic id this platform answers to, so a
// primitive declared here can never collide with a stdlib intrinsic or with
// another platform's.
const intrinsicNS = "android:"

// nativeScheme is the language whose native widgets this platform renders: a
// `#[kt.native]` component is a composable, called by the name it gives.
const nativeScheme = "kotlin"

// composeIntrinsic returns the composable name behind n when n is a component
// marked #[intrinsic] in this platform's namespace, or a composable a
// `#[kt.native]` mark names -- the form a program writes for one the platform
// never heard of. A stdlib component whose android override is written in the
// extension form is inlined into its caller at lower time, so what reaches
// codegen is the primitive the override named.
func composeIntrinsic(n *ir.NodeInst) (*ir.Component, string) {
	if n.Component == nil {
		return nil, ""
	}
	if f := n.Component.Foreign; n.Component.IsNative() && f.Scheme == nativeScheme {
		return n.Component, f.Name
	}
	name, ok := strings.CutPrefix(n.Component.Intrinsic, intrinsicNS)
	if !ok || name == "" {
		return nil, ""
	}
	return n.Component, name
}

// renderIntrinsic emits a declared composable from its declaration:
// `composable` is the intrinsic id past the namespace, each declared prop is
// the Compose argument of that name in declaration order, and children are a
// trailing lambda. A func-typed prop is a callback, emitted as the Kotlin
// lambda Compose takes there.
//
// Two prop names are this emitter's own: `modifier` is extended with what a
// test build tags the node with, and `then` appends further Modifier values to
// it. Every other prop, a host value a `#[kt.native]` const names included, is
// written as the expression it is.
func (cc *irComposeContext) renderIntrinsic(n *ir.NodeInst, comp *ir.Component, composable string) {
	if comp.IsNative() && comp.Foreign.Path != "" {
		composable = cc.kc.NativeName(comp.Foreign.Path, comp.Foreign.Name)
	}
	var args []string
	for _, p := range comp.Props {
		switch {
		case p.Name == "then":
		case p.Name == "modifier":
			args = append(args, "modifier = "+cc.intrinsicModifier(n))
		default:
			v := codegen.NodeProp(n, p.Name)
			if v == nil {
				continue
			}
			if lam, ok := v.(*ir.Lambda); ok {
				args = append(args, cc.lambdaArg(p.Name, lam, funcArity(p.Type)))
				continue
			}
			args = append(args, p.Name+" = "+cc.kc.EvalExpr(v))
		}
	}
	args = append(args, cc.slotArgs(n, comp)...)
	call := fmt.Sprintf("%s(%s)", composable, strings.Join(args, ", "))
	if comp.ChildrenType == nil {
		cc.line("%s", call)
		return
	}
	cc.line("%s {", call)
	cc.indent++
	// Nothing below here is the window's content.
	outerRoot := cc.atRoot
	cc.atRoot = false
	defer func() { cc.atRoot = outerRoot }()
	for _, child := range n.Children {
		cc.renderStmt(child)
	}
	cc.indent--
	cc.line("}")
}

// slotArgs emits each named slot a call site populated as the composable
// lambda Compose takes in its place -- `label = { Text("Name") }`. A value
// prop cannot say that: what Compose takes there is content, and content is a
// tree the override writes like any other.
//
// A slot whose body renders nothing is left out rather than passed empty,
// because Compose reads the two differently: an absent `label` has no floating
// label at all, while `label = {}` still reserves its space and notches the
// outline. That empty body is what an override conditional on a prop folds to
// when the prop is a constant -- a condition only answerable at run time keeps
// the argument, and the widget then has a caption that may be empty.
func (cc *irComposeContext) slotArgs(n *ir.NodeInst, comp *ir.Component) []string {
	var args []string
	for _, slot := range comp.Slots {
		if slot.Rest {
			continue
		}
		sc := n.Slots[slot.Name]
		if sc == nil {
			continue
		}
		body := cc.renderNested(sc.Body)
		if body == "" {
			continue
		}
		args = append(args, slot.Name+" = {\n"+body+strings.Repeat("    ", cc.indent)+"}")
	}
	return args
}

// renderNested renders stmts one level in, into a buffer of their own. The
// lambda is never the window's own content, so nothing inside it asks for the
// display cutout's inset.
func (cc *irComposeContext) renderNested(stmts []ir.Stmt) string {
	savedBuf, savedRoot := cc.buf, cc.atRoot
	cc.buf, cc.atRoot = &strings.Builder{}, false
	cc.indent++
	for _, stmt := range stmts {
		cc.renderStmt(stmt)
	}
	cc.indent--
	body := cc.buf.String()
	cc.buf, cc.atRoot = savedBuf, savedRoot
	return body
}

// intrinsicModifier is the Modifier a declared composable is drawn with: its
// `modifier` prop, then its `then` values -- Modifiers built from the calls
// android.sngl declares, where no Style field names one.
func (cc *irComposeContext) intrinsicModifier(n *ir.NodeInst) string {
	tail := cc.thenChain(codegen.NodeProp(n, "then"))
	if cc.atRoot {
		// A phone's window is not a rectangle: a status bar sits over the top
		// of it and a cutout over part of that. Last in the chain, so it
		// insets the content and not the background -- the colour still
		// reaches the edges and only what is drawn inside is kept clear.
		tail += "." + cc.kc.Name(ktSafeDrawingPadding) + "()"
	}
	return cc.nodeModifier(n, codegen.NodeProp(n, "modifier"), tail)
}

// thenChain appends a `then` prop's Modifiers to a chain: one `.then(m)` per
// element of a list literal, and a fold over any other list.
func (cc *irComposeContext) thenChain(v ir.Expr) string {
	switch v := v.(type) {
	case nil:
		return ""
	case *ir.ListLit:
		var b strings.Builder
		for _, e := range v.Elems {
			b.WriteString(".then(" + cc.kc.EvalExpr(e) + ")")
		}
		return b.String()
	}
	return ".then((" + cc.kc.EvalExpr(v) + ").fold(" + cc.kc.Name(ktModifier) + ") { m, next -> m.then(next) })"
}

// lambdaArg emits a func-typed prop as the Kotlin lambda Compose takes in its
// place. Compose spells a callback as an argument, so the body is written where
// the argument goes and the whole call stays one expression even when the body
// spans lines.
//
// Every parameter the callback takes is listed, arity of them, `_` where the
// body reads none and where the handler wrote fewer than the callback passes:
// with none listed, Kotlin names a lone parameter `it`, which hides a
// program's own `it` in the body.
//
// A prop that *returns* something is written as Kotlin's other function
// literal, the anonymous `fun`, because the lambda form cannot say either half
// of what such an argument needs. `return` in a lambda is a non-local return
// from the enclosing function, and the value's type is left to inference --
// where Compose wants a Float and SNGL's `float` is a Double, inference gives
// it the wrong one silently. `fun(): Float { return … }` states both.
func (cc *irComposeContext) lambdaArg(name string, lam *ir.Lambda, arity int) string {
	if lam.Func == nil || len(lam.Func.Block) == 0 {
		return name + " = {}"
	}
	kc := cc.kc
	for _, p := range lam.Func.Params {
		kc = kc.WithLocal(p.Name)
	}
	saved := cc.kc
	cc.kc = kc
	var body []string
	for _, stmt := range lam.Func.Block {
		body = append(body, cc.kc.EvalStmt(stmt)...)
	}
	cc.kc = saved
	if len(body) == 0 {
		return name + " = {}"
	}
	// A `return` in a lambda would leave the composable it is written in,
	// which Kotlin refuses; in an anonymous function it leaves the handler.
	if lam.Func.Return != nil || kotlin.LambdaReturns(lam.Func.Block) {
		return cc.anonFuncArg(name, lam, body)
	}

	var params []string
	for _, p := range lam.Func.Params {
		if !readsParam(lam.Func.Block, p) {
			params = append(params, "_")
			continue
		}
		params = append(params, p.Name)
	}
	for len(params) < arity {
		params = append(params, "_")
	}

	outer := strings.Repeat("    ", cc.indent)
	var b strings.Builder
	b.WriteString(name + " = {")
	if len(params) > 0 {
		b.WriteString(" " + strings.Join(params, ", ") + " ->")
	}
	for _, line := range body {
		b.WriteString("\n" + outer + "    " + line)
	}
	b.WriteString("\n" + outer + "}")
	return b.String()
}

// anonFuncArg emits a value-returning func-typed prop as Kotlin's anonymous
// function. Every parameter is named and typed here, unlike the lambda form:
// an anonymous `fun` may not drop one, and the declaration holds the types
// that make the signature Compose's rather than whatever inference reached.
func (cc *irComposeContext) anonFuncArg(name string, lam *ir.Lambda, body []string) string {
	params := make([]string, 0, len(lam.Func.Params))
	for _, p := range lam.Func.Params {
		params = append(params, p.Name+": "+kotlin.IRTypeToKt(p.Type))
	}
	outer := strings.Repeat("    ", cc.indent)
	var b strings.Builder
	ret := "Unit"
	if lam.Func.Return != nil {
		ret = kotlin.IRTypeToKt(lam.Func.Return)
	}
	fmt.Fprintf(&b, "%s = fun(%s): %s {", name, strings.Join(params, ", "), ret)
	for _, line := range body {
		b.WriteString("\n" + outer + "    " + line)
	}
	b.WriteString("\n" + outer + "}")
	return b.String()
}

// readsParam reports whether stmts reference p. The parameter is declared only
// then: what a callback's lambda receives is named for the body's sake, and a
// name declared over an unused value would shadow whatever else carries it —
// a call site binding a state var of the same name is exactly that case.
func readsParam(stmts []ir.Stmt, p *ir.Param) bool {
	found := false
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok && id.Sym == p {
			found = true
		}
		return nil
	})
	return found
}

// colorExpr is a color-valued style field as a Compose Color. A ternary is
// mapped per branch: each literal becomes its ComposeColor, where evaluating
// the whole would name the SNGL `Color` record, which this target declares
// only when a program stores one.
func (cc *irComposeContext) colorExpr(e ir.Expr) string {
	if t, ok := e.(*ir.Ternary); ok {
		return fmt.Sprintf("(if (%s) %s else %s)", cc.kc.EvalExpr(t.Cond), cc.colorExpr(t.Then), cc.colorExpr(t.Else))
	}
	return composeColorExpr(cc.kc, cc.kc.EvalExpr(e))
}

// counterStmt reports whether s declares or steps a local the lowering
// synthesized.
func counterStmt(s ir.Stmt) bool {
	var sym ir.Symbol
	switch n := s.(type) {
	case *ir.LocalVar:
		sym = n.Sym
	case *ir.Assign:
		if id, ok := n.Target.(*ir.Ident); ok {
			sym = id.Sym
		}
	}
	v, ok := sym.(*ir.Var)
	return ok && v != nil && v.Synthesized
}

// funcArity is how many parameters a value of the function type t is handed,
// or of the function an optional t holds.
func funcArity(t *ir.Type) int {
	if t != nil && t.Kind == ir.TypeOption && len(t.Elems) == 1 {
		t = t.Elems[0]
	}
	if t == nil || t.Kind != ir.TypeFunc || t.Sig == nil {
		return 0
	}
	return len(t.Sig.Params)
}
