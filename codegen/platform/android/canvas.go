package android

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Canvas2D rendering for android via Jetpack Compose's DrawScope.
//
// passCanvas (internal/lower) extracts a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func whose body is a sequence of canvas
// intrinsic CallStmts (CanvasSave / CanvasApplyStyle / CanvasDrawRect / ...).
// android is declarative (it keeps the NodeInst tree), so the canvas node
// reaches renderNode with n.CanvasDraw set. We emit a Compose
//
//	Canvas(modifier = Modifier.size(w.dp, h.dp)) { /* this: DrawScope */ ... }
//
// and translate the draw func body inline into the trailing DrawScope lambda.
// The draw lambda reads Compose state (e.g. `radius`, computed styles) directly,
// so recomposition redraws the Canvas automatically — that's why android sets
// ReactiveCanvas=false and CanvasRedrawStmt is a no-op (see compose_ir.go).
//
// The canvas intrinsics are NOT registered in the lang-keyed intrinsic registry
// (RegisterIntrinsic("kotlin", ...)) — that registry is for import-free Kotlin
// builtins and would be wrong for the Compose-specific 2D API. They are
// translated here, inside the android emitter, the same way gtk4/fyne translate
// the same intrinsics inside their own platform packages.

// canvasComposeImports are the Compose graphics imports the DrawScope
// translation needs. Registered when any canvas is rendered.
var canvasComposeImports = []string{
	"androidx.compose.foundation.Canvas",
	"androidx.compose.ui.geometry.Offset",
	"androidx.compose.ui.geometry.Size",
	"androidx.compose.ui.graphics.Path",
	"androidx.compose.ui.graphics.drawscope.Fill",
	"androidx.compose.ui.graphics.drawscope.Stroke",
	"androidx.compose.ui.graphics.nativeCanvas",
	"androidx.compose.ui.graphics.asImageBitmap",
	"androidx.compose.ui.unit.IntOffset",
	"androidx.compose.ui.unit.IntSize",
}

// packageHasCanvas reports whether any component/window func is a synthesized
// canvas draw func — i.e. the program contains at least one canvas. Used to
// decide whether to emit the canvas stdlib data classes.
func packageHasCanvas(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	if slices.ContainsFunc(pkg.Funcs, isCanvasDrawFunc) {
		return true
	}
	for _, comp := range pkg.Components {
		if slices.ContainsFunc(comp.Funcs, isCanvasDrawFunc) {
			return true
		}
	}
	for _, w := range pkg.Windows {
		if slices.ContainsFunc(w.Funcs, isCanvasDrawFunc) {
			return true
		}
	}
	return false
}

// isCanvasDrawFunc reports whether fn is a synthesized canvas draw func
// (`_canvasDrawN` produced by passCanvas). Such funcs hold canvas-intrinsic
// CallStmts that only the canvas translation below understands, so the
// generic Kotlin func-emission path must skip them.
func isCanvasDrawFunc(fn *ir.Func) bool {
	return fn != nil && fn.Synthesized && strings.HasPrefix(fn.Name, "_canvasDraw")
}

// canvasIntProp extracts the integer pixel value of a numeric/measurement prop
// (e.g. canvas `width=400px`) from the NodeInst. Mirrors lower.nodeIntProp
// (which runs on the flattened CreateNode path used by Go platforms); android
// keeps the NodeInst, so it reads the prop here. Returns 0 when absent.
func canvasIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			raw := strings.TrimSuffix(lit.Value, lit.Suffix)
			if v, err := strconv.Atoi(raw); err == nil {
				return v
			}
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				return int(f)
			}
		}
	}
	return 0
}

// renderCanvas emits the Compose Canvas composable for a canvas NodeInst whose
// CanvasDraw func was set by passCanvas, translating the draw body inline into
// the DrawScope lambda.
func (cc *irComposeContext) renderCanvas(n *ir.NodeInst) {
	for _, imp := range canvasComposeImports {
		cc.kc.RequireImport(imp)
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
		tag = fmt.Sprintf(".testTag(%q)", id)
	}
	cc.line("Canvas(modifier = Modifier.size(%d.dp, %d.dp)%s) {", w, h, tag)
	cc.indent++
	cc.emitDrawBody(n.CanvasDraw)
	cc.indent--
	cc.line("}")
}

// pendingStyle holds the Kotlin expression of the CanvasStyle bound by the most
// recent CanvasApplyStyle, consumed by the following draw primitive.
type canvasDrawState struct {
	styleExpr   string // Kotlin expr for the active CanvasStyle ("" → none)
	styleVarSeq int
}

