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
	if pkg == nil {
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

// walkCanvasStmts finds canvas containers (NodeInsts with list<shape> ChildrenType)
// and transforms their shape children into a draw function.
func walkCanvasStmts(stmts []ir.Stmt, funcs *[]*ir.Func, counter *int) {
	for _, s := range stmts {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		if isShapeContainer(ni) {
			name := fmt.Sprintf("_canvasDraw%d", *counter)
			*counter++
			drawFunc := buildDrawFunc(ni.Children, funcs, name)
			*funcs = append(*funcs, drawFunc)
			ni.CanvasDraw = drawFunc
			ni.Children = nil
		}
	}
}

// isShapeContainer reports whether a NodeInst has list<shape> ChildrenType.
func isShapeContainer(ni *ir.NodeInst) bool {
	if ni.Component == nil || ni.Component.ChildrenType == nil {
		return false
	}
	ct := ni.Component.ChildrenType
	return ct.Kind == ir.TypeList &&
		len(ct.Elems) > 0 &&
		ct.Elems[0].Kind == ir.TypeShape
}

// buildDrawFunc generates a draw function for a set of shape children.
func buildDrawFunc(children []ir.Stmt, funcs *[]*ir.Func, name string) *ir.Func {
	var body []ir.Stmt
	emitShapes(children, &body, funcs)
	return &ir.Func{
		Name:        name,
		Params:      []*ir.Param{{Name: "ctx", Type: ir.TypDyn}},
		Return:      ir.TypVoid,
		Synthesized: true,
		Block:       body,
	}
}

// emitShapes emits draw calls for each shape child.
func emitShapes(children []ir.Stmt, body *[]ir.Stmt, funcs *[]*ir.Func) {
	for _, s := range children {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		emitShape(ni, body, funcs)
	}
}

// ctxExpr returns an Ident for the ctx draw-function parameter.
func ctxExpr() *ir.Ident {
	return &ir.Ident{Name: "ctx", Type: ir.TypDyn}
}

// canvasCall builds a CallStmt invoking a CanvasIntrinsic.
func canvasCall(intrinsicName string, extraArgs ...ir.Expr) *ir.CallStmt {
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
	args = append(args, ir.CallArg{Name: "ctx", Value: ctxExpr()})
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
	return &ir.Literal{Type: ir.TypFloat, Raw: "0"}
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
func emitShape(ni *ir.NodeInst, body *[]ir.Stmt, funcs *[]*ir.Func) {
	*body = append(*body, canvasCall("CanvasSave"))

	if hasArg(ni, "style") {
		*body = append(*body, canvasCall("CanvasApplyStyle", argVal(ni, "style")))
	}

	switch ni.Name {
	case "rect":
		*body = append(*body, canvasCall("CanvasDrawRect",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "w"), argVal(ni, "h")))
	case "circle":
		*body = append(*body, canvasCall("CanvasDrawCircle",
			argVal(ni, "cx"), argVal(ni, "cy"), argVal(ni, "r")))
	case "ellipse":
		*body = append(*body, canvasCall("CanvasDrawEllipse",
			argVal(ni, "cx"), argVal(ni, "cy"), argVal(ni, "rx"), argVal(ni, "ry")))
	case "line":
		*body = append(*body, canvasCall("CanvasDrawLine",
			argVal(ni, "x1"), argVal(ni, "y1"), argVal(ni, "x2"), argVal(ni, "y2")))
	case "path":
		*body = append(*body, canvasCall("CanvasDrawPath", argVal(ni, "cmds")))
	case "canvasText":
		*body = append(*body, canvasCall("CanvasDrawText",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "content")))
	case "image":
		*body = append(*body, canvasCall("CanvasDrawImage",
			argVal(ni, "x"), argVal(ni, "y"), argVal(ni, "w"), argVal(ni, "h"), argVal(ni, "src")))
	// user-defined shapes: emit only save/restore (children handled by recursion below)
	}

	if len(ni.Children) > 0 {
		emitShapes(ni.Children, body, funcs)
	}

	*body = append(*body, canvasCall("CanvasRestore"))
}
