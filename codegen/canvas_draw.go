package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A drawing is read off the tree, not lifted out of it.
//
// There used to be a lowering pass that walked every canvas, turned its shape
// children into a synthesized `_canvasDrawN` function, hung that off the node
// and stripped the children. The function is still what a platform emits --
// drawing is imperative, so the tree of shapes has to become a sequence of
// calls somewhere -- but it is built here, at analysis time, and belongs to
// nobody: it is in no package's, component's or window's func list, and the
// generic func-emission path never meets it. That is what let the pass go, and
// with it the name match every platform needed to skip the funcs it invented.
//
// What a canvas is remains a fact about declarations: ir.IsShapeContainer.

// Canvas is one drawing in the program: the node the shapes were written
// under, and the function that paints them.
type Canvas struct {
	// Node is the canvas instantiation. Non-nil always -- on a target whose
	// lowering flattened the tree it is reached through LocalVar.CanvasNode.
	Node *ir.NodeInst
	// Local is the flattened createNode statement, or nil on a target that
	// kept its tree. Platforms key their widget off whichever they hold.
	Local *ir.LocalVar
	Draw  *ir.Func
	// ID is the node id a platform addresses the widget by -- the synthesized
	// `__nN` on a flattened target.
	ID string
	// Width and Height are the coordinate space the shapes were placed in, and
	// Scaling what the platform does when the room it lays the canvas out in
	// is not that size. Empty Scaling means the declaration's default.
	Width, Height int
	Scaling       string
}

// Canvases is every drawing the package describes, in the order the tree holds
// them: each component's body then its funcs, each window's body then its
// funcs, then the package's own.
//
// The order is the draw functions' names, which a platform emits as method
// names -- so it is the order the deleted pass numbered in rather than
// ir.Owners', and an owner's funcs come after its body because that is where a
// flattened canvas ends up: passDeclarative moves a canvas local out of the
// body and into that same owner's render func, which preserves the order
// within an owner but not across one.
func Canvases(pkg *ir.Package) []Canvas {
	if pkg == nil || !pkg.UsesDrawShapes() {
		return nil
	}
	var out []Canvas
	owner := func(body []ir.Stmt, funcs []*ir.Func) {
		collectCanvases(body, &out)
		for _, fn := range funcs {
			if fn != nil {
				collectCanvases(fn.Block, &out)
			}
		}
	}
	for _, comp := range pkg.Components {
		if comp != nil {
			owner(comp.Body, comp.Funcs)
		}
	}
	for _, w := range pkg.Windows {
		if w != nil {
			owner(w.Body, w.Funcs)
		}
	}
	owner(nil, pkg.Funcs)
	for i := range out {
		c := &out[i]
		c.Draw = buildDrawFunc(c.Node, fmt.Sprintf("_canvasDraw%d", i))
		c.Width = nodeIntProp(c.Node, "width")
		c.Height = nodeIntProp(c.Node, "height")
		c.Scaling = nodeEnumProp(c.Node, "scalingMode")
		if c.Local != nil {
			c.ID = c.Local.Name
		} else {
			c.ID = c.Node.ID
		}
	}
	return out
}

// CanvasDraws is Canvases keyed both ways, which is what a platform emitting
// one node at a time wants: it holds a *ir.NodeInst on a render-model target
// and a *ir.LocalVar on a target whose tree was flattened.
type CanvasDraws struct {
	byNode  map[*ir.NodeInst]*Canvas
	byLocal map[*ir.LocalVar]*Canvas
	all     []Canvas
}

func NewCanvasDraws(pkg *ir.Package) *CanvasDraws {
	cs := &CanvasDraws{all: Canvases(pkg)}
	// Deduped by node: a canvas reached both as a NodeInst and as a flattened
	// local is one drawing. Nothing produces both today -- a target either
	// flattens or does not -- and keying by node is what says so.
	cs.byNode = make(map[*ir.NodeInst]*Canvas, len(cs.all))
	cs.byLocal = make(map[*ir.LocalVar]*Canvas, len(cs.all))
	for i := range cs.all {
		c := &cs.all[i]
		cs.byNode[c.Node] = c
		if c.Local != nil {
			cs.byLocal[c.Local] = c
		}
	}
	return cs
}

// All is every drawing, in name order.
func (cs *CanvasDraws) All() []Canvas {
	if cs == nil {
		return nil
	}
	return cs.all
}

