package android

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/canvasutil"
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/ir"
)

// Canvas2D rendering for android via Jetpack Compose's DrawScope.
//
// passShapeDraw (internal/lower) replaces a canvas's shape children in place
// with the statements that paint them -- nothing is synthesized, and the
// `_canvasDrawN` name is codegen's own. Every one of those statements is what a
// shape override's own `@draw` handler was written as -- for android, Compose draw
// calls from `component shapes.circle[platform]`. The lowering contributes
// nothing of its own: the CanvasSave/CanvasRestore bracket it used to put
// around a composed shape is gone, and was a pair of no-ops here anyway, a
// DrawScope managing its state per primitive.
//
// android is declarative (it keeps the NodeInst tree), so the canvas node
// reaches renderNode, and ctx.Canvases.ForNode finds its drawing. We emit a
// Compose
//
//	Canvas(modifier = Modifier.size(w.dp, h.dp)) { /* this: DrawScope */ ... }
//
// and translate the draw func body inline into the trailing DrawScope lambda.
// The draw lambda reads Compose state (e.g. `radius`, computed styles) directly,
// so recomposition redraws the Canvas automatically — that's why android sets
// ReactiveCanvas=false and CanvasRedrawStmt is a no-op (see compose_ir.go).
//
// There are no canvas intrinsics left to register anywhere. Where a platform
// once had to translate them itself — the lang-keyed registry being wrong for
// a Compose-specific 2D API — the drawing is now written in SNGL and reaches
// the emitter as ordinary Compose calls.

// packageHasCanvas reports whether the program contains at least one canvas.
// Used to decide whether to emit the canvas stdlib data classes.
func packageHasCanvas(draws *codegen.CanvasDraws) bool {
	return len(draws.All()) > 0
}

// canvasIntProp extracts the integer pixel value of a numeric/measurement prop
// (e.g. canvas `width=400px`) from the NodeInst. Mirrors lower.nodeIntProp
// (which runs on the flattened CreateNode path used by Go platforms); android
// keeps the NodeInst, so it reads the prop here. Returns 0 when absent.
func canvasIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name == name {
			px, _ := codegen.MeasurementPx(p.Value, codegen.CanvasEmPx)
			return int(px)
		}
	}
	return 0
}

// renderCanvas emits the Compose Canvas composable for a canvas NodeInst whose
// codegen built the draw func for it, and the body is translated inline into
// the DrawScope lambda.
func (cc *irComposeContext) renderCanvas(n *ir.NodeInst) {
	drawing := cc.ctx.Canvases.ForNode(n)
	if drawing == nil {
		return
	}
	w, h := canvasIntProp(n, "width"), canvasIntProp(n, "height")
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}

	tag := ""
	if id := userTestTag(n); id != "" {
		tag = fmt.Sprintf(".%s(%q)", cc.kc.Name(ktTestTag), id)
	}

	// `width`/`height` are the coordinate space the shapes were placed in.
	// At `center` the Canvas is that size and the drawing lands one to one;
	// otherwise it takes the room it is given and the shapes are scaled into
	// it, because a DrawScope has no backing store to stretch the way a
	// browser or Fyne does.
	mode := canvasScalingProp(n)
	switch mode {
	case canvasutil.ScaleFit, canvasutil.ScaleFill, canvasutil.ScaleStretch:
		cc.line("%s(modifier = %s.%s().%s(%df / %df)%s) {", cc.kc.Name(ktCanvas), cc.kc.Name(ktModifier), cc.kc.Name(ktFillMaxWidth), cc.kc.Name(ktAspectRatio), w, h, tag)
		cc.indent++
		cc.emitCanvasScale(mode, w, h)
	default:
		cc.line("%s(modifier = %s.%s(%s, %s)%s) {", cc.kc.Name(ktCanvas), cc.kc.Name(ktModifier), cc.kc.Name(ktSize), dp(cc.kc, strconv.Itoa(w)), dp(cc.kc, strconv.Itoa(h)), tag)
		cc.indent++
		cc.emitDrawBody(drawing.Draw)
		cc.indent--
		cc.line("}")
		return
	}
	cc.emitDrawBody(drawing.Draw)
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

