package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passCanvas = pass{
	name:    "Canvas",
	enabled: func(c Caps) bool { return c.Canvas },
	apply:   lowerCanvas,
}

func lowerCanvas(pkg *ir.Package, _ Caps, _ Options) error {
	if !pkg.UsesTree(drawPkg, shapeTree) {
		return nil
	}
	var counter int
	for _, comp := range pkg.Components {
		walkCanvasStmts(comp.Body, &comp.Funcs, &counter)
	}
	for _, w := range pkg.Windows {
		walkCanvasStmts(w.Body, &w.Funcs, &counter)
	}
	return nil
}

// walkCanvasStmts finds canvas containers (NodeInsts that host shapes)
// and transforms their shape children into a draw function. Recurses into
// layout NodeInsts (vbox, hbox, etc.) and ir.Window nodes to find canvases
// nested at any depth.
func walkCanvasStmts(stmts []ir.Stmt, funcs *[]*ir.Func, counter *int) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.NodeInst:
			if isShapeContainer(v) {
				name := fmt.Sprintf("_canvasDraw%d", *counter)
				*counter++
				drawFunc := buildDrawFunc(v.Children, funcs, name)
				*funcs = append(*funcs, drawFunc)
				v.CanvasDraw = drawFunc
				v.Children = nil
			} else {
				walkCanvasStmts(v.Children, funcs, counter)
			}
		case *ir.Window:
			walkCanvasStmts(v.Body, &v.Funcs, counter)
		}
	}
}

// drawPkg and shapeTree identify sngl://draw's own tree. The pass emits that
// package's drawing primitives, so a tree declared elsewhere is not its
// business however it is spelled.
const (
	drawPkg   = "sngl://draw"
	shapeTree = "shape"
)

// isShapeContainer reports whether a NodeInst hosts shapes without being one:
// a canvas, not a rect. A shape's own children are drawn by the emitter that
// draws it, so only the outermost host becomes a draw function.
func isShapeContainer(ni *ir.NodeInst) bool {
	return ni.Component != nil &&
		ni.Component.Tree == nil &&
		ir.IsTreeNamed(treeHosted(ni.Component), drawPkg, shapeTree)
}

// treeHosted is the segmented tree a component's default slot accepts, or nil.
// A canvas hosts shapes; a rect, being one, hosts its own and is not a
// container.
func treeHosted(comp *ir.Component) *ir.StructDef {
	for _, s := range comp.Slots {
		if s.Name != ir.DefaultSlot || s.Content == nil || s.Content.Kind != ir.TypeStruct {
			continue
		}
		if sd, ok := s.Content.Decl.(*ir.StructDef); ok && sd.IsTree {
			return sd
		}
	}
	return nil
}

// hostsTree reports whether a component's default slot accepts a segmented
// tree, which is what makes it a rendered position rather than a wrapper the
// inliner may compose away.
func hostsTree(comp *ir.Component) bool {
	return treeHosted(comp) != nil
}

// buildDrawFunc generates a draw function for a set of shape children.
func buildDrawFunc(children []ir.Stmt, funcs *[]*ir.Func, name string) *ir.Func {
	ctx := &ir.Param{Name: "ctx", Type: ir.TypDyn}
	var body []ir.Stmt
	emitShapes(children, &body, funcs, ctx)
	return &ir.Func{
		Name:        name,
		Params:      []*ir.Param{ctx},
		Return:      ir.TypVoid,
		Synthesized: true,
		Block:       body,
	}
}

// emitShapes emits draw calls for each shape child.
func emitShapes(children []ir.Stmt, body *[]ir.Stmt, funcs *[]*ir.Func, ctx *ir.Param) {
	for _, s := range children {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		emitShape(ni, body, funcs, ctx)
	}
}

// ctxExpr returns an Ident for the ctx draw-function parameter.
func ctxExpr(ctx *ir.Param) *ir.Ident {
	return &ir.Ident{Name: ctx.Name, Type: ctx.Type, Sym: ctx, Synthesized: true}
}

// canvasCall builds a CallStmt invoking a CanvasIntrinsic.
func canvasCall(ctx *ir.Param, intrinsicName string, extraArgs ...ir.Expr) *ir.CallStmt {
	def := ir.LookupIntrinsic(intrinsicName)
	if def == nil {
		panic(fmt.Sprintf("passCanvas: unknown intrinsic %q", intrinsicName))
	}
	fn := &ir.Func{
		Name:      def.Name,
		Intrinsic: def.Name,
		Params:    def.Params,
		Return:    def.Return,
	}
	args := make([]ir.CallArg, 0, 1+len(extraArgs))
	args = append(args, ir.CallArg{Name: "ctx", Value: ctxExpr(ctx)})
	for _, a := range extraArgs {
		args = append(args, ir.CallArg{Value: a})
	}
	return &ir.CallStmt{
		Call: &ir.Call{
			Type: ir.TypVoid,
			Func: fn,
			Args: args,
		},
	}
}

// argVal returns the value of a named prop on a NodeInst, or a zero float literal.
func argVal(ni *ir.NodeInst, name string) ir.Expr {
	for i := range ni.Props {
		if ni.Props[i].Name == name {
			return ni.Props[i].Value
		}
	}
	return &ir.Literal{Type: ir.TypFloat, Value: "0"}
}

// hasArg reports whether a named prop is present on a NodeInst.
func hasArg(ni *ir.NodeInst, name string) bool {
	for i := range ni.Props {
		if ni.Props[i].Name == name {
			return true
		}
	}
	return false
}

// emitShape emits save / applyStyle / primitive-draw / recurse / restore for one shape.
func emitShape(ni *ir.NodeInst, body *[]ir.Stmt, funcs *[]*ir.Func, ctx *ir.Param) {
	*body = append(*body, canvasCall(ctx, "CanvasSave"))

	if hasArg(ni, "style") {
		*body = append(*body, canvasCall(ctx, "CanvasApplyStyle", argVal(ni, "style")))
	}

	switch ni.Name {
	case "rect":
		*body = append(*body, canvasCall(ctx, "CanvasDrawRect",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "w"), argVal(ni, "h")))
	case "circle":
		*body = append(*body, canvasCall(ctx, "CanvasDrawCircle",
			argVal(ni, "cx"), argVal(ni, "cy"), argVal(ni, "r")))
	case "ellipse":
		*body = append(*body, canvasCall(ctx, "CanvasDrawEllipse",
			argVal(ni, "cx"), argVal(ni, "cy"), argVal(ni, "rx"), argVal(ni, "ry")))
	case "line":
		*body = append(*body, canvasCall(ctx, "CanvasDrawLine",
			argVal(ni, "x1"), argVal(ni, "y1"), argVal(ni, "x2"), argVal(ni, "y2")))
	case "path":
		*body = append(*body, canvasCall(ctx, "CanvasDrawPath", argVal(ni, "cmds")))
	case "canvasText":
		*body = append(*body, canvasCall(ctx, "CanvasDrawText",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "content")))
	case "canvasImage":
		*body = append(*body, canvasCall(ctx, "CanvasDrawImage",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "w"), argVal(ni, "h"), argVal(ni, "src")))
		// user-defined shapes: emit only save/restore (children handled by recursion below)
	}

	if len(ni.Children) > 0 {
		emitShapes(ni.Children, body, funcs, ctx)
	}

	*body = append(*body, canvasCall(ctx, "CanvasRestore"))
}
