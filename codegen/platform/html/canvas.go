package html

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/canvasutil"
	"duckfam.us/sngl/codegen/lang/javascript"
	"duckfam.us/sngl/ir"
)

// This package registered two platform intrinsics here, CanvasSave and
// CanvasRestore, keyed to the platform rather than to "js" because only an
// html build draws onto a 2D context. Both are gone with the lowering that
// emitted them: a draw function is each shape override's own `@draw` body, and
// html's overrides paint through `#[js.native]` calls the JS translator
// already writes.

// canvasSetup records a canvas element that needs its draw function wired up.
type canvasSetup struct {
	id string
	// node is what a repaint names: a CanvasRedrawStmt points at the canvas
	// instantiation, the draw function being codegen's own artifact.
	node     *ir.NodeInst
	drawName string
	draw     []ir.Stmt
	// w and h are the coordinate space the shapes were placed in, and scaling
	// what to do when the box is not that size. Both are needed at the draw
	// call, not only where the element is written.
	w, h    int
	scaling string
	// image is a drawing an image shows, painted into a canvas of its own
	// rather than into the element.
	image bool
}

// canvasIntProp is the integer pixel value of a canvas dimension prop, which
// is a measurement literal by the time it reaches codegen.
func canvasIntProp(n *ir.NodeInst, name string) int {
	lit, ok := codegen.NodeProp(n, name).(*ir.Literal)
	if !ok || lit == nil {
		return 0
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return 0
	}
	return int(v)
}

// drawCall is the one way this platform draws a canvas: through the helper,
// which decides the backing store from the box and scales the shapes into it.
func (cs canvasSetup) drawCall() string {
	if cs.image {
		return fmt.Sprintf("_snglImageDraw(%s,%d,%d,%s)", cs.id, cs.w, cs.h, cs.drawName)
	}
	return fmt.Sprintf("_snglCanvasDraw(%s,%d,%d,%q,%s)", cs.id, cs.w, cs.h, cs.scaling, cs.drawName)
}

// canvasDrawStmt is that same call as IR, for a canvas the lowering flattened
// into a scope that is emitted as code rather than as markup -- a component
// factory, or a slot renderer. The element and the draw func are locals of
// that scope there, so both are named rather than looked up.
func canvasDrawStmt(m *canvasutil.Meta) ir.Stmt {
	num := func(v int) ir.Expr { return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(v)} }
	el := canvasElementRef(m)
	draw := &ir.Ident{Name: m.DrawName, Type: drawFuncType(m)}
	if isImageDrawing(m.Node) {
		return &ir.CallStmt{Call: &ir.Call{
			Type: ir.TypVoid,
			Func: &ir.Func{Name: imageDrawHelper},
			Args: []ir.CallArg{
				{Value: el},
				{Value: num(m.Width)},
				{Value: num(m.Height)},
				{Value: draw},
			},
		}}
	}
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Name: "_snglCanvasDraw"},
		Args: []ir.CallArg{
			{Value: el},
			{Value: num(m.Width)},
			{Value: num(m.Height)},
			{Value: &ir.Literal{Type: ir.TypString, Value: m.Scaling}},
			{Value: draw},
		},
	}}
}

// canvasInitLines is the first draw of every canvas a translator's scope
// created, and the box watch a scaled one also needs. Recording that the scope
// drew at all is what makes the page carry the helpers: emitCanvasSetups gates
// on the page's own markup canvases, and a factory has none.
func (g *htmlGen) canvasInitLines(tr *htmlTranslator, jc *javascript.JsIRContext) []string {
	var out []string
	for _, m := range tr.canvasDraws {
		g.usesLoweredCanvas = true
		if isImageDrawing(m.Node) {
			g.ctx.Helpers[imageDrawHelper] = true
		}
		stmts := []ir.Stmt{canvasDrawStmt(m)}
		if m.Scaling != "" && !isImageDrawing(m.Node) {
			stmts = append(stmts, canvasWatchStmt(m))
		}
		for _, s := range stmts {
			for _, ln := range jc.EvalStmt(s) {
				out = append(out, ln+";")
			}
		}
	}
	return out
}

// canvasWatchStmt re-runs the draw when the element's box changes, which is
// what a scaled canvas needs: its backing store is sized from the box.
func canvasWatchStmt(m *canvasutil.Meta) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Name: "_snglCanvasWatch"},
		Args: []ir.CallArg{
			{Value: canvasElementRef(m)},
			{Value: &ir.Lambda{Type: voidFuncType(), Func: &ir.Func{Return: ir.TypVoid, Block: []ir.Stmt{canvasDrawStmt(m)}}}},
		},
	}}
}