// emitDrawBody translates a synthesized `_canvasDrawN` block into DrawScope
// Kotlin lines. passCanvas emits a fixed per-shape structure: Save,
// [ApplyStyle], DrawPrimitive, Restore. ApplyStyle binds the style the
// following primitive's fill/stroke reference.
func (cc *irComposeContext) emitDrawBody(fn *ir.Func) {
	if fn == nil {
		return
	}
	ds := &canvasDrawState{}
	for _, stmt := range fn.Block {
		cs, ok := stmt.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil {
			continue
		}
		cc.emitCanvasIntrinsic(cs.Call, ds)
	}
}

// evalStyleArg evaluates a CanvasApplyStyle argument to a Kotlin CanvasStyle
// expression. passCanvas emits style references to component funcs as explicit
// receiver calls (`main.circleStyle()`). android emits those component funcs as
// either computed properties (`val circleStyle by ... derivedStateOf`) or as
// state methods, so a generic method-call translation can't resolve them. Here
// we map the call to the bare name (computed → property read, plain func →
// call), routing through IdentRewrites so test-mode `state.` prefixing applies.
// Inline `CanvasStyle{...}` literals (no receiver call) fall through to
// EvalExpr unchanged.
func (cc *irComposeContext) evalStyleArg(e ir.Expr) string {
	call, ok := e.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Receiver == "" {
		return cc.kc.EvalExpr(e)
	}
	// Confirm the receiver names a component in this package (component-self
	// method), not a real type method on some value.
	if !cc.receiverIsComponent(call.Func.Receiver) {
		return cc.kc.EvalExpr(e)
	}
	name := call.Func.Name
	if rw, ok := cc.kc.IdentRewrites[name]; ok {
		name = rw
	}
	if codegen.IsComputed(call.Func) {
		return name // computed → derivedStateOf property read
	}
	return name + "()"
}

// receiverIsComponent reports whether name matches a component in the package.
func (cc *irComposeContext) receiverIsComponent(name string) bool {
	if cc.ctx == nil || cc.ctx.Pkg == nil {
		return false
	}
	for _, comp := range cc.ctx.Pkg.Components {
		if comp.Name == name {
			return true
		}
	}
	return false
}

// f converts a SNGL float-valued arg expr to a Kotlin Float for the DrawScope
// API (Offset/Size/radius all take Float; SNGL floats are Double).
func (cc *irComposeContext) f(e ir.Expr) string {
	return "(" + cc.kc.EvalExpr(e) + ").toFloat()"
}