// emitCanvasScale opens the DrawScope transform that maps the drawing's own
// coordinate space onto the space the Canvas was laid out in. The caller
// closes it.
//
// `stretch` scales the two axes separately; `fit` and `fill` scale both by one
// factor, the smaller ratio for fit so the whole drawing lands inside and the
// larger for fill so none of the room is left over.
func (cc *irComposeContext) emitCanvasScale(mode string, w, h int) {
	scale, zero := cc.kc.Name(ktDrawScale), cc.kc.Name(ktOffset)+".Zero"
	switch mode {
	case canvasutil.ScaleStretch:
		cc.line("%s(scaleX = size.width / %df, scaleY = size.height / %df, pivot = %s) {", scale, w, h, zero)
	case canvasutil.ScaleFill:
		cc.line("%s(scale = maxOf(size.width / %df, size.height / %df), pivot = %s) {", scale, w, h, zero)
	default:
		cc.line("%s(scale = minOf(size.width / %df, size.height / %df), pivot = %s) {", scale, w, h, zero)
	}
	cc.indent++
}

// canvasScalingProp reads the canvas node's `scalingMode`.
func canvasScalingProp(n *ir.NodeInst) string {
	if v, ok := codegen.NodeProp(n, "scalingMode").(*ir.Ident); ok {
		return v.Member
	}
	return ""
}

// emitDrawBody writes a drawing into the DrawScope lambda as Kotlin lines.
//
// There is no fixed per-shape structure to expect: what a drawing holds is
// whatever each shape override's `@draw` handler was written as, which for
// android is Compose draw calls.
func (cc *irComposeContext) emitDrawBody(stmts []ir.Stmt) {
	cc.emitDrawStmts(stmts)
}

// emitDrawStmts walks a draw body. An `if` or a `for` is not a shape, it is
// how the shapes under it got there -- reading only the top-level calls drew
// the canvas background and dropped every shape a loop produced, which is a
// seven-segment display with no segments.
func (cc *irComposeContext) emitDrawStmts(stmts []ir.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ir.CallStmt:
			if s.Call == nil || s.Call.Func == nil {
				continue
			}
			// A call that is not one of this package's canvas intrinsics is
			// an ordinary statement of the draw body -- a Compose scope
			// function a shape override named, or a helper. Dropping it is
			// what left an override's `if` with an empty body.
			if s.Call.Func.Intrinsic == "" {
				cc.line("%s", cc.kc.EvalExpr(s.Call))
			}
			// A call that *does* carry an intrinsic id is dropped, which is
			// what this did before the canvas ids went: emitCanvasIntrinsic
			// answered CanvasSave and CanvasRestore with no-ops -- a DrawScope
			// manages its state per primitive -- and had no default arm, so
			// anything else fell through it in silence. Nothing in a draw body
			// carries one today; the drop is kept rather than turned into an
			// emit so that this change moves no output.
		case *ir.LocalVar:
			// A shape override binds one -- a Path it fills before drawing --
			// and dropping it left the draw call naming a value nothing
			// declared.
			cc.line("%s", cc.kc.LocalVarText(s, cc.kc.EvalExpr(s.Init)))
		case *ir.For:
			cc.emitDrawFor(s)
		case *ir.If:
			cc.emitDrawIf(s)
		}
	}
}

func (cc *irComposeContext) emitDrawFor(s *ir.For) {
	iterExpr := cc.kc.EvalExpr(s.Iter)
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
	cc.emitDrawStmts(s.Body)
	cc.indent--
	cc.line("%s", cc.kc.BlockEnd())
	cc.kc = savedKC
	if len(s.Else) > 0 {
		cc.emitDrawStmts(s.Else)
	}
}

func (cc *irComposeContext) emitDrawIf(s *ir.If) {
	cc.line("if (%s) {", cc.kc.EvalExpr(s.Cond))
	cc.indent++
	cc.emitDrawStmts(s.Body)
	cc.indent--
	if len(s.Else) > 0 {
		cc.line("} else {")
		cc.indent++
		cc.emitDrawStmts(s.Else)
		cc.indent--
	}
	cc.line("}")
}