// ForNode and ForLocal answer nil for a node that is not a canvas, so a caller
// may ask of every node it meets.
func (cs *CanvasDraws) ForNode(n *ir.NodeInst) *Canvas {
	if cs == nil {
		return nil
	}
	return cs.byNode[n]
}

func (cs *CanvasDraws) ForLocal(lv *ir.LocalVar) *Canvas {
	if cs == nil {
		return nil
	}
	return cs.byLocal[lv]
}

// collectCanvases finds the drawings in one statement list, reaching through
// everything that says when or how many rather than what.
//
// A canvas is often one arm of a target test -- a drawing where there are
// pixels, something else where there are not -- and a boundary or a context
// override is how the nodes under it got there rather than a node. Stopping at
// NodeInsts left such a canvas unlowered and its shapes rendered as widgets.
func collectCanvases(stmts []ir.Stmt, out *[]Canvas) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(v) {
				*out = append(*out, Canvas{Node: v})
				continue
			}
			collectCanvases(v.Children, out)
		case *ir.LocalVar:
			// The tree was flattened; the canvas node rode across on the
			// createNode statement. Its shapes were never flattened, so this
			// is the same drawing reached by the other route.
			if v.CanvasNode != nil {
				*out = append(*out, Canvas{Node: v.CanvasNode, Local: v})
			}
		case *ir.Window:
			collectCanvases(v.Body, out)
		case *ir.If:
			collectCanvases(v.Body, out)
			collectCanvases(v.Else, out)
		case *ir.For:
			collectCanvases(v.Body, out)
			collectCanvases(v.Else, out)
		case *ir.ErrorBoundary:
			// Children alone: passBoundaryFailed has already rewritten the
			// pair into a reactive `if` over its flag, so walking Failed would
			// find the fallback a second time.
			collectCanvases(v.Children, out)
		case *ir.ContextProvider:
			collectCanvases(v.Children, out)
		}
	}
}

// buildDrawFunc is the function that paints one canvas's shape children.
//
// It is an ir.Func because that is what the five platform emitters already
// take, and it carries no owner because nothing owns it: it is built here and
// handed to whoever emits this canvas.
func buildDrawFunc(canvas *ir.NodeInst, name string) *ir.Func {
	ctx := &ir.Param{Name: "ctx", Type: ir.TypDyn}
	var body []ir.Stmt
	emitShapes(canvas.Children, &body, drawEnv{
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
	if ir.IsTreelessNode(ni) {
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
		out = append(out, foldPayloadReads(substituteParams(ir.CloneStmtsSharingDecls(h.Func.Block), bindings))...)
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
	return substituteParams(ir.CloneStmtsSharingDecls(ni.Component.Body), bindings)
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
	return rewriteExprsIn(stmts, func(e ir.Expr) ir.Expr {
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
}

// substituteParams replaces every *ir.Ident whose Sym is a *ir.Param matched by
// name in bindings with a copy of the bound argument expression.
//
// A copy per use: later phases rewrite IR in place, so two positions holding
// one argument must not share the node. The declarations inside it stay shared,
// which is what CloneExprSharingDecls means and what an argument naming a var
// requires.
func substituteParams(stmts []ir.Stmt, bindings map[string]ir.Expr) []ir.Stmt {
	return rewriteExprsIn(stmts, func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok {
			return e
		}
		if _, isParam := id.Sym.(*ir.Param); !isParam {
			return e
		}
		if bound, ok := bindings[id.Name]; ok {
			return ir.CloneExprSharingDecls(bound)
		}
		return e
	})
}

// rewriteExprsIn applies transform to every expression in stmts, in place,
// and does not descend into what it replaced.
func rewriteExprsIn(stmts []ir.Stmt, transform func(ir.Expr) ir.Expr) []ir.Stmt {
	_ = ir.Rewrite(stmts, func(n ir.Node) (ir.Node, error) {
		e, isExpr := n.(ir.Expr)
		if !isExpr {
			return n, nil
		}
		if out := transform(e); out != e {
			return out, ir.SkipDir
		}
		return n, nil
	})
	return stmts
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

// nodeEnumProp is an enum-valued prop as the member name, or "" when absent.
func nodeEnumProp(n *ir.NodeInst, name string) string {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if id, ok := p.Value.(*ir.Ident); ok && id.Member != "" {
			return id.Member
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			return lit.Value
		}
	}
	return ""
}

// nodeIntProp is a numeric or measurement prop as an int, or 0 when absent.
func nodeIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			raw := strings.TrimSuffix(lit.Value, lit.Suffix)
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
