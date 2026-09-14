package lower

import (
	"fmt"
	"strconv"

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

// isTreeless reports whether a node's declaration belongs to no family, which
// is what `#[tree.none]` says and what `effect` and `timer` carry.
func isTreeless(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Tree == nil
}

// treelessChildren is the children a canvas keeps: the members of no family,
// under whatever control flow they were written in. Everything else is a shape
// and is the draw function now.
//
// They stay *children* rather than being lifted anywhere, because that is where
// their lifetime is: an effect written inside a canvas lives as long as the
// canvas is rendered, the same as one written inside a vbox. And the control
// flow is kept with them for the same reason -- `if enabled` around a timer is
// what makes toggling the gate a mount and an unmount.
//
// It mirrors emitShapes, which is the other half of the same split: everything
// that walk reaches through, this one reaches through too, or a bracket written
// under it is dropped exactly as it was before any of this.
func treelessChildren(children []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, c := range children {
		switch v := c.(type) {
		case *ir.NodeInst:
			if isTreeless(v) {
				out = append(out, v)
			}
		case *ir.If:
			body, els := treelessChildren(v.Body), treelessChildren(v.Else)
			if len(body) == 0 && len(els) == 0 {
				continue
			}
			out = append(out, &ir.If{AST: v.AST, Cond: v.Cond, Body: body, Else: els})
		case *ir.For:
			body, els := treelessChildren(v.Body), treelessChildren(v.Else)
			if len(body) == 0 && len(els) == 0 {
				continue
			}
			out = append(out, &ir.For{
				AST: v.AST, Key: v.Key, Value: v.Value, Iter: v.Iter,
				ElemType: v.ElemType, Body: body, Else: els,
				KeySym: v.KeySym, ValueSym: v.ValueSym,
			})
		case *ir.ErrorBoundary:
			// Flattened, as emitShapes flattens it: what it wrapped is what the
			// canvas holds, and the fallback is already an `if` among them.
			out = append(out, treelessChildren(v.Children)...)
		case *ir.ContextProvider:
			kids := treelessChildren(v.Children)
			if len(kids) == 0 {
				continue
			}
			cp := *v
			cp.Children = kids
			out = append(out, &cp)
		}
	}
	return out
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
				drawFunc := buildDrawFunc(v, funcs, name)
				*funcs = append(*funcs, drawFunc)
				v.CanvasDraw = drawFunc
				// The shapes are the draw function now, but a child belonging
				// to no tree is not one of them: `effect` and `timer` are
				// placed in a drawing precisely because they render nothing,
				// and dropping them with the shapes left the passes that turn
				// them into schedules nothing to find. A canvas that animates
				// itself is the case, and it silently did not.
				v.Children = treelessChildren(v.Children)
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
		case *ir.ErrorBoundary:
			// A boundary and a context provider are how the nodes under them
			// got there rather than nodes, exactly as the `if` above is. A
			// canvas written under either was left unlowered and rendered
			// nothing at all -- on bubbletea the whole drawing was absent from
			// the model, with no diagnostic anywhere.
			//
			// Children alone: passBoundaryFailed runs well before this and has
			// already rewritten the pair into a reactive `if` over its flag,
			// so Failed is empty by now and walking it would emit the fallback
			// a second time.
			walkCanvasStmts(v.Children, funcs, counter)
		case *ir.ContextProvider:
			walkCanvasStmts(v.Children, funcs, counter)
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

// buildDrawFunc generates a draw function for a canvas node's shape children.
func buildDrawFunc(canvas *ir.NodeInst, funcs *[]*ir.Func, name string) *ir.Func {
	ctx := &ir.Param{Name: "ctx", Type: ir.TypDyn}
	var body []ir.Stmt
	emitShapes(canvas.Children, &body, funcs, drawEnv{
		ctx:    ctx,
		width:  nodeIntProp(canvas, "width"),
		height: nodeIntProp(canvas, "height"),
	})
	return &ir.Func{
		Name:        name,
		Params:      []*ir.Param{ctx},
		Return:      ir.TypVoid,
		Synthesized: true,
		Block:       body,
	}
}

// emitShapes emits draw calls for each shape child.
// drawEnv is what a draw body is written against: the context, and the size of
// the coordinate space the shapes are placed in. The size is the node's own
// declared width and height, which is a compile-time property -- so a handler
// that asks for it gets a literal and no platform's draw-function signature
// has to grow.
type drawEnv struct {
	ctx           *ir.Param
	width, height int
}

func emitShapes(children []ir.Stmt, body *[]ir.Stmt, funcs *[]*ir.Func, env drawEnv) {
	for _, s := range children {
		switch v := s.(type) {
		case *ir.NodeInst:
			emitShape(v, body, funcs, env)
		case *ir.If:
			// The draw function is imperative, so the conditional and the loop
			// a canvas body was written with survive into it. Skipping them --
			// which is what reading only NodeInsts did -- silently dropped
			// every shape a program drew from data.
			var then, otherwise []ir.Stmt
			emitShapes(v.Body, &then, funcs, env)
			emitShapes(v.Else, &otherwise, funcs, env)
			*body = append(*body, &ir.If{AST: v.AST, Cond: v.Cond, Body: then, Else: otherwise})
		case *ir.For:
			var loop, empty []ir.Stmt
			emitShapes(v.Body, &loop, funcs, env)
			emitShapes(v.Else, &empty, funcs, env)
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
			//
			// Children alone, for the reason walkCanvasStmts gives: the
			// fallback is already an `if` inside them by the time this runs.
			emitShapes(v.Children, body, funcs, env)
		case *ir.ContextProvider:
			emitShapes(v.Children, body, funcs, env)
		}
	}
}

// foldPayloadReads replaces a read of the payload's field with the field's
// own value, so `e.ctx.save()` becomes a call on the context rather than one
// on a struct literal.
//
// Substituting the parameter puts the whole literal at every use, which is
// wrong twice over: it allocates a record per read, and in JavaScript a
// statement opening with `{` is a block, so `{ctx: …}.ctx.save()` does not
// parse. Folding here rather than leaving it to the optimizer, which has
// already run by the time this pass does.
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

// dimLit is a canvas dimension as the literal a handler parameter binds to.
func dimLit(v int) ir.Expr {
	return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(v)}
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

// emitShape emits save / applyStyle / primitive-draw / recurse / restore for one shape.
func emitShape(ni *ir.NodeInst, body *[]ir.Stmt, funcs *[]*ir.Func, env drawEnv) {
	// A member of no tree is not a shape and paints nothing -- `effect` and
	// `timer` are placed in a drawing precisely because they render nothing.
	// Bracketing one emitted a save and a restore with no drawing between them.
	if isTreeless(ni) {
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
		*body = append(*body, canvasCall(env.ctx, "CanvasSave"))
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
	if drawn, ok := primitiveDrawBody(ni, env); ok {
		*body = append(*body, drawn...)
		if len(ni.Children) > 0 {
			emitShapes(ni.Children, body, funcs, env)
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
		emitShapes(shapeBody(ni), body, funcs, env)
	}

	if len(ni.Children) > 0 {
		emitShapes(ni.Children, body, funcs, env)
	}

	if bracket {
		*body = append(*body, canvasCall(env.ctx, "CanvasRestore"))
	}
}
