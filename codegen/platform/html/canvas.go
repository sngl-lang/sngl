package html

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterIntrinsic("js", id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
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
	// Always set fillStyle and strokeStyle so subsequent fill/stroke calls use
	// the right color. When alpha=0, _snglColor produces rgba(r,g,b,0) which
	// is fully transparent — correct behaviour without needing runtime guards.
	reg("CanvasApplyStyle", func(a []string) string {
		ctx, s := a[0], a[1]
		return strings.Join([]string{
			ctx + ".fillStyle=_snglColor(" + s + ".fill);",
			ctx + ".strokeStyle=_snglColor(" + s + ".stroke);",
			ctx + ".lineWidth=" + s + ".strokeWidth;",
			ctx + ".lineCap=" + s + ".lineCap;",
			ctx + ".lineJoin=" + s + ".lineJoin;",
			ctx + ".font=" + s + ".fontSize+\"px \"+" + s + ".fontFamily;",
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
}

// snglColorHelper converts a SNGL color struct {r,g,b,a} to CSS rgba().
// Emitted once in the JS bundle whenever canvas is present.
const snglColorHelper = "function _snglColor(c){return\"rgba(\"+c.r+\",\"+c.g+\",\"+c.b+\",\"+(c.a/255)+\")\"}\n"
