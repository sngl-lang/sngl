package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A drawing is spliced where it was written, not lifted out of it.
//
// passCanvas used to walk every canvas, turn its shape children into a
// synthesized `_canvasDrawN` function, hang that off the node and strip the
// children. Nothing is synthesized now: the shapes become the statements that
// paint them, in place, and codegen emits those wherever its own backend
// paints. What a canvas is stays a fact about declarations, ir.IsShapeContainer.

var passShapeDraw = pass{
	name:    "Canvas",
	enabled: func(c Features) bool { return c.Canvas },
	apply:   lowerShapeDraw,
}

// canvasCtxParam is the drawing context every spliced statement is written
// against. One shared *ir.Param rather than one per canvas: codegen binds the
// name once per drawing it emits, and two canvases in one program name the
// same context in their own scopes.
var canvasCtxParam = &ir.Param{Name: "ctx", Type: ir.TypDyn}

func lowerShapeDraw(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil || !pkg.UsesDrawShapes() {
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		walkCanvases(o.Stmts())
	}
	return nil
}

// walkCanvases finds the drawings in a statement list and splices each.
//
// It reaches through everything that says when or how many rather than what: a
// canvas is often one arm of a target test, and a boundary or a context
// override is how the nodes under it got there rather than a node. Stopping at
// NodeInsts left such a canvas unspliced and its shapes rendered as widgets.
func walkCanvases(stmts []ir.Stmt) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(v) {
				spliceCanvas(v)
				continue
			}
			walkCanvases(v.Children)
		case *ir.If:
			walkCanvases(v.Body)
			walkCanvases(v.Else)
		case *ir.For:
			walkCanvases(v.Body)
			walkCanvases(v.Else)
		case *ir.ErrorBoundary:
			// Children alone: passBoundaryFailed has already rewritten the
			// pair into a reactive `if` over its flag, so walking Failed would
			// find the fallback a second time.
			walkCanvases(v.Children)
		case *ir.ContextProvider:
			walkCanvases(v.Children)
		}
	}
}

// spliceCanvas replaces a canvas's shape children with the statements that
// paint them.
//
// In place, and that is the whole of the design: they stay ordinary statements
// in the tree, so the optimize pass that runs after lowering folds them and
// keeps what they call, exactly as it does for any other body. A version that
// built a function here instead put the body somewhere nothing walked -- fyne's
// applyStyle and gtk4's paint were shaken away as unreferenced, and a
// zero-alpha stroke test that should have folded to false emitted a second
// drawRect on android.
func spliceCanvas(canvas *ir.NodeInst) {
	var body []ir.Stmt
	emitShapes(canvas.Children, &body, drawEnv{
		ctx:    canvasCtxParam,
		width:  nodeIntProp(canvas, "width"),
		height: nodeIntProp(canvas, "height"),
	})
	canvas.Children = body
}

// drawEnv is what a draw body is written against: the context, and the size of
// the coordinate space the shapes are placed in. The size is the node's own
// declared width and height, which is a compile-time property -- so a handler
// that asks for it gets a literal and no platform's draw-function signature
// has to grow.
type drawEnv struct {
	ctx           *ir.Param
	width, height int
}

func emitShapes(children []ir.Stmt, body *[]ir.Stmt, env drawEnv) {
	for _, s := range children {
		switch v := s.(type) {
		case *ir.NodeInst:
			emitShape(v, body, env)
		case *ir.If:
			// The draw function is imperative, so the conditional and the loop
			// a canvas body was written with survive into it. Skipping them --
			// which is what reading only NodeInsts did -- silently dropped
			// every shape a program drew from data.
			var then, otherwise []ir.Stmt
			emitShapes(v.Body, &then, env)
			emitShapes(v.Else, &otherwise, env)
			*body = append(*body, &ir.If{AST: v.AST, Cond: v.Cond, Body: then, Else: otherwise})
		case *ir.For:
			var loop, empty []ir.Stmt
			emitShapes(v.Body, &loop, env)
			emitShapes(v.Else, &empty, env)
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
		case *ir.ErrorBoundary:
			// Flattened rather than kept: a draw function paints, and a
			// boundary has nothing to paint of its own. What it wrapped is
			// what the canvas draws, and dropping it here is what made a shape
			// under one disappear from every target's draw function while its
			// siblings drew normally.
			emitShapes(v.Children, body, env)
		case *ir.ContextProvider:
			emitShapes(v.Children, body, env)
		}
	}
}

