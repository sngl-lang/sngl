package html

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	// Keyed to the platform, not to "js". These render onto a canvas 2D
	// context, which only an html build has — registering them per language
	// claimed every js build can draw, which no other js platform can.
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterPlatformIntrinsic("html", id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a), nil
		})
	}

	// a[0]=ctx
	reg("CanvasSave", func(a []string) string { return a[0] + ".save()" })
	reg("CanvasRestore", func(a []string) string { return a[0] + ".restore()" })

	// a[0]=ctx, a[1]=style (CanvasStyle struct)
	// JS struct literals only contain fields explicitly set in the source;
	// CanvasStyle fields with SNGL defaults may be absent (undefined) in the
	// JS object. Guard each assignment and use || fallbacks for scalar fields.
	reg("CanvasApplyStyle", func(a []string) string {
		ctx, s := a[0], a[1]
		return strings.Join([]string{
			"if(" + s + ".fill){" + ctx + ".fillStyle=_snglColor(" + s + ".fill);}",
			"if(" + s + ".stroke){" + ctx + ".strokeStyle=_snglColor(" + s + ".stroke);}",
			ctx + ".lineWidth=" + s + ".strokeWidth||1;",
			ctx + ".lineCap=" + s + ".lineCap||\"butt\";",
			ctx + ".lineJoin=" + s + ".lineJoin||\"miter\";",
			"if(" + s + ".fontSize){" + ctx + ".font=(" + s + ".fontSize)+\"px \"+(" + s + ".fontFamily||\"sans-serif\");}",
		}, "")
	})

	// a[0]=ctx, a[1]=x, a[2]=y, a[3]=w, a[4]=h
	reg("CanvasDrawRect", func(a []string) string {
		ctx, x, y, w, h := a[0], a[1], a[2], a[3], a[4]
		return ctx + ".fillRect(" + x + "," + y + "," + w + "," + h + ");" +
			ctx + ".strokeRect(" + x + "," + y + "," + w + "," + h + ")"
	})

	// a[0]=ctx, a[1]=cx, a[2]=cy, a[3]=r
	reg("CanvasDrawCircle", func(a []string) string {
		ctx, cx, cy, r := a[0], a[1], a[2], a[3]
		return ctx + ".beginPath();" +
			ctx + ".arc(" + cx + "," + cy + "," + r + ",0,Math.PI*2);" +
			ctx + ".fill();" +
			ctx + ".stroke()"
	})

	// a[0]=ctx, a[1]=cx, a[2]=cy, a[3]=rx, a[4]=ry
	reg("CanvasDrawEllipse", func(a []string) string {
		ctx, cx, cy, rx, ry := a[0], a[1], a[2], a[3], a[4]
		return ctx + ".beginPath();" +
			ctx + ".ellipse(" + cx + "," + cy + "," + rx + "," + ry + ",0,0,Math.PI*2);" +
			ctx + ".fill();" +
			ctx + ".stroke()"
	})

	// a[0]=ctx, a[1]=x1, a[2]=y1, a[3]=x2, a[4]=y2
	reg("CanvasDrawLine", func(a []string) string {
		ctx, x1, y1, x2, y2 := a[0], a[1], a[2], a[3], a[4]
		return ctx + ".beginPath();" +
			ctx + ".moveTo(" + x1 + "," + y1 + ");" +
			ctx + ".lineTo(" + x2 + "," + y2 + ");" +
			ctx + ".stroke()"
	})

	// a[0]=ctx, a[1]=cmds (list<PathCmd>)
	reg("CanvasDrawPath", func(a []string) string {
		ctx, cmds := a[0], a[1]
		return ctx + ".beginPath();for(const _cmd of " + cmds + "){" +
			"if(_cmd.op===\"moveTo\"){" + ctx + ".moveTo(_cmd.x,_cmd.y);}" +
			"else if(_cmd.op===\"lineTo\"){" + ctx + ".lineTo(_cmd.x,_cmd.y);}" +
			"else if(_cmd.op===\"bezierTo\"){" + ctx + ".bezierCurveTo(_cmd.cx1,_cmd.cy1,_cmd.cx2,_cmd.cy2,_cmd.x,_cmd.y);}" +
			"else if(_cmd.op===\"arcTo\"){" + ctx + ".arcTo(_cmd.cx1,_cmd.cy1,_cmd.x,_cmd.y,_cmd.r);}" +
			"else if(_cmd.op===\"close\"){" + ctx + ".closePath();}" +
			"};" + ctx + ".fill();" + ctx + ".stroke()"
	})

	// a[0]=ctx, a[1]=x, a[2]=y, a[3]=content
	reg("CanvasDrawText", func(a []string) string {
		ctx, x, y, content := a[0], a[1], a[2], a[3]
		return ctx + ".fillText(" + content + "," + x + "," + y + ");" +
			ctx + ".strokeText(" + content + "," + x + "," + y + ")"
	})

	// a[0]=ctx, a[1]=x, a[2]=y, a[3]=w, a[4]=h, a[5]=src
	reg("CanvasDrawImage", func(a []string) string {
		ctx, x, y, w, h, src := a[0], a[1], a[2], a[3], a[4], a[5]
		return "(function(){const _img=new Image();_img.src=" + src + ";_img.onload=function(){" +
			ctx + ".drawImage(_img," + x + "," + y + "," + w + "," + h + ");};})()"
	})
}