// snglDrawImageHelper paints a file-backed image, caching the decode.
//
// A shim rather than a mark, for the reason android's image shim is one: the
// drawing is asynchronous, and neither `new Image()` nor an event listener is
// something a native mark can spell. Written only for a page that draws an
// image: `_snglColor` is keyed to the intrinsic expansion that calls it, and
// this is keyed to the native the override calls, which the JS translator
// records as it emits one.
//
// The cache is the part that matters. A canvas redraws on every state change,
// and the element this replaces was built fresh each time -- so a drawing with
// an image in it re-entered the network stack on every frame, and the image
// blinked as each new decode landed. Keyed by src, which is all a file-backed
// source has to distinguish it.
//
// The listener stays for the first paint: a draw that happens before the
// decode finishes has nothing to put down, so it paints when the decode
// arrives instead. Once the image is complete, every later draw takes the
// synchronous path and no listener is added.
//
// The cache hangs off the function rather than sitting beside it as a `const`,
// because only the function declaration is hoisted: a synthesized slot runs
// its own render inline, above where these helpers are written, so a canvas
// image inside one reached the map before the `const` initialized and threw.
// `snglCanvasHelper` is immune for the same reason this now is -- it is a
// function declaration.
const snglDrawImageHelper = `function _snglDrawImage(ctx,src,x,y,w,h){
  const cache=_snglDrawImage.cache||(_snglDrawImage.cache=new Map());
  let img=cache.get(src);
  if(img&&img.complete&&img.naturalWidth>0){ctx.drawImage(img,x,y,w,h);return}
  if(!img){img=new Image();cache.set(src,img);img.src=src}
  img.addEventListener("load",function(){ctx.drawImage(img,x,y,w,h)},{once:true})
}
`

// snglCanvasHelper draws one canvas, scaling the geometry rather than the
// picture.
//
// A canvas has two sizes: the coordinate space the shapes were placed in, and
// the box the layout gave it. Letting the browser bridge the two -- a fixed
// backing store stretched by CSS -- resamples a small image up to a big one,
// and a drawing of hard edges comes out soft. So the backing store is sized
// to the box instead, at device resolution, and the shapes are scaled on the
// way in: every edge is rasterized where it lands rather than interpolated
// from where it landed at another size.
//
// `center` keeps the backing store the author's, which is what a canvas that
// is shown at its own size wants and costs nothing to redraw.
const snglCanvasHelper = `function _snglCanvasDraw(el,w,h,mode,draw){
  const ctx=el.getContext("2d");
  if(!mode){ctx.setTransform(1,0,0,1,0,0);ctx.clearRect(0,0,el.width,el.height);draw(ctx);return}
  const dpr=window.devicePixelRatio||1;
  const pw=Math.max(1,Math.round((el.clientWidth||w)*dpr));
  const ph=Math.max(1,Math.round((el.clientHeight||h)*dpr));
  if(el.width!==pw)el.width=pw;
  if(el.height!==ph)el.height=ph;
  let sx,sy;
  if(mode==="stretch"){sx=pw/w;sy=ph/h}
  else{sx=sy=mode==="fill"?Math.max(pw/w,ph/h):Math.min(pw/w,ph/h)}
  ctx.setTransform(sx,0,0,sy,0,0);
  ctx.clearRect(0,0,w,h);
  draw(ctx);
}
function _snglCanvasWatch(el,redraw){
  if(typeof ResizeObserver!=="function")return;
  let first=true;
  new ResizeObserver(function(){if(first){first=false;return}redraw()}).observe(el);
}
`

// canvasElementRef is the element a canvas, or a drawn image source, is: a
// `<canvas>`, or the `<img>` the drawing's picture is handed to.
func canvasElementRef(m *canvasutil.Meta) *ir.Ident {
	if isImageDrawing(m.Node) {
		return &ir.Ident{Name: m.ID, Type: ir.NativeNamed("HTMLImageElement")}
	}
	return &ir.Ident{Name: m.ID, Type: ir.NativeNamed("HTMLCanvasElement")}
}

// drawCtxType is the context a canvas's draw func is handed.
func drawCtxType(draw []ir.Stmt) *ir.Type {
	return canvasutil.CtxType(draw, ir.NativeNamed("CanvasRenderingContext2D"))
}

// drawFuncType is a canvas's draw func: it takes the context and paints.
func drawFuncType(m *canvasutil.Meta) *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Params: []*ir.Param{{Name: "ctx", Type: drawCtxType(m.Draw)}}, Return: ir.TypVoid}}
}

// voidFuncType is func(), a callback handed nothing that returns nothing.
func voidFuncType() *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: ir.TypVoid}}
}