// emitCanvasIntrinsic translates one canvas-intrinsic Call into DrawScope lines.
func (cc *irComposeContext) emitCanvasIntrinsic(call *ir.Call, ds *canvasDrawState) {
	id := call.Func.Intrinsic
	// All canvas intrinsics take ctx as arg 0; the DrawScope is the implicit
	// receiver, so ctx itself is unused in the Compose translation.
	rest := call.Args[1:]
	arg := func(i int) ir.Expr { return rest[i].Value }

	switch id {
	case "CanvasSave":
		// DrawScope clip/transform state is managed per-primitive; the
		// save/restore brackets passCanvas emits have no DrawScope analog
		// (each draw* is independent). No-op.
	case "CanvasRestore":
		ds.styleExpr = "" // a fresh shape follows; drop the consumed style.
	case "CanvasApplyStyle":
		// Bind the style to a fresh local so the following draw's fill/stroke
		// don't re-evaluate the (possibly large) style expression repeatedly.
		ds.styleVarSeq++
		name := fmt.Sprintf("_style%d", ds.styleVarSeq)
		cc.line("val %s = %s", name, cc.evalStyleArg(arg(0)))
		ds.styleExpr = name
	case "CanvasDrawRect":
		topLeft := fmt.Sprintf("Offset(%s, %s)", cc.f(arg(0)), cc.f(arg(1)))
		size := fmt.Sprintf("Size(%s, %s)", cc.f(arg(2)), cc.f(arg(3)))
		cc.emitFillStroke(ds, func(drawStyle string) string {
			return fmt.Sprintf("drawRect(color = %%s, topLeft = %s, size = %s, style = %s)", topLeft, size, drawStyle)
		})
	case "CanvasDrawCircle":
		center := fmt.Sprintf("Offset(%s, %s)", cc.f(arg(0)), cc.f(arg(1)))
		radius := cc.f(arg(2))
		cc.emitFillStroke(ds, func(drawStyle string) string {
			return fmt.Sprintf("drawCircle(color = %%s, radius = %s, center = %s, style = %s)", radius, center, drawStyle)
		})
	case "CanvasDrawEllipse":
		// drawOval takes topLeft = center - radii, size = 2*radii.
		cx, cy := cc.f(arg(0)), cc.f(arg(1))
		rx, ry := cc.f(arg(2)), cc.f(arg(3))
		topLeft := fmt.Sprintf("Offset(%s - %s, %s - %s)", cx, rx, cy, ry)
		size := fmt.Sprintf("Size(%s * 2f, %s * 2f)", rx, ry)
		cc.emitFillStroke(ds, func(drawStyle string) string {
			return fmt.Sprintf("drawOval(color = %%s, topLeft = %s, size = %s, style = %s)", topLeft, size, drawStyle)
		})
	case "CanvasDrawLine":
		start := fmt.Sprintf("Offset(%s, %s)", cc.f(arg(0)), cc.f(arg(1)))
		end := fmt.Sprintf("Offset(%s, %s)", cc.f(arg(2)), cc.f(arg(3)))
		cc.emitStrokeOnly(ds, func(width string) string {
			return fmt.Sprintf("drawLine(color = %%s, start = %s, end = %s, strokeWidth = %s)", start, end, width)
		})
	case "CanvasDrawText":
		// CanvasDrawText(ctx, x, y, content). DrawScope has no text primitive,
		// so draw through the underlying android.graphics.Canvas with a Paint
		// carrying the style's fill color + font size (fully qualified to avoid
		// extra imports). Baseline y matches the Canvas2D / cairo convention.
		x, y := cc.f(arg(0)), cc.f(arg(1))
		content := cc.kc.EvalExpr(arg(2))
		fillA := ds.styleSel("fill.a", "255")
		fillR := ds.styleSel("fill.r", "0")
		fillG := ds.styleSel("fill.g", "0")
		fillB := ds.styleSel("fill.b", "0")
		fontSize := ds.styleSel("fontSize", "16.0")
		cc.line("drawContext.canvas.nativeCanvas.drawText(%s, %s, %s, android.graphics.Paint().apply {", content, x, y)
		cc.indent++
		cc.line("color = android.graphics.Color.argb(%s, %s, %s, %s)", fillA, fillR, fillG, fillB)
		cc.line("textSize = (%s).toFloat()", fontSize)
		cc.indent--
		cc.line("})")
		ds.styleExpr = ""
	case "CanvasDrawPath":
		cc.emitPath(arg(0), ds)
	case "CanvasDrawImage":
		// CanvasDrawImage(ctx, x, y, w, h, src). Decode the bitmap from a local
		// file path and draw it scaled into the (x,y,w,h) box. Null-safe: a
		// missing/undecodable path (or an asset/URL src, which decodeFile can't
		// read) silently draws nothing rather than crashing. Full asset/URL/
		// async loading is a follow-up.
		x, y := cc.f(arg(0)), cc.f(arg(1))
		w, h := cc.f(arg(2)), cc.f(arg(3))
		src := cc.kc.EvalExpr(arg(4))
		cc.line("android.graphics.BitmapFactory.decodeFile(%s)?.asImageBitmap()?.let {", src)
		cc.indent++
		cc.line("drawImage(image = it, dstOffset = IntOffset((%s).toInt(), (%s).toInt()), dstSize = IntSize((%s).toInt(), (%s).toInt()))", x, y, w, h)
		cc.indent--
		cc.line("}")
		ds.styleExpr = ""
	}
}

// styleSel returns the Kotlin expr for `<style>.<field>`, or a zero default
// when no style is active.
func (ds *canvasDrawState) styleSel(field, zero string) string {
	if ds.styleExpr == "" {
		return zero
	}
	return ds.styleExpr + "." + field
}

// emitFillStroke emits a fill draw (when fill.a>0) and a stroke draw (when
// stroke.a>0) for a primitive. mk builds the draw call given a DrawStyle expr;
// it must contain one %s placeholder for the color expression.
func (cc *irComposeContext) emitFillStroke(ds *canvasDrawState, mk func(drawStyle string) string) {
	fillColor := ds.styleSel("fill", "Color(0, 0, 0, 0)")
	strokeColor := ds.styleSel("stroke", "Color(0, 0, 0, 0)")
	strokeWidth := ds.styleSel("strokeWidth", "1.0")

	cc.line("if (%s.a > 0) {", fillColor)
	cc.indent++
	cc.line(fmt.Sprintf(mk("Fill"), "_snglComposeColor("+fillColor+")"))
	cc.indent--
	cc.line("}")
	cc.line("if (%s.a > 0) {", strokeColor)
	cc.indent++
	stroke := fmt.Sprintf("Stroke(width = (%s).toFloat())", strokeWidth)
	cc.line(fmt.Sprintf(mk(stroke), "_snglComposeColor("+strokeColor+")"))
	cc.indent--
	cc.line("}")
	ds.styleExpr = ""
}