// emitShape emits save / applyStyle / primitive-draw / recurse / restore for
// one shape.
func emitShape(ni *ir.NodeInst, body *[]ir.Stmt, env drawEnv) {
	// A member of no tree is not a shape and paints nothing -- `effect` and
	// `timer` are placed in a drawing precisely because they render nothing.
	// Bracketing one emitted a save and a restore with no drawing between them.
	//
	// It is *kept* rather than dropped: its lifetime is the canvas's, so it
	// stays a child the way it would under a vbox, and passEffect finds it
	// there. A canvas that animates itself is exactly this case, and dropping
	// these is what made one compile clean and never move.
	if ir.IsTreelessNode(ni) {
		*body = append(*body, ni)
		return
	}
	// A shape the target implemented itself brackets its own drawing: the
	// override is the body, and what it saves, styles and restores is its
	// business. Emitting a bracket around it too gave every overridden shape
	// two nested saves and applied the style twice -- and it is the bracket a
	// hand-written override has to be able to leave out to match native
	// performance.
	//
	// A composed shape still gets one, because its style is what its children
	// inherit; that is what makes `group(style=…) { … }` work. A platform
	// primitive never gets one either, and for the same reason from the other
	// side: it *is* the drawing, and every save, style and restore around it
	// was written in the override that called it.
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
		*body = append(*body, canvasCall(env.ctx, "CanvasSave"))
	}

	// A platform primitive carries the drawing itself: its handler body is
	// what the target paints, written against the context the handler binds.
	// Spliced here with that parameter rebound to the draw function's own ctx,
	// which is the whole of what makes an override's `@draw` reach the output.
	//
	// Recognised by the marks rather than by a name: an #[intrinsic]
	// declaration that is a member of a segmented tree is a rendered
	// primitive, and any handler it declares is the render. The same shape
	// serves a markup or menu tree with no change here.
	if drawn, ok := primitiveDrawBody(ni, env); ok {
		*body = append(*body, drawn...)
		if len(ni.Children) > 0 {
			emitShapes(ni.Children, body, env)
		}
		if bracket {
			*body = append(*body, canvasCall(env.ctx, "CanvasRestore"))
		}
		return
	}

	// A component with a body renders from it -- a composed shape, or the
	// override a target supplied. It is exempt from component inlining, since
	// a tree kind marks a declaration as rendered rather than composed away,
	// so its body is expanded here with the call site's arguments substituted
	// for its props.
	//
	// A shape with no body and no override for this target draws nothing, and
	// says so where it is declared: that is what the bodyless-component rule
	// reports. What used to stand here was a switch over the seven stdlib
	// shape names, which rendered any shape it did not recognise as nothing at
	// all, silently.
	if ni.Component != nil && len(ni.Component.Body) > 0 {
		emitShapes(shapeBody(ni), body, env)
	}

	if len(ni.Children) > 0 {
		emitShapes(ni.Children, body, env)
	}

	if bracket {
		*body = append(*body, canvasCall(env.ctx, "CanvasRestore"))
	}
}

