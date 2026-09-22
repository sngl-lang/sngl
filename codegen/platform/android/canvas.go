package android

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The 2D primitives are translated in this package rather than through an
// IntrinsicEmitter, which renders one expression and could not carry the
// statements and pending style these need. Declaring the package is how that
// implementation becomes visible to the completeness check.
func init() { codegen.DeclarePlatformImplements("android", "sngl:internal/draw") }

// Canvas2D rendering for android via Jetpack Compose's DrawScope.
//
// passShapeDraw (internal/lower) turns a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func. Two kinds of statement reach it: the
// bracket around a composed shape (CanvasSave / CanvasRestore, the only canvas
// intrinsics anything still emits, and both no-ops here -- a DrawScope manages
// its state per primitive), and whatever a shape override's own `@draw`
// handler was written as, which for android is Compose draw calls from
// `component shapes.circle[platform]`.
//
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
	// No Fill: the fill variants in android.sngl omit `style` entirely and
	// take Compose's own default, so nothing the overrides emit names it.
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

// packageHasCanvas reports whether the program contains at least one canvas.
// Used to decide whether to emit the canvas stdlib data classes.
func packageHasCanvas(draws *codegen.CanvasDraws) bool {
	return len(draws.All()) > 0
}

// treeHasCanvas reports whether any node in stmts is a canvas.
func treeHasCanvas(stmts []ir.Stmt) bool {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(n) || treeHasCanvas(n.Children) {
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
// codegen built the draw func for it, and the body is translated inline into
// the DrawScope lambda.
func (cc *irComposeContext) renderCanvas(n *ir.NodeInst) {
	drawing := cc.ctx.Canvases.ForNode(n)
	if drawing == nil {
		return
	}
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

// emitDrawBody translates a synthesized `_canvasDrawN` block into DrawScope
// Kotlin lines. passCanvas emits a fixed per-shape structure: Save,
// [ApplyStyle], DrawPrimitive, Restore. ApplyStyle binds the style the
// following primitive's fill/stroke reference.
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
				continue
			}
			cc.emitCanvasIntrinsic(s.Call)
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

// evalStyleArg evaluates a CanvasApplyStyle argument to a Kotlin CanvasStyle
// expression. passCanvas emits style references to component funcs as explicit
// receiver calls (`main.circleStyle()`). android emits those component funcs as
// either computed properties (`val circleStyle by ... derivedStateOf`) or as
// state methods, so a generic method-call translation can't resolve them. Here
// we map the call to the bare name (computed → property read, plain func →
// call), routing through IdentRewrites so test-mode `state.` prefixing applies.
// Inline `CanvasStyle{...}` literals (no receiver call) fall through to
// EvalExpr unchanged.

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
func (cc *irComposeContext) emitCanvasIntrinsic(call *ir.Call) {
	id := call.Func.Intrinsic
	// All canvas intrinsics take ctx as arg 0; the DrawScope is the implicit
	// receiver, so ctx itself is unused in the Compose translation.
	switch id {
	case "CanvasSave":
		// DrawScope clip/transform state is managed per-primitive; the
		// save/restore brackets passCanvas emits have no DrawScope analog
		// (each draw* is independent). No-op.
	case "CanvasRestore":
	}
}

// canvasKotlinDecls returns the Kotlin data classes for the canvas stdlib
// structs (CanvasStyle/PathCmd) plus the SNGL-color→Compose-Color helper.
// These structs are stdlib-only and aren't carried on pkg.Structs, so — like
// canvasutil does for the Go platforms — they're materialized here. declared is
// the set of user-declared struct names (data classes already emitted) so a
// collision drops just that one decl.
//
// Color is not among them: a canvas is one of two ways a program reaches the
// color type, so it is emitted by colorKotlinDecl for both.
func canvasKotlinDecls(declared map[string]struct{}) string {
	var b strings.Builder
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
const snglDrawShims = `fun DrawScope.snglDrawText(content: String, x: Float, y: Float, c: ComposeColor, size: Float, family: String) {
    drawContext.canvas.nativeCanvas.drawText(content, x, y, android.graphics.Paint().apply {
        color = c.toArgb()
        textSize = size
        // The three CSS generic families Typeface names, and Typeface.create
        // falls back to the system default for anything else -- which is the
        // right answer for a family this device does not have. android read
        // fontFamily nowhere before this (#223).
        typeface = android.graphics.Typeface.create(family, android.graphics.Typeface.NORMAL)
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
// build emits names it in its signature, which is the one way a program
// reaches the type without drawing.
func namesColor(structs []*ir.StructDef) bool {
	for _, sd := range structs {
		if sd.Builtin == ir.BuiltinColor {
			return true
		}
	}
	return false
}