// emitStrokeOnly emits a stroked primitive (lines have no fill). mk builds the
// draw call given the stroke-width expr; it must contain one %s for the color.
func (cc *irComposeContext) emitStrokeOnly(ds *canvasDrawState, mk func(width string) string) {
	strokeColor := ds.styleSel("stroke", "Color(0, 0, 0, 255)")
	strokeWidth := ds.styleSel("strokeWidth", "1.0")
	width := fmt.Sprintf("(%s).toFloat()", strokeWidth)
	cc.line(fmt.Sprintf(mk(width), "_snglComposeColor("+strokeColor+")"))
	ds.styleExpr = ""
}

// emitPath builds a Compose Path from the PathCmd list and draws it, filling
// and/or stroking per the active style.
func (cc *irComposeContext) emitPath(cmds ir.Expr, ds *canvasDrawState) {
	fillColor := ds.styleSel("fill", "Color(0, 0, 0, 0)")
	strokeColor := ds.styleSel("stroke", "Color(0, 0, 0, 0)")
	strokeWidth := ds.styleSel("strokeWidth", "1.0")

	cc.line("run {")
	cc.indent++
	cc.line("val _path = Path()")
	cc.line("for (_cmd in %s) {", cc.kc.EvalExpr(cmds))
	cc.indent++
	cc.line("when (_cmd.op) {")
	cc.indent++
	cc.line(`"moveTo" -> _path.moveTo(_cmd.x.toFloat(), _cmd.y.toFloat())`)
	cc.line(`"lineTo" -> _path.lineTo(_cmd.x.toFloat(), _cmd.y.toFloat())`)
	cc.line(`"bezierTo" -> _path.cubicTo(_cmd.cx1.toFloat(), _cmd.cy1.toFloat(), _cmd.cx2.toFloat(), _cmd.cy2.toFloat(), _cmd.x.toFloat(), _cmd.y.toFloat())`)
	cc.line(`"close" -> _path.close()`)
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.line("if (%s.a > 0) {", fillColor)
	cc.indent++
	cc.line("drawPath(path = _path, color = _snglComposeColor(%s), style = Fill)", fillColor)
	cc.indent--
	cc.line("}")
	cc.line("if (%s.a > 0) {", strokeColor)
	cc.indent++
	cc.line("drawPath(path = _path, color = _snglComposeColor(%s), style = Stroke(width = (%s).toFloat()))", strokeColor, strokeWidth)
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	ds.styleExpr = ""
}

// canvasKotlinDecls returns the Kotlin data classes for the canvas stdlib
// structs (Color/CanvasStyle/PathCmd) plus the SNGL-color→Compose-Color helper.
// These structs are stdlib-only and aren't carried on pkg.Structs, so — like
// canvasutil does for the Go platforms — they're materialized here. declared is
// the set of user-declared struct names (data classes already emitted) so a
// collision drops just that one decl.
func canvasKotlinDecls(declared map[string]struct{}) string {
	var b strings.Builder
	if _, ok := declared["Color"]; !ok {
		b.WriteString("data class Color(\n")
		b.WriteString("    var r: Int = 0,\n    var g: Int = 0,\n    var b: Int = 0,\n    var a: Int = 255\n")
		b.WriteString(")\n\n")
	}
	if _, ok := declared["CanvasStyle"]; !ok {
		b.WriteString("data class CanvasStyle(\n")
		b.WriteString("    var fill: Color = Color(a = 0),\n")
		b.WriteString("    var stroke: Color = Color(a = 0),\n")
		b.WriteString("    var strokeWidth: Double = 1.0,\n")
		b.WriteString("    var lineCap: String = \"butt\",\n")
		b.WriteString("    var lineJoin: String = \"miter\",\n")
		b.WriteString("    var fontSize: Double = 16.0,\n")
		b.WriteString("    var fontFamily: String = \"sans-serif\"\n")
		b.WriteString(")\n\n")
	}
	if _, ok := declared["PathCmd"]; !ok {
		b.WriteString("data class PathCmd(\n")
		b.WriteString("    var op: String = \"\",\n")
		b.WriteString("    var x: Double = 0.0,\n    var y: Double = 0.0,\n")
		b.WriteString("    var cx1: Double = 0.0,\n    var cy1: Double = 0.0,\n")
		b.WriteString("    var cx2: Double = 0.0,\n    var cy2: Double = 0.0,\n")
		b.WriteString("    var r: Double = 0.0\n")
		b.WriteString(")\n\n")
	}
	// SNGL color{r,g,b,a} 0..255 → Compose ComposeColor(red,green,blue,alpha)
	// (the Int overload takes 0..255 channels).
	b.WriteString("fun _snglComposeColor(c: Color): ComposeColor = ComposeColor(c.r, c.g, c.b, c.a)\n\n")
	return b.String()
}