// primitiveDrawBody is the statements a platform drawing primitive paints,
// taken from the handlers its call site supplied and rebound to ctx.
//
// The handler's own parameter is the context it was written against, so the
// substitution is that name to the draw function's ctx. A primitive with no
// handler paints nothing and says so by returning true with no statements --
// which is different from not being a primitive at all.
func primitiveDrawBody(ni *ir.NodeInst, env drawEnv) ([]ir.Stmt, bool) {
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
		// One parameter, the event payload, as every other handler has. A
		// primitive whose payload is a struct gets the context and the
		// coordinate space in its fields; one whose payload is the context
		// itself gets that. Which it is comes from the declaration, so the
		// binding reads the parameter's own type rather than counting.
		bindings := map[string]ir.Expr{}
		for _, p := range h.Func.Params {
			bindings[p.Name] = drawPayload(p.Type, env)
		}
		out = append(out, foldPayloadReads(substituteParams(deepCloneStmts(h.Func.Block), bindings))...)
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
		switch {
		case p.Default != nil:
			bindings[p.Name] = p.Default
		default:
			// A stdlib prop with no declared default renders as its type's
			// zero value, which is what the declaration means by leaving one
			// out. Binding nothing left the name standing in the spliced body,
			// so an override reading a prop the call site omitted -- `style`
			// on a `rect(x, y, w, h)` -- emitted an identifier nothing
			// declares and the generated code did not compile.
			if z := ir.DeclaredDefault(p.Type); z != nil {
				bindings[p.Name] = z
			}
		}
	}
	for i := range ni.Props {
		bindings[ni.Props[i].Name] = ni.Props[i].Value
	}
	return substituteParams(deepCloneStmts(ni.Component.Body), bindings)
}

// drawPayload is what a draw handler's parameter binds to.
//
// A struct payload is built field by field from the declaration: a field whose
// type is not a dimension gets the context, and `width`/`height` get the
// canvas's own declared size -- a compile-time property, so they arrive as
// literals and no platform's draw-function signature has to grow.
func drawPayload(t *ir.Type, env drawEnv) ir.Expr {
	sd := payloadStruct(t)
	if sd == nil {
		return ctxExpr(env.ctx)
	}
	lit := &ir.StructLit{Type: t, Def: sd}
	for _, f := range sd.Fields {
		var v ir.Expr
		switch f.Name {
		case "width":
			v = dimLit(env.width)
		case "height":
			v = dimLit(env.height)
		default:
			v = ctxExpr(env.ctx)
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: f.Name, Value: v})
	}
	return lit
}

// payloadStruct is the struct a payload type names, or nil when the payload is
// the context itself.
func payloadStruct(t *ir.Type) *ir.StructDef {
	if t == nil || t.Decl == nil {
		return nil
	}
	sd, ok := t.Decl.(*ir.StructDef)
	if !ok || len(sd.Fields) == 0 {
		return nil
	}
	// A context described with native fields is not a payload record: its
	// fields are host properties, not values to fill in.
	if sd.Foreign.Name != "" {
		return nil
	}
	return sd
}

// foldPayloadReads replaces a read of the payload's field with the field's own
// value, so `e.ctx.save()` becomes a call on the context rather than one on a
// struct literal.
//
// Substituting the parameter puts the whole literal at every use, which is
// wrong twice over: it allocates a record per read, and in JavaScript a
// statement opening with `{` is a block, so `{ctx: …}.ctx.save()` does not
// parse.
func foldPayloadReads(stmts []ir.Stmt) []ir.Stmt {
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		sel, ok := e.(*ir.Select)
		if !ok {
			return e
		}
		lit, ok := sel.Operand.(*ir.StructLit)
		if !ok {
			return e
		}
		for _, f := range lit.Fields {
			if f.Name == sel.Field {
				return f.Value
			}
		}
		return e
	})
	return w.stmts(stmts)
}

// canvasCall builds a CallStmt invoking a canvas intrinsic.
func canvasCall(ctx *ir.Param, intrinsicName string, extraArgs ...ir.Expr) *ir.CallStmt {
	def := ir.LookupIntrinsic(intrinsicName)
	if def == nil {
		panic(fmt.Sprintf("codegen: unknown canvas intrinsic %q", intrinsicName))
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
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: fn, Args: args}}
}

// ctxExpr returns an Ident for the ctx draw-function parameter.
func ctxExpr(ctx *ir.Param) *ir.Ident {
	return &ir.Ident{Name: ctx.Name, Type: ctx.Type, Sym: ctx, Synthesized: true}
}

// dimLit is a canvas dimension as the literal a handler parameter binds to.
func dimLit(v int) ir.Expr {
	return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(v)}
}