// canvasSetup records a canvas element that needs its draw function wired up.
type canvasSetup struct {
	id       string
	drawFunc *ir.Func
	// w and h are the coordinate space the shapes were placed in, and scaling
	// what to do when the box is not that size. Both are needed at the draw
	// call, not only where the element is written.
	w, h    int
	scaling string
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
	return fmt.Sprintf("_snglCanvasDraw(%s,%d,%d,%q,%s)", cs.id, cs.w, cs.h, cs.scaling, cs.drawFunc.Name)
}

// canvasDrawStmt is that same call as IR, for a canvas the lowering flattened
// into a scope that is emitted as code rather than as markup -- a component
// factory, or a slot renderer. The element and the draw func are locals of
// that scope there, so both are named rather than looked up.
func canvasDrawStmt(m *canvasutil.Meta) ir.Stmt {
	num := func(v int) ir.Expr { return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(v)} }
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Name: "_snglCanvasDraw"},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: m.ID, Type: ir.TypDyn}},
			{Value: num(m.Width)},
			{Value: num(m.Height)},
			{Value: &ir.Literal{Type: ir.TypString, Value: m.Scaling}},
			{Value: &ir.Ident{Name: m.Draw.Name, Type: ir.TypDyn}},
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
		stmts := []ir.Stmt{canvasDrawStmt(m)}
		if m.Scaling != "" {
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
			{Value: &ir.Ident{Name: m.ID, Type: ir.TypDyn}},
			{Value: &ir.Lambda{Type: ir.TypDyn, Func: &ir.Func{Block: []ir.Stmt{canvasDrawStmt(m)}}}},
		},
	}}
}

// snglColorHelper converts a SNGL color struct {r,g,b,a} to CSS rgba().
// Emitted once in the JS bundle whenever canvas is present.
const snglColorHelper = "function _snglColor(c){return c?\"rgba(\"+c.r+\",\"+c.g+\",\"+c.b+\",\"+(c.a/255)+\")\":\"rgba(0,0,0,0)\"}\n"

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
const snglDrawImageHelper = `const _snglImgs=new Map();
function _snglDrawImage(ctx,src,x,y,w,h){
  let img=_snglImgs.get(src);
  if(img&&img.complete&&img.naturalWidth>0){ctx.drawImage(img,x,y,w,h);return}
  if(!img){img=new Image();_snglImgs.set(src,img);img.src=src}
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
// way in: every edge is rasterised where it lands rather than interpolated
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
