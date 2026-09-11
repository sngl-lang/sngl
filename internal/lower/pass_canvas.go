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
	if !pkg.UsesDrawShapes() {
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
		case *ir.If:
			// A canvas is often one arm of a target test -- a drawing on the
			// platforms that have pixels, something else on the ones that do
			// not. Stopping at NodeInsts left that canvas unlowered, and the
			// backend rendered its shapes as though they were widgets.
			walkCanvasStmts(v.Body, funcs, counter)
			walkCanvasStmts(v.Else, funcs, counter)
		case *ir.For:
			walkCanvasStmts(v.Body, funcs, counter)
			walkCanvasStmts(v.Else, funcs, counter)
		}
	}
}

// isShapeContainer reports whether a NodeInst hosts shapes without being one:
// a canvas, not a rect. A shape's own children are drawn by the emitter that
// draws it, so only the outermost host becomes a draw function.
func isShapeContainer(ni *ir.NodeInst) bool {
	return ni.Component != nil &&
		!ir.IsDrawShapeTree(ni.Component.Tree) &&
		ir.IsDrawShapeTree(treeHosted(ni.Component))
}

// treeHosted is the segmented tree a component's rest slot accepts, or nil.
// A canvas hosts shapes; a rect, being one, hosts its own and is not a
// container.
func treeHosted(comp *ir.Component) *ir.StructDef {
	for _, s := range comp.Slots {
		if !s.Rest || s.Content == nil || s.Content.Kind != ir.TypeStruct {
			continue
		}
		if sd, ok := s.Content.Decl.(*ir.StructDef); ok && ir.IsSegmentedTree(sd) {
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
		switch v := s.(type) {
		case *ir.NodeInst:
			emitShape(v, body, funcs, ctx)
		case *ir.If:
			// The draw function is imperative, so the conditional and the loop
			// a canvas body was written with survive into it. Skipping them --
			// which is what reading only NodeInsts did -- silently dropped
			// every shape a program drew from data.
			var then, otherwise []ir.Stmt
			emitShapes(v.Body, &then, funcs, ctx)
			emitShapes(v.Else, &otherwise, funcs, ctx)
			*body = append(*body, &ir.If{AST: v.AST, Cond: v.Cond, Body: then, Else: otherwise})
		case *ir.For:
			var loop, empty []ir.Stmt
			emitShapes(v.Body, &loop, funcs, ctx)
			emitShapes(v.Else, &empty, funcs, ctx)
			*body = append(*body, &ir.For{
				AST:      v.AST,
				Key:      v.Key,
				Value:    v.Value,
				Iter:     v.Iter,
				ElemType: v.ElemType,
				Body:     loop,
				Else:     empty,
				KeySym:   v.KeySym,
				ValueSym: v.ValueSym,
			})
		}
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

// primitiveShapeName is the shape a NodeInst draws, or "" when it is a shape
// composed of other shapes.
//
// It reads the declaration's name rather than the spelling at the call site,
// which is what the node's own Name carries: under `import draw "sngl:ui/draw"`
// that is "draw.rect", it matched nothing, and the canvas emitted a save and a
// restore with no drawing in between. A shape declared with a body is composed
// of the shapes in it, so only a body-less one is a primitive -- that is also
// what keeps a program's own `component rect` from being mistaken for this
// package's.
//
// Reached only for a body-less component now: emitShape decides the
// body-bearing case before the switch, rather than relying on a call-site name
// not to match one of these.
func primitiveShapeName(ni *ir.NodeInst) string {
	if ni.Component == nil {
		return ni.Name
	}
	return ni.Component.Name
}

// primitiveDrawBody is the statements a platform drawing primitive paints,
// taken from the handlers its call site supplied and rebound to ctx.
//
// The handler's own parameter is the context it was written against, so the
// substitution is that name to the draw function's ctx. A primitive with no
// handler paints nothing and says so by returning true with no statements --
// which is different from not being a primitive at all.
func primitiveDrawBody(ni *ir.NodeInst, ctx *ir.Param) ([]ir.Stmt, bool) {
	if ni.Component == nil || ni.Component.Intrinsic == "" {
		return nil, false
	}
	if !ir.IsSegmentedTree(ni.Component.Tree) {
		return nil, false
	}
	var out []ir.Stmt
	for _, h := range ni.Handlers {
		if h.Func == nil {
			continue
		}
		bindings := map[string]ir.Expr{}
		for _, p := range h.Func.Params {
			bindings[p.Name] = ctxExpr(ctx)
		}
		out = append(out, substituteParams(deepCloneStmts(h.Func.Block), bindings)...)
	}
	return out, true
}

// shapeBody is a composed shape's declaration body with the call site's
// arguments substituted for its props, ready to be emitted where the call
// stands. A prop the call site leaves out takes its declared default.
//
// The body is cloned: one declaration is drawn once per call site, and each
// gets its own arguments.
func shapeBody(ni *ir.NodeInst) []ir.Stmt {
	if ni.Component == nil || len(ni.Component.Body) == 0 {
		return nil
	}
	bindings := map[string]ir.Expr{}
	for _, p := range ni.Component.Props {
		if p.Default != nil {
			bindings[p.Name] = p.Default
		}
	}
	for i := range ni.Props {
		bindings[ni.Props[i].Name] = ni.Props[i].Value
	}
	return substituteParams(deepCloneStmts(ni.Component.Body), bindings)
}

// emitShape emits save / applyStyle / primitive-draw / recurse / restore for one shape.
func emitShape(ni *ir.NodeInst, body *[]ir.Stmt, funcs *[]*ir.Func, ctx *ir.Param) {
	// A shape the target implemented itself brackets its own drawing: the
	// override is the body, and what it saves, styles and restores is its
	// business. Emitting a bracket around it too gave every overridden shape
	// two nested saves and applied the style twice -- and it is the bracket a
	// hand-written override has to be able to leave out to match native
	// performance.
	//
	// A composed shape still gets one, because its style is what its children
	// inherit; that is what makes `group(style=…) { … }` work.
	// A platform primitive never gets one either, and for the same reason from
	// the other side: it *is* the drawing, and every save, style and restore
	// around it was written in the override that called it.
	isPrimitive := ni.Component != nil && ni.Component.Intrinsic != "" &&
		ir.IsSegmentedTree(ni.Component.Tree)
	// `len(Body) == 0` matters as much as the specialization: a component can
	// be specialized for this target and still have nothing in it -- a harness
	// that checks without merging this platform's extensions leaves the
	// override empty -- and such a shape falls through to the name-matched
	// translation, which needs the bracket. Reading SpecializedFor alone gave
	// it neither, and the shape drew with no style at all.
	selfBrackets := ni.Component != nil && ni.Component.SpecializedFor != "" && len(ni.Component.Body) > 0
	bracket := !isPrimitive && !selfBrackets
	if bracket {
		*body = append(*body, canvasCall(ctx, "CanvasSave"))
		if hasArg(ni, "style") {
			*body = append(*body, canvasCall(ctx, "CanvasApplyStyle", argVal(ni, "style")))
		}
	}

	// A platform primitive carries the drawing itself: its handler body is
	// what the target paints, written against the context the handler binds.
	// Spliced here with that parameter rebound to the draw function's own
	// ctx, which is the whole of what makes an override's `@draw` reach the
	// output.
	//
	// Recognised by the marks rather than by a name: an #[intrinsic]
	// declaration that is a member of a segmented tree is a rendered
	// primitive, and any handler it declares is the render. The same shape
	// serves a markup or menu tree with no change here.
	if drawn, ok := primitiveDrawBody(ni, ctx); ok {
		*body = append(*body, drawn...)
		if len(ni.Children) > 0 {
			emitShapes(ni.Children, body, funcs, ctx)
		}
		if bracket {
			*body = append(*body, canvasCall(ctx, "CanvasRestore"))
		}
		return
	}

	// A component with a body renders from it -- a composed shape, or the
	// override a target supplied. Decided before the name switch and not by
	// falling through it: primitiveShapeName returned the *call-site* name to
	// force that fallthrough, and under `import . "sngl:ui/draw"` the bare
	// "rect" matched the case it was trying to miss, so an override applied or
	// not depending on how the program spelled its import.
	if ni.Component != nil && len(ni.Component.Body) > 0 {
		emitShapes(shapeBody(ni), body, funcs, ctx)
		if len(ni.Children) > 0 {
			emitShapes(ni.Children, body, funcs, ctx)
		}
		if bracket {
			*body = append(*body, canvasCall(ctx, "CanvasRestore"))
		}
		return
	}

	switch primitiveShapeName(ni) {
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
	default:
		// A shape composed of other shapes. It is exempt from component
		// inlining -- a tree kind marks a declaration as rendered rather than
		// composed away -- so its body is expanded here instead, with the call
		// site's arguments substituted for its props. Emitting only the
		// save/restore around it is what made a program's own shape draw
		// nothing at all.
		emitShapes(shapeBody(ni), body, funcs, ctx)
	}

	if len(ni.Children) > 0 {
		emitShapes(ni.Children, body, funcs, ctx)
	}

	if bracket {
		*body = append(*body, canvasCall(ctx, "CanvasRestore"))
	}
}
