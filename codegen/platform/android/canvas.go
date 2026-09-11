package android

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The 2D primitives are translated in this package rather than through an
// IntrinsicEmitter, which renders one expression and could not carry the
// statements and pending style these need. Declaring the package is how that
// implementation becomes visible to the completeness check.
func init() { codegen.DeclarePlatformImplements("android", "sngl:internal/draw") }

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
	// The two shims' own needs: the receiver they extend, and the ARGB int a
	// `Paint` colour is.
	"androidx.compose.ui.graphics.drawscope.DrawScope",
	"androidx.compose.ui.graphics.toArgb",
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
	// A canvas node carries its draw func on the NodeInst, and this platform
	// renders it inline from there rather than emitting the func -- so a
	// program whose only canvas is inside a component body has no
	// `_canvasDraw` anywhere the loops above look. Asking the tree is asking
	// the same question renderCanvas answers.
	for _, comp := range pkg.Components {
		if comp != nil && treeHasCanvas(comp.Body) {
			return true
		}
	}
	for _, w := range pkg.Windows {
		if w != nil && treeHasCanvas(w.Body) {
			return true
		}
	}
	return false
}

// treeHasCanvas reports whether any node in stmts is a canvas passCanvas gave
// a draw func to.
func treeHasCanvas(stmts []ir.Stmt) bool {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ir.NodeInst:
			if n.CanvasDraw != nil || treeHasCanvas(n.Children) {
				return true
			}
		case *ir.If:
			if treeHasCanvas(n.Body) || treeHasCanvas(n.Else) {
				return true
			}
		case *ir.For:
			if treeHasCanvas(n.Body) || treeHasCanvas(n.Else) {
				return true
			}
		case *ir.Window:
			if treeHasCanvas(n.Body) {
				return true
			}
		case *ir.SlotInst:
			if treeHasCanvas(n.Children) {
				return true
			}
		case *ir.ErrorBoundary:
			if treeHasCanvas(n.Children) {
				return true
			}
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
			raw := lit.Value
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

	// `width`/`height` are the coordinate space the shapes were placed in.
	// At `center` the Canvas is that size and the drawing lands one to one;
	// otherwise it takes the room it is given and the shapes are scaled into
	// it, because a DrawScope has no backing store to stretch the way a
	// browser or Fyne does.
	mode := canvasScalingProp(n)
	switch mode {
	case canvasutil.ScaleFit, canvasutil.ScaleFill, canvasutil.ScaleStretch:
		cc.kc.RequireImport("androidx.compose.foundation.layout.fillMaxWidth")
		cc.kc.RequireImport("androidx.compose.ui.graphics.drawscope.scale")
		cc.line("Canvas(modifier = Modifier.fillMaxWidth().aspectRatio(%df / %df)%s) {", w, h, tag)
		cc.indent++
		cc.kc.RequireImport("androidx.compose.foundation.layout.aspectRatio")
		cc.emitCanvasScale(mode, w, h)
	default:
		cc.line("Canvas(modifier = Modifier.size(%d.dp, %d.dp)%s) {", w, h, tag)
		cc.indent++
		cc.emitDrawBody(n.CanvasDraw)
		cc.indent--
		cc.line("}")
		return
	}
	cc.emitDrawBody(n.CanvasDraw)
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
	switch mode {
	case canvasutil.ScaleStretch:
		cc.line("scale(scaleX = size.width / %df, scaleY = size.height / %df, pivot = Offset.Zero) {", w, h)
	case canvasutil.ScaleFill:
		cc.line("scale(scale = maxOf(size.width / %df, size.height / %df), pivot = Offset.Zero) {", w, h)
	default:
		cc.line("scale(scale = minOf(size.width / %df, size.height / %df), pivot = Offset.Zero) {", w, h)
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
	cc.emitDrawStmts(fn.Block, ds)
}

// emitDrawStmts walks a draw body. An `if` or a `for` is not a shape, it is
// how the shapes under it got there -- reading only the top-level calls drew
// the canvas background and dropped every shape a loop produced, which is a
// seven-segment display with no segments.
func (cc *irComposeContext) emitDrawStmts(stmts []ir.Stmt, ds *canvasDrawState) {
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
				continue
			}
			cc.emitCanvasIntrinsic(s.Call, ds)
		case *ir.LocalVar:
			// A shape override binds one -- a Path it fills before drawing --
			// and dropping it left the draw call naming a value nothing
			// declared.
			cc.line("%s", cc.kc.LocalVarText(s, cc.kc.EvalExpr(s.Init)))
		case *ir.For:
			cc.emitDrawFor(s, ds)
		case *ir.If:
			cc.emitDrawIf(s, ds)
		}
	}
}

func (cc *irComposeContext) emitDrawFor(s *ir.For, ds *canvasDrawState) {
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
	cc.emitDrawStmts(s.Body, ds)
	cc.indent--
	cc.line("%s", cc.kc.BlockEnd())
	cc.kc = savedKC
	if len(s.Else) > 0 {
		cc.emitDrawStmts(s.Else, ds)
	}
}

func (cc *irComposeContext) emitDrawIf(s *ir.If, ds *canvasDrawState) {
	cc.line("if (%s) {", cc.kc.EvalExpr(s.Cond))
	cc.indent++
	cc.emitDrawStmts(s.Body, ds)
	cc.indent--
	if len(s.Else) > 0 {
		cc.line("} else {")
		cc.indent++
		cc.emitDrawStmts(s.Else, ds)
		cc.indent--
	}
	cc.line("}")
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
	b.WriteString(snglDrawShims)
	return b.String()
}

// snglDrawShims are the DrawScope extensions a shape override calls for the
// two drawings a native declaration cannot describe. See emitCanvasStructs.
const snglDrawShims = `fun DrawScope.snglDrawText(content: String, x: Float, y: Float, c: ComposeColor, size: Float) {
    drawContext.canvas.nativeCanvas.drawText(content, x, y, android.graphics.Paint().apply {
        color = c.toArgb()
        textSize = size
    })
}

// A file that will not decode draws nothing rather than failing the frame,
// which is what every other target does with an unreadable source.
fun DrawScope.snglDrawImage(src: String, x: Float, y: Float, w: Float, h: Float) {
    val bmp = android.graphics.BitmapFactory.decodeFile(src) ?: return
    drawImage(
        image = bmp.asImageBitmap(),
        dstOffset = IntOffset(x.toInt(), y.toInt()),
        dstSize = IntSize(w.toInt(), h.toInt()),
    )
}

`