// canvasKotlinDecls returns the Kotlin data classes for the canvas stdlib
// struct (Style) plus the SNGL-color→Compose-Color helper.
// These structs are stdlib-only and aren't carried on pkg.Structs, so — like
// canvasutil does for the Go platforms — they're materialized here. declared is
// the set of user-declared struct names (data classes already emitted) so a
// collision drops just that one decl.
//
// Color is not among them: a canvas is one of two ways a program reaches the
// color type, so it is emitted by colorKotlinDecl for both.
func canvasKotlinDecls(kc *kotlin.KtIRContext, declared map[string]struct{}) string {
	var b strings.Builder
	if _, ok := declared["Style"]; !ok {
		b.WriteString("data class Style(\n")
		b.WriteString("    var fill: Color = Color(a = 0),\n")
		b.WriteString("    var stroke: Color = Color(a = 0),\n")
		b.WriteString("    var strokeWidth: Double = 1.0,\n")
		b.WriteString("    var lineCap: String = \"butt\",\n")
		b.WriteString("    var lineJoin: String = \"miter\",\n")
		b.WriteString("    var fontSize: Double = 16.0,\n")
		b.WriteString("    var fontFamily: String = \"sans-serif\"\n")
		b.WriteString(")\n\n")
	}
	// Two DrawScope extensions the shape overrides call, for the drawing this
	// platform cannot describe with a native declaration: text goes through a
	// `Paint` built by an `apply` block and reached by chained property
	// access, and an image through a safe-call chain ending in a `let`. Both
	// are Kotlin the mark cannot spell, and a shim is where that belongs --
	// the same trade gtk4 makes for `g_object_unref`, whose `gpointer` cast an
	// override cannot write either.
	//
	// Extensions on DrawScope, so an override calls them the way it calls
	// `drawRect`: receiverless, with the scope supplying the receiver.
	b.WriteString(strings.NewReplacer(
		"{DrawScope}", kc.Name(ktDrawScope),
		"{ComposeColor}", composeColor(kc),
		"{nativeCanvas}", kc.Name(ktNativeCanvas),
		"{toArgb}", kc.Name(ktToArgb),
		"{IntOffset}", kc.Name(ktIntOffset),
		"{IntSize}", kc.Name(ktIntSize),
	).Replace(snglDrawShims))
	return b.String()
}

// snglDrawShims are the DrawScope extensions a shape override calls for the
// two drawings a native declaration cannot describe. See emitCanvasStructs.
// Each {Name} is a Compose name, replaced with what the file calls it.
const snglDrawShims = `fun {DrawScope}.snglDrawText(content: String, x: Float, y: Float, c: {ComposeColor}, size: Float, family: String) {
    drawContext.canvas.{nativeCanvas}.drawText(content, x, y, android.graphics.Paint().apply {
        color = c.{toArgb}()
        textSize = size
        // The three CSS generic families Typeface names, and Typeface.create
        // falls back to the system default for anything else -- which is the
        // right answer for a family this device does not have. android read
        // fontFamily nowhere before this (#223).
        typeface = android.graphics.Typeface.create(family, android.graphics.Typeface.NORMAL)
    })
}

// A picture that will not decode draws nothing rather than failing the frame,
// which is what every other target does with an unreadable source.
fun {DrawScope}.snglDrawImage(src: String, x: Float, y: Float, w: Float, h: Float) {
    val bmp = SnglAssets.bitmap(src) ?: return
    drawImage(
        image = bmp,
        dstOffset = {IntOffset}(x.toInt(), y.toInt()),
        dstSize = {IntSize}(w.toInt(), h.toInt()),
    )
}

// A drawing painted in place of a picture: its own space mapped onto the box
// and clipped to it, until snglDrawLeave.
fun {DrawScope}.snglDrawEnter(x: Float, y: Float, sx: Float, sy: Float, w: Float, h: Float) {
    drawContext.canvas.save()
    drawContext.canvas.translate(x, y)
    drawContext.canvas.scale(sx, sy)
    drawContext.canvas.clipRect(0f, 0f, w, h)
}

fun {DrawScope}.snglDrawLeave() {
    drawContext.canvas.restore()
}

`

// colorKotlinDecl returns the Color data class, or "" when the program
// declares a struct of that name itself -- that one is emitted from
// info.Structs and is what every reference resolves to.
func colorKotlinDecl(declared map[string]struct{}) string {
	if _, ok := declared["Color"]; ok {
		return ""
	}
	return kotlin.ColorDecl + "\n"
}

// namesColor reports whether the built-in color type reached this package's
// struct list -- promoteForeignStructs puts it there when a function this
// build emits names it in its signature -- or a field of a struct on it names
// the type, since that data class is emitted and spells Color too.
func namesColor(structs []*ir.StructDef) bool {
	for _, sd := range structs {
		if sd.Builtin == ir.BuiltinColor {
			return true
		}
		for _, f := range sd.Fields {
			if typeNamesColor(f.Type) {
				return true
			}
		}
	}
	return false
}

func typeNamesColor(t *ir.Type) bool {
	if t == nil {
		return false
	}
	if ir.IsColorStruct(t) {
		return true
	}
	return slices.ContainsFunc(t.Elems, typeNamesColor)
}
