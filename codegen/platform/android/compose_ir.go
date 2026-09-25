package android

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irComposeContext tracks state during IR-based Compose code generation.
type irComposeContext struct {
	kc     *kotlin.KtIRContext
	ctx    *codegen.CodegenCtx
	buf    *strings.Builder
	indent int
	// parentAxis is "Row" or "Column" when the node being rendered sits in
	// one, and "" at the top of a composable. weight() lives on those two
	// scopes, and which one it is decides which axis it grows.
	parentAxis string
	// atRoot marks the window's own content, which is the node the display
	// cutout has to be kept out of.
	atRoot bool
	// combo is the selected Android toolchain (versions/SDK). Available so
	// emitters can branch where a real version difference changes output;
	// there is no such divergence between the current combos, so nothing
	// branches on it yet — it's the wired hook, not dead weight.
	combo androidtc.Combo
	// widgetSeq names per-widget local state (e.g. a select's `expanded`)
	// uniquely within a composable so multiple instances don't collide.
	widgetSeq int
}

func (cc *irComposeContext) line(format string, args ...any) {
	fmt.Fprintf(cc.buf, "%s"+format+"\n", append([]any{strings.Repeat("    ", cc.indent)}, args...)...)
}

func (cc *irComposeContext) renderStmt(stmt ir.Stmt) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		if ir.IsWindowNode(s) {
			panic(fmt.Sprintf("android: unexpected nested Window in compose tree: %#v", s))
		}
		// A schedule is not a composable. The timer primitive stays in the
		// tree so the branch around it is answered there; AnalyzeCommon reads
		// it and the LaunchedEffect it becomes is emitted with the model,
		// not here.
		if ir.IsTimerPrimitive(s.Component) {
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
	case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		// Imperative stmts have no Compose rendering.
	case *ir.ContextProvider:
		panic(fmt.Sprintf("android: ContextProvider should be lowered before compose emission: %#v", s))
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
		if cc.renderStdlibComposable(n) {
			return
		}
		if !n.Component.Stdlib {
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

// emitInputHandlerCall lowers one @change handler attached to select, the
// last composable whose body is written here. The handler body runs inside
// Compose's onClick lambda with the chosen option bound to `newValueVar`;
// the handler's event param (if any) is materialized as a tiny data holder so
// `e.value` reads return the same Kotlin string. A handler with no event param
// drops the alias.
func emitInputHandlerCall(cc *irComposeContext, n *ir.NodeInst, eventName, newValueVar string) {
	h := codegen.NodeHandler(n, eventName)
	if h == nil || h.Func == nil {
		return
	}
	// Each handler body runs in its own block so two handlers with
	// the same event-param name (e.g. both `@input(e)` and
	// `@change(e)`) don't collide on a single Kotlin scope.
	cc.line("run {")
	cc.indent++
	if len(h.Func.Params) > 0 {
		param := h.Func.Params[0]
		cc.line("val %s = SnglInputEvent(%s)", param.Name, newValueVar)
	}
	for _, stmt := range h.Func.Block {
		for _, line := range cc.kc.EvalStmt(stmt) {
			cc.line("%s", line)
		}
	}
	cc.indent--
	cc.line("}")
}

func (cc *irComposeContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(cc.ctx.Pkg.Components, comp)
}

// declaredName is the name the component was declared under, which is what the
// switch below is written against. NodeInst.Name is the call site's spelling
// instead, so a program reaching the same component through a qualified import
// writes `ui.input` and misses every case.
func declaredName(n *ir.NodeInst) string {
	if n.Component != nil && n.Component.Name != "" {
		return n.Component.Name
	}
	return n.Name
}

// handWrittenComposables names what renderStdlibComposable has a case for.
// Kept beside it so the one other question asked about that set -- whether a
// node reaches emitInputHandlerCall, and so needs the event holder declared --
// reads the same list rather than a copy of it.
var handWrittenComposables = []string{"select"}

func handWrittenComposable(n *ir.NodeInst) bool {
	return slices.Contains(handWrittenComposables, declaredName(n))
}

// renderStdlibComposable emits the components whose android body is still
// written here rather than declared in codegen/platform/android. It reports
// whether it recognised n; an unrecognised one is a missing override, which
// renderNode panics on.
//
// Each is here for a reason a declaration cannot yet state, and the comment on
// its `component ui.X()` stub in widgets.sngl is where that reason is written
// down.
func (cc *irComposeContext) renderStdlibComposable(n *ir.NodeInst) bool {
	style := cc.buildModifier(n)

	switch declaredName(n) {
	case "select":
		// Two-way `:value` dropdown. The binding pass synthesizes the
		// write-back as a @change handler (like `input`), so selecting an
		// option calls it with the chosen label. `expanded` is per-instance
		// local state controlling the menu.
		optionsExpr := "listOf<String>()"
		if o := codegen.NodeProp(n, "options"); o != nil {
			optionsExpr = cc.kc.EvalExpr(o)
		}
		valueExpr := "\"\""
		if v := codegen.NodeProp(n, "value"); v != nil {
			valueExpr = cc.kc.EvalExpr(v)
		}
		placeholder, _ := codegen.IRLiteralString(codegen.NodeProp(n, "placeholder"))
		cc.widgetSeq++
		exp := fmt.Sprintf("expanded%d", cc.widgetSeq)
		cc.line("var %s by remember { mutableStateOf(false) }", exp)
		cc.line("Box(%s) {", style)
		cc.indent++
		cc.line("OutlinedButton(onClick = { %s = true }) {", exp)
		cc.indent++
		if placeholder != "" {
			cc.line("Text(if (%s.isNotEmpty()) %s else %q)", valueExpr, valueExpr, placeholder)
		} else {
			cc.line("Text(%s)", valueExpr)
		}
		cc.indent--
		cc.line("}")
		cc.line("DropdownMenu(expanded = %s, onDismissRequest = { %s = false }) {", exp, exp)
		cc.indent++
		cc.line("%s.forEach { opt ->", optionsExpr)
		cc.indent++
		cc.line("DropdownMenuItem(text = { Text(opt) }, onClick = {")
		cc.indent++
		// Two-way `:value` write-back (the bound var is an assignable lvalue),
		// then any user @change handler, then close the menu.
		cc.line("%s = opt", valueExpr)
		emitInputHandlerCall(cc, n, "change", "opt")
		cc.line("%s = false", exp)
		cc.indent--
		cc.line("})")
		cc.indent--
		cc.line("}")
		cc.indent--
		cc.line("}")
		cc.indent--
		cc.line("}")

	default:
		return false
	}
	return true
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
	cc.line("%s(%s)", exportName(n.Name), strings.Join(args, ", "))
}

// --- Helpers ---

// buildModifierRaw returns just the modifier expression (e.g., "Modifier.padding(16.dp)")
// without the "modifier = " prefix. A user-authored #id (not the
// synthetic __nN ids from passReactivity) attaches a `.testTag("<id>")`
// so Compose UI tests can locate the node via `onNodeWithTag`.
func (cc *irComposeContext) buildModifierRaw(n *ir.NodeInst) string {
	return cc.modifierRaw(n, "style")
}

// modifierRaw is buildModifierRaw over a named style prop: a declared
// intrinsic names its props after the Compose arguments it emits, so the
// Style behind its Modifier is not called "style".
func (cc *irComposeContext) modifierRaw(n *ir.NodeInst, styleProp string) string {
	return cc.modifierRawExcept(n, styleProp, nil)
}

// modifierRawExcept is modifierRaw with some style fields left out, for a
// widget that answers them another way.
func (cc *irComposeContext) modifierRawExcept(n *ir.NodeInst, styleProp string, skip map[string]bool) string {
	parts := []string{"Modifier"}
	if id := userTestTag(n); id != "" {
		parts = append(parts, fmt.Sprintf("testTag(%q)", id))
	}
	for _, sf := range codegen.NodeStyleFieldsOf(n, styleProp) {
		if skip[sf.Name] {
			continue
		}
		if sf.Name == "flex" {
			if mod := cc.flexModifier(cc.styleValue(sf.Value)); mod != "" {
				parts = append(parts, mod)
			}
			continue
		}
		if mod := composeModifier(sf.Name, cc.styleValue(sf.Value)); mod != "" {
			parts = append(parts, mod)
		}
	}
	return strings.Join(parts, ".")
}

// styleValue is a style field as the Compose argument reading it wants it.
//
// A measurement is the one kind that cannot go through EvalExpr: `8px` is a
// unit value, which Kotlin spells as the `Measurement` data class this target
// emits, and every place a Style field lands appends `.dp` or `.sp` to it.
// `Measurement(px = 8.0).dp` names no extension Compose declares, so a padding
// or a font size written with its unit failed to compile while the same number
// written bare worked -- which is why no fixture had caught it.
//
// Reduced to its magnitude here rather than given a `.dp` of its own in
// Kotlin: `px` is the only base this platform reads, as it is everywhere else
// android answers a measurement, and a helper would make the other four look
// answered.
func (cc *irComposeContext) styleValue(e ir.Expr) string {
	if lit, ok := e.(*ir.Literal); ok {
		if mag, _, ok := ir.UnitMagnitude(lit); ok {
			return ir.FormatUnitMagnitude(mag)
		}
	}
	return cc.kc.EvalExpr(e)
}

// flexModifier is what a child asking for a share of its parent becomes.
//
// Inside a Row or a Column that is weight(), which is what makes a keypad fill
// the screen instead of every button sizing to its own label -- the row of
// `C +/- % /` came out wider than the rows under it, because "+/-" is.
//
// At the top of a composable there is no parent to take a share of and
// weight() is not even in scope, so the same request means the screen.
func (cc *irComposeContext) flexModifier(val string) string {
	if val == "" || val == "0" || val == "0.0" {
		return ""
	}
	switch cc.parentAxis {
	case "Row":
		// weight() grows the main axis only. CSS stretches a flex child
		// across the other one by default, which is why the rows of the
		// keypad divided the height while the keys in them stayed the height
		// of their own labels.
		return fmt.Sprintf("weight(%s).fillMaxHeight()", ktFloat(val))
	case "Column":
		return fmt.Sprintf("weight(%s).fillMaxWidth()", ktFloat(val))
	}
	return "fillMaxSize()"
}

// declaresProp reports whether comp declares a prop of this name.
func declaresProp(comp *ir.Component, name string) bool {
	if comp == nil {
		return false
	}
	for _, p := range comp.Props {
		if p.Name == name {
			return true
		}
	}
	return false
}

// cornerShapeExpr renders a Style's borderRadius as the shape a Material
// widget takes, or "" when it asked for none.
func (cc *irComposeContext) cornerShapeExpr(n *ir.NodeInst, styleProp string) string {
	for _, sf := range codegen.NodeStyleFieldsOf(n, styleProp) {
		if sf.Name != "borderRadius" {
			continue
		}
		v := cc.styleValue(sf.Value)
		if v == "" || v == "0" || v == "0.0" {
			continue
		}
		cc.kc.RequireImport("androidx.compose.foundation.shape.RoundedCornerShape")
		return fmt.Sprintf("RoundedCornerShape(%s.dp)", v)
	}
	return ""
}

// buttonColorsExpr renders a Style's background and foreground as the colour
// set a Material button takes, or "" when it asked for neither.
func (cc *irComposeContext) buttonColorsExpr(n *ir.NodeInst, styleProp string) string {
	var args []string
	for _, sf := range codegen.NodeStyleFieldsOf(n, styleProp) {
		switch sf.Name {
		case "background":
			args = append(args, "containerColor = "+composeColorExpr(cc.kc.EvalExpr(sf.Value)))
		case "color":
			args = append(args, "contentColor = "+composeColorExpr(cc.kc.EvalExpr(sf.Value)))
		}
	}
	if len(args) == 0 {
		return ""
	}
	return "ButtonDefaults.buttonColors(" + strings.Join(args, ", ") + ")"
}

func (cc *irComposeContext) buildModifier(n *ir.NodeInst) string {
	return "modifier = " + cc.buildModifierRaw(n)
}

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

// The three font fields beside fontSize go through the same readers a run of
// rich text uses, because a Style says them the same way: an enum member, not
// a string. `fontWeight` was compared against the string `"bold"` and so never
// matched an enum at all, and the other two had no case -- so a heading came
// out unemphasized on this target and a quotation upright, the same defect
// fyne carried until this branch.
func (cc *irComposeContext) textStyleExpr(n *ir.NodeInst, styleProp string) string {
	styleFields := codegen.NodeStyleFieldsOf(n, styleProp)
	if styleFields == nil {
		return ""
	}
	var styleParts []string
	for _, sf := range styleFields {
		val := cc.styleValue(sf.Value)
		switch sf.Name {
		case "fontSize":
			styleParts = append(styleParts, fmt.Sprintf("fontSize = %s.sp", val))
		case "fontWeight":
			if w := composeWeight(enumMember(sf.Value)); w != "" {
				styleParts = append(styleParts, "fontWeight = "+w)
			}
		case "fontStyle":
			if st := composeSlant(enumMember(sf.Value)); st != "" {
				styleParts = append(styleParts, "fontStyle = "+st)
				cc.kc.RequireImport("androidx.compose.ui.text.font.FontStyle")
			}
		case "fontFamily":
			if name, ok := codegen.IRLiteralString(sf.Value); ok && name != "" {
				styleParts = append(styleParts, "fontFamily = "+fontFamilyExpr(name))
				cc.kc.RequireImport("androidx.compose.ui.text.font.FontFamily")
			}
		case "textAlign":
			switch val {
			case `"center"`:
				styleParts = append(styleParts, "textAlign = TextAlign.Center")
			case `"right"`:
				styleParts = append(styleParts, "textAlign = TextAlign.End")
			}
		case "color":
			styleParts = append(styleParts, "color = "+composeColorExpr(val))
		}
	}
	if len(styleParts) == 0 {
		return ""
	}
	return "TextStyle(" + strings.Join(styleParts, ", ") + ")"
}

// composeColorExpr converts an evaluated SNGL color value to a Compose Color
// expression. SNGL `color` values lower to a `Color(r=.., g=.., b=.., a=..)`
// struct (0..255 ints); hex string literals ("#rrggbb") are also accepted.
// Compose's ComposeColor has an Int (0..255) constructor:
// ComposeColor(red, green, blue, alpha). Anything else is assumed to be a
// runtime Color-struct expression and is built from its r/g/b/a fields.
func composeColorExpr(val string) string {
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
		return fmt.Sprintf("ComposeColor(red = %s, green = %s, blue = %s, alpha = %s)",
			fields["r"], fields["g"], fields["b"], fields["a"])
	}
	// Hex string literal: "#rrggbb" or "#rgb".
	if r, g, b, ok := parseHexColorLiteral(val); ok {
		return fmt.Sprintf("ComposeColor(red = %d, green = %d, blue = %d, alpha = 255)", r, g, b)
	}
	// Runtime Color-struct expression: build from its fields, reading the
	// expression once. Interpolating it four times meant four calls per
	// iteration of whatever loop the widget sits in -- `bgOf(entry.tone())`
	// in the calculator's keypad -- and no compiler can undo that, because
	// nothing here promises the call is pure. `let` binds it instead.
	return fmt.Sprintf("(%s).let { ComposeColor(it.r, it.g, it.b, it.a) }", val)
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

func composeModifier(prop, val string) string {
	switch prop {
	case "padding":
		return fmt.Sprintf("padding(%s.dp)", val)
	case "paddingTop":
		return fmt.Sprintf("padding(top = %s.dp)", val)
	case "paddingBottom":
		return fmt.Sprintf("padding(bottom = %s.dp)", val)
	case "paddingLeft":
		return fmt.Sprintf("padding(start = %s.dp)", val)
	case "paddingRight":
		return fmt.Sprintf("padding(end = %s.dp)", val)
	case "width":
		return fmt.Sprintf("width(%s.dp)", val)
	case "height":
		return fmt.Sprintf("height(%s.dp)", val)
	case "background":
		return fmt.Sprintf("background(%s)", composeColorExpr(val))
	case "opacity":
		return fmt.Sprintf("alpha(%s)", val)
	case "flex":
		// Decided in modifierRawExcept, which knows whether weight() is in
		// scope here.
		return ""
	case "gap":
		// Gap handled by Arrangement in Column/Row
		return ""
	}
	return ""
}

// --- Declared Compose intrinsics ---

// intrinsicNS prefixes every intrinsic id this platform answers to, so a
// primitive declared here can never collide with a stdlib intrinsic or with
// another platform's.
const intrinsicNS = "android:"

// composeIntrinsic returns the composable name behind n when n is a component
// marked #[intrinsic] in this platform's namespace. A stdlib component whose
// android override is written in the extension form is inlined into its caller
// at lower time, so what reaches codegen is the intrinsic the override named.
func composeIntrinsic(n *ir.NodeInst) (*ir.Component, string) {
	if n.Component == nil {
		return nil, ""
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
// Five prop names are this emitter's own — `modifier` builds the Modifier
// chain from a Style, `chain` appends further Modifier calls to it, `args` is
// arguments already spelled in Kotlin, `colors` is a Style rendered as the
// widget's own colour set and `shape` as its own outline — because none of
// the five is a value Compose takes as written.
func (cc *irComposeContext) renderIntrinsic(n *ir.NodeInst, comp *ir.Component, composable string) {
	var args []string
	for _, p := range comp.Props {
		switch p.Name {
		case "chain":
		case "args":
			args = append(args, irStringList(codegen.NodeProp(n, p.Name))...)
		case "modifier":
			args = append(args, "modifier = "+cc.intrinsicModifier(n, comp))
		case "shape":
			// A Material button is a stadium by default, so a tall one is an
			// ellipse. `borderRadius` is the corner it actually asked for.
			if sh := cc.cornerShapeExpr(n, "modifier"); sh != "" {
				args = append(args, p.Name+" = "+sh)
			}
		case "colors":
			// A Button paints its own surface, so Modifier.background draws a
			// rectangle *behind* the pill rather than colouring it -- which is
			// the orange square that showed around every operator key. The
			// colour belongs in the widget's own colour set.
			if c := cc.buttonColorsExpr(n, p.Name); c != "" {
				args = append(args, p.Name+" = "+c)
			}
		default:
			if isStyleType(p.Type) {
				if ts := cc.textStyleExpr(n, p.Name); ts != "" {
					args = append(args, p.Name+" = "+ts)
				}
				continue
			}
			v := codegen.NodeProp(n, p.Name)
			if v == nil {
				continue
			}
			if lam, ok := v.(*ir.Lambda); ok {
				args = append(args, cc.lambdaArg(p.Name, lam))
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
	// weight() lives on RowScope and ColumnScope, so whether a child may ask
	// for a share -- and along which axis -- depends on which composable is
	// about to receive it. Nothing below here is the window's content.
	outerAxis, outerRoot := cc.parentAxis, cc.atRoot
	cc.parentAxis = ""
	if composable == "Row" || composable == "Column" {
		cc.parentAxis = composable
	}
	cc.atRoot = false
	defer func() { cc.parentAxis, cc.atRoot = outerAxis, outerRoot }()
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
// lambda is neither a Row nor a Column scope and is never the window's own
// content, so nothing inside it may ask for a weight or for the display
// cutout's inset.
func (cc *irComposeContext) renderNested(stmts []ir.Stmt) string {
	savedBuf, savedAxis, savedRoot := cc.buf, cc.parentAxis, cc.atRoot
	cc.buf, cc.parentAxis, cc.atRoot = &strings.Builder{}, "", false
	cc.indent++
	for _, stmt := range stmts {
		cc.renderStmt(stmt)
	}
	cc.indent--
	body := cc.buf.String()
	cc.buf, cc.parentAxis, cc.atRoot = savedBuf, savedAxis, savedRoot
	return body
}

// intrinsicModifier builds the Modifier chain for a declared composable:
// the styling and testTag every node gets, then the composable's own
// `chain` entries, which are Modifier calls spelled in Kotlin because
// Style has no field that names one.
func (cc *irComposeContext) intrinsicModifier(n *ir.NodeInst, comp *ir.Component) string {
	var mod strings.Builder
	// A widget that takes its own colours takes the background with them, so
	// the Modifier must not draw one too.
	skip := map[string]bool{}
	if declaresProp(comp, "colors") {
		skip["background"] = true
	}
	mod.WriteString(cc.modifierRawExcept(n, "modifier", skip))
	for _, call := range irStringList(codegen.NodeProp(n, "chain")) {
		mod.WriteString("." + call)
	}
	if cc.atRoot {
		// A phone's window is not a rectangle: a status bar sits over the top
		// of it and a cutout over part of that. Last in the chain, so it
		// insets the content and not the background -- the colour still
		// reaches the edges and only what is drawn inside is kept clear.
		cc.kc.RequireImport("androidx.compose.foundation.layout.safeDrawingPadding")
		mod.WriteString(".safeDrawingPadding()")
	}
	return mod.String()
}

// rawCallbackValue names the value Compose hands a callback, in the emitted
// Kotlin. It is a generated local: the declaration names the SNGL event built
// from it, not the framework's own argument.
const rawCallbackValue = "newValue"

// lambdaArg emits a func-typed prop as the Kotlin lambda Compose takes in its
// place. Compose spells a callback as an argument, so the body is written where
// the argument goes and the whole call stays one expression even when the body
// spans lines.
//
// A parameter is declared only where the body reads it: a Compose callback
// lambda may leave the value it is passed unnamed, and every declaration here
// is one the stdlib override wrote for whichever handlers a call site turned
// out to supply.
//
// A prop that *returns* something is written as Kotlin's other function
// literal, the anonymous `fun`, because the lambda form cannot say either half
// of what such an argument needs. `return` in a lambda is a non-local return
// from the enclosing function, and the value's type is left to inference --
// where Compose wants a Float and SNGL's `float` is a Double, inference gives
// it the wrong one silently. `fun(): Float { return … }` states both.
func (cc *irComposeContext) lambdaArg(name string, lam *ir.Lambda) string {
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
	if lam.Func.Return != nil {
		return cc.anonFuncArg(name, lam, body)
	}

	var params, prologue []string
	for _, p := range lam.Func.Params {
		if !readsParam(lam.Func.Block, p) {
			continue
		}
		// A parameter declared as an event payload is the SNGL event, while
		// Compose passes the changed value: the event is built from it here,
		// the only place both are in view. SnglInputEvent is the holder this
		// platform emits (compiler_ir.go).
		if paramTakesInputHolder(lam.Func.Block, p) {
			params = append(params, rawCallbackValue)
			prologue = append(prologue, fmt.Sprintf("val %s = SnglInputEvent(%s)", p.Name, rawCallbackValue))
			continue
		}
		params = append(params, p.Name)
	}

	outer := strings.Repeat("    ", cc.indent)
	var b strings.Builder
	b.WriteString(name + " = {")
	if len(params) > 0 {
		b.WriteString(" " + strings.Join(params, ", ") + " ->")
	}
	for _, line := range append(prologue, body...) {
		b.WriteString("\n" + outer + "    " + line)
	}
	b.WriteString("\n" + outer + "}")
	return b.String()
}

// anonFuncArg emits a value-returning func-typed prop as Kotlin's anonymous
// function. Every parameter is named and typed here, unlike the lambda form:
// an anonymous `fun` may not drop one, and the declaration holds the types
// that make the signature Compose's rather than whatever inference reached.
//
// An event-payload parameter is built from the framework's value here too,
// the way lambdaArg does it -- a parameter typed as the SNGL event names a
// class this platform emits under its own name, so declaring it as written
// would name one nothing declares.
func (cc *irComposeContext) anonFuncArg(name string, lam *ir.Lambda, body []string) string {
	params := make([]string, 0, len(lam.Func.Params))
	var prologue []string
	for _, p := range lam.Func.Params {
		if paramTakesInputHolder(lam.Func.Block, p) {
			params = append(params, rawCallbackValue+": String")
			prologue = append(prologue, fmt.Sprintf("val %s = SnglInputEvent(%s)", p.Name, rawCallbackValue))
			continue
		}
		params = append(params, p.Name+": "+kotlin.IRTypeToKt(p.Type))
	}
	outer := strings.Repeat("    ", cc.indent)
	var b strings.Builder
	fmt.Fprintf(&b, "%s = fun(%s): %s {", name, strings.Join(params, ", "), kotlin.IRTypeToKt(lam.Func.Return))
	for _, line := range append(prologue, body...) {
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

// paramTakesInputHolder reports whether p is a callback parameter this
// platform materializes as a SnglInputEvent: an event payload the body reads,
// where Compose passes the changed value instead.
//
// It is a function rather than the inline test it replaces because
// compiler_ir.go has to answer the same question *before* it writes the
// preamble the holder is declared in, and a second spelling of the predicate
// would eventually disagree with this one. Only over-declaring is harmless;
// under-declaring is Kotlin naming a class nothing defines.
func paramTakesInputHolder(block []ir.Stmt, p *ir.Param) bool {
	return readsParam(block, p) && p.Type != nil && p.Type.Kind == ir.TypeStruct
}

// usesInputHolder reports whether anything in pkg will reach one of the two
// sites that emit a SnglInputEvent: a func-typed prop's lambda (lambdaArg) or
// a param-carrying handler on one of the components renderStdlibComposable
// still writes by hand (emitInputHandlerCall).
//
// The walk is over the whole package rather than over the windows and
// components actually emitted, because the cost of the two answers is not
// symmetric: a declaration nothing uses is dead Kotlin, and a use with no
// declaration does not compile.
func usesInputHolder(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch v := n.(type) {
		case *ir.Lambda:
			if v.Func == nil || len(v.Func.Block) == 0 {
				return nil
			}
			for _, p := range v.Func.Params {
				if paramTakesInputHolder(v.Func.Block, p) {
					found = true
				}
			}
		case *ir.NodeInst:
			if !handWrittenComposable(v) {
				return nil
			}
			for i := range v.Handlers {
				if h := &v.Handlers[i]; h.Func != nil && len(h.Func.Params) > 0 {
					found = true
				}
			}
		}
		return nil
	})
	return found
}

// usesChangeEvent reports whether anything in pkg builds a `sngl:ui`
// ChangeEvent. A struct literal is what spells the class name in the emitted
// Kotlin -- `change({value=opt})` in an override becomes
// `ChangeEvent(value = opt)` -- and nothing flows a stdlib struct into the
// data classes the file declares.
func usesChangeEvent(pkg *ir.Package) bool {
	found := false
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		if lit, ok := e.(*ir.StructLit); ok && lit.Def != nil && lit.Def.Name == "ChangeEvent" {
			found = true
		}
		return nil
	})
	return found
}

// isStyleType reports whether t is a struct named Style — in practice the
// stdlib's, which is the prop type
// the TextStyle mapping is keyed on, since Compose spells typography as an
// argument of its own rather than as a Modifier.
func isStyleType(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd.Name == "Style"
}

// irStringList reads a list-of-string-literal expression. A non-literal
// element is skipped: the value is Kotlin source, so nothing else could be
// emitted for it.
func irStringList(e ir.Expr) []string {
	lit, ok := e.(*ir.ListLit)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(lit.Elems))
	for _, el := range lit.Elems {
		if s, ok := codegen.IRLiteralString(el); ok {
			out = append(out, s)
		}
	}
	return out
}

// ktFloat renders an already-emitted Kotlin expression as a Float. weight()
// takes one and a SNGL float is a Double, so a literal takes the `f` suffix --
// but the suffix is the literal's, and appending it to an expression made
// `entry.span` into the undeclared name `entry.spanf`.
func ktFloat(val string) string {
	if _, err := strconv.ParseFloat(val, 64); err == nil {
		return val + "f"
	}
	return "(" + val + ").toFloat()"
}
