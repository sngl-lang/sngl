package html

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/javascript"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// emitComponentFactories writes one factory per component this build renders as
// a live instance rather than inlining away.
//
// A closure is the record. JS gives per-instance state for nothing -- the
// component's `var`s are locals of the factory call, so two instances share no
// cell -- which is the whole of what the Go platforms need a struct for. What
// the factory returns is the instance: the node it renders as, one setter per
// prop it can absorb, and a teardown.
//
// This is what `__cf_<name>` has always been called and never been. The name
// comes from javascript.FactoryName, which the CreateComponent dispatch has
// emitted a call to since long before anything defined it.
func (g *htmlGen) emitComponentFactories(b *strings.Builder) {
	comps := g.instanceComponents()
	if len(comps) == 0 {
		return
	}
	for _, comp := range comps {
		openDecl(b, javascript.FactoryName(comp))
		g.emitComponentFactory(b, comp)
		b.WriteByte('\n')
		closeDecl(b)
	}
}

// instanceComponents is every component the package still declares apart from
// the root: after passNoInlineComponents, what survives is what could not be
// flattened, which is exactly what needs a factory.
func (g *htmlGen) instanceComponents() []*ir.Component {
	if g.pkg == nil {
		return nil
	}
	var out []*ir.Component
	for _, c := range g.pkg.Components {
		if g.isInstanceComponent(c) {
			out = append(out, c)
		}
	}
	return out
}

// pageComponents is the complement: every component whose declarations belong
// to the page's own module scope, which is the root plus whatever the build
// inlined into it.
//
// Every emitter that writes into that one scope reads this list. Each of them
// used to filter g.pkg.Components itself, which made the question five
// separate answers -- and three of them answered it wrong, each producing a
// page that named something only a factory declares.
func (g *htmlGen) pageComponents() []*ir.Component {
	if g.pkg == nil {
		return nil
	}
	var out []*ir.Component
	for _, c := range g.pkg.Components {
		if c == nil || g.isInstanceComponent(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// pageOwnsFunc reports whether fn is declared in the page's scope. Asked of
// pkg.Teardown, which arrives as a bare *ir.Func with no owner attached, so
// the only way to place it is to look for it.
func (g *htmlGen) pageOwnsFunc(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	for _, c := range g.instanceComponents() {
		if slices.Contains(c.Funcs, fn) {
			return false
		}
	}
	return true
}

// isInstanceComponent reports whether a component's declarations belong to its
// factory rather than to the page.
//
// The page's own emitters must skip these. A slot inside an instance renders
// into that instance's root, not into a page anchor, and its accumulator is
// per-instance -- emitting either at module scope gives every instance of the
// component one shared list and one shared parent, which is the same
// everything-is-a-singleton mistake the factory exists to fix.
func (g *htmlGen) isInstanceComponent(c *ir.Component) bool {
	return c != nil && c != g.rootComp && c.RuntimeInstance
}

// adoptElemDecl recovers the raw-element declaration from a factory's lowered
// body when the page's own prewalk found none.
//
// That prewalk learns the declaration by meeting a node, and a page whose whole
// content sits inside a reactive slot meets none -- so every prop in the
// factory fell through to setAttribute. Which is not a near-miss: an attribute
// is not a property, and `setAttribute("disabled", false)` disables a button,
// because HTML cannot spell an attribute's absence.
//
// The flattened node keeps its declaration on the LocalVar's type, which is
// where passDeclarative put it, so it is still recoverable here.
func (g *htmlGen) adoptElemDecl(comp *ir.Component) {
	if g.elemDecl != nil || comp == nil {
		return
	}
	var scan func(stmts []ir.Stmt)
	scan = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if n.Type != nil && n.Type.Kind == ir.TypeComponent {
					if c, ok := n.Type.Decl.(*ir.Component); ok && isElement(c) && g.elemDecl == nil {
						g.elemDecl = c
					}
				}
			case *ir.If, *ir.For, *ir.Match:
				for _, b := range ir.TransparentBlocks(n) {
					scan(*b)
				}
			}
		}
	}
	scan(comp.Stmts)
	for _, fn := range comp.Funcs {
		if fn != nil {
			scan(fn.Block)
		}
	}
}

// factorySlotAnchors is the set of slots in comp whose first render is a
// statement of the body itself, each of which the factory gives an anchor of
// its own to re-render into.
func factorySlotAnchors(comp *ir.Component) map[string]bool {
	anchored := map[string]bool{}
	initial := map[*ir.Call]bool{}
	var scan func(stmts []ir.Stmt)
	scan = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if b, ok := s.(*ir.ErrorBoundary); ok {
				scan(b.Children)
				continue
			}
			cs, ok := s.(*ir.CallStmt)
			if !ok || cs.Call == nil || cs.Call.Func == nil || len(cs.Call.Args) != 1 {
				continue
			}
			if n := slotIndexFromRenderFunc(cs.Call.Func.Name); n != "" && componentHasFunc(comp, cs.Call.Func.Name) {
				anchored[n] = true
				initial[cs.Call] = true
			}
		}
	}
	scan(comp.Stmts)
	if len(anchored) == 0 {
		return anchored
	}
	retarget := func(root any) {
		_ = walkThroughCatch(root, func(node ir.Node) error {
			call, ok := node.(*ir.Call)
			if !ok || call.Func == nil || initial[call] || len(call.Args) != 1 {
				return nil
			}
			if n := slotIndexFromRenderFunc(call.Func.Name); anchored[n] {
				// The anchor stands for the container the slot was handed.
				call.Args[0].Value = &ir.Ident{Name: slotAnchorVar(n), Type: call.Args[0].Value.ExprType(), IsElementRef: true, Synthesized: true}
			}
			return nil
		})
	}
	retarget(comp.Stmts)
	for _, fn := range comp.Funcs {
		if fn != nil {
			retarget(fn.Block)
		}
	}
	return anchored
}

// anchoredSlotCall reports whether stmt is the initial render of an anchored
// slot, and the parent it was written against.
func anchoredSlotCall(stmt ir.Stmt, anchored map[string]bool) (string, ir.Expr, bool) {
	cs, ok := stmt.(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.Func == nil || len(cs.Call.Args) != 1 {
		return "", nil, false
	}
	n := slotIndexFromRenderFunc(cs.Call.Func.Name)
	if !anchored[n] {
		return "", nil, false
	}
	return n, cs.Call.Args[0].Value, true
}

func (g *htmlGen) emitComponentFactory(b *strings.Builder, comp *ir.Component) {
	g.adoptElemDecl(comp)
	anchored := factorySlotAnchors(comp)
	// Props and vars are locals of the call, so every read of one renders as a
	// bare identifier rather than `state.x` -- which is what makes the closure
	// the record.
	names := make([]string, 0, len(comp.Props)+len(comp.Vars))
	for _, p := range comp.Props {
		names = append(names, p.Name)
	}
	for _, v := range comp.Vars {
		names = append(names, v.Name)
	}
	ctx := g.ctx.ForComponent(comp).WithLocals(names...)
	ctx.ClosureMethods = true
	jc := javascript.NewIRContext(ctx)

	fmt.Fprintf(b, "function %s(props) {\n", javascript.FactoryName(comp))

	// The instance's own node, and the parent a reactive slot inside the body
	// renders into. Always a wrapper: a component with two top-level nodes has
	// no single element to be, and an instance whose root moved would leave
	// its parent holding a node that is no longer in the tree.
	fmt.Fprintf(b, "\tconst %s = document.createElement(\"div\");\n", instanceRootLocal)
	fmt.Fprintf(b, "\t%s.style.display = \"contents\";\n", instanceRootLocal)

	for _, p := range comp.Props {
		read := fmt.Sprintf("props.%s", p.Name)
		if p.Default != nil {
			// Parenthesized: an arrow function binds looser than `??`, so a
			// func-typed prop's default closed the expression at the `=>` and
			// esbuild rejected the whole page.
			read = fmt.Sprintf("%s ?? (%s)", read, jc.EvalExpr(p.Default))
		}
		fmt.Fprintf(b, "\tlet %s = %s;\n", p.Name, read)
	}
	for _, v := range comp.Vars {
		// __root is the wrapper, already bound above.
		if v.Name == slotRootSentinel {
			continue
		}
		init := "null"
		if v.Init != nil {
			init = jc.EvalExpr(v.Init)
		}
		fmt.Fprintf(b, "\tlet %s = %s;\n", v.Name, init)
	}

	// What the factory creates is its own, in every function it declares as
	// well as its body: a setter or a slot renderer naming the factory's `t`
	// means that one and not an element of the page writing a `#t`.
	own := codegen.CreatedIDs(comp.Stmts)
	for _, fn := range comp.Funcs {
		if fn != nil {
			maps.Copy(own, codegen.CreatedIDs(fn.Block))
		}
	}
	// Function declarations hoist, so the setters and slot renderers may be
	// written before the body that calls them without a dead-zone read.
	for _, fn := range comp.Funcs {
		if fn == nil {
			continue
		}
		tr := g.newHTMLTranslator(jc)
		tr.own = maps.Clone(own)
		params := fn.Params
		if len(params) > 0 && params[0].Receiver {
			params = params[1:]
		}
		walked := &ir.Func{
			Name:   fn.Name,
			Params: params,
			Block:  codegen.WalkLowered(context.Background(), fn.Block, tr),
			// The colour the call sites read, as html.go's synthesized funcs
			// keep it: a waiting query's mount in a component built at run
			// time lost it, an await in a sync function.
			IsAsync: fn.IsAsync,
		}
		for _, line := range jc.EmitFuncDef(walked) {
			b.WriteString("\t" + line + "\n")
		}
	}

	// A drawing written in this component's body paints into elements this
	// factory creates, so its routine is declared here where they are in
	// scope. Emitted at page scope instead, it closed over nothing and every
	// instance drew a blank canvas.
	for i := range g.canvasDraws.All() {
		cv := &g.canvasDraws.All()[i]
		if cv.Owner != comp {
			continue
		}
		dtr := g.newHTMLTranslator(jc)
		dtr.own = maps.Clone(own)
		drawn := &ir.Func{
			Name:   cv.Name,
			Params: []*ir.Param{{Name: "ctx", Type: drawCtxType(cv.Draw)}},
			Block:  codegen.WalkLowered(context.Background(), cv.Draw, dtr),
		}
		for _, line := range jc.EmitFuncDef(drawn) {
			b.WriteString("\t" + line + "\n")
		}
	}

	tr := g.newHTMLTranslator(jc)
	tr.own = maps.Clone(own)
	body := codegen.WalkLowered(context.Background(), comp.Stmts, tr)
	topLevel := make(map[string]bool, len(tr.topLevel))
	for _, id := range tr.topLevel {
		topLevel[id] = true
	}
	var rootOrder []string
	for _, stmt := range body {
		if lv, ok := stmt.(*ir.LocalVar); ok && topLevel[lv.Name] {
			rootOrder = append(rootOrder, lv.Name)
		}
		if n, parent, ok := anchoredSlotCall(stmt, anchored); ok {
			anchor := slotAnchorVar(n)
			fmt.Fprintf(b, "\tconst %s = document.createElement(\"span\");\n", anchor)
			fmt.Fprintf(b, "\t%s.style.display = \"contents\";\n", anchor)
			if id, isID := parent.(*ir.Ident); isID && id.Name == slotRootSentinel {
				rootOrder = append(rootOrder, anchor)
			} else {
				fmt.Fprintf(b, "\t%s.appendChild(%s);\n", jc.EvalExpr(parent), anchor)
			}
			fmt.Fprintf(b, "\t__renderSlot%s(%s);\n", n, anchor)
			continue
		}
		for _, line := range jc.EvalStmt(stmt) {
			b.WriteString("\t" + line + "\n")
		}
	}
	for _, id := range tr.topLevel {
		if !slices.Contains(rootOrder, id) {
			rootOrder = append(rootOrder, id)
		}
	}
	// Whatever the body did not attach itself is the instance's own content,
	// in the order the body wrote it.
	for _, id := range rootOrder {
		fmt.Fprintf(b, "\t%s.appendChild(%s);\n", instanceRootLocal, id)
	}
	// After the attach, because the helper sizes the backing store from the
	// box the element was laid out in.
	for _, line := range g.canvasInitLines(tr, jc) {
		b.WriteString("\t" + line + "\n")
	}

	reads := g.instanceReadsOf(comp)
	if len(reads) > 0 {
		b.WriteString("\tconst __inst = {\n")
	} else {
		b.WriteString("\treturn {\n")
	}
	fmt.Fprintf(b, "\t\t%s: %s,\n", instanceRootField, instanceRootLocal)
	fmt.Fprintf(b, "\t\t%s() { %s },\n", instanceDestroyMethod, instanceTeardownCall(comp))
	for _, p := range comp.Props {
		setter := ir.ComponentSetter(p.Name)
		if !componentHasFunc(comp, setter) {
			continue
		}
		fmt.Fprintf(b, "\t\t%s: %s,\n", instanceUpdateMethod(p.Name), setter)
	}
	// What a test reads of the instance, which it finds by its root
	// (emitInstanceReads).
	for _, r := range reads {
		if r.Func != nil {
			fmt.Fprintf(b, "\t\t%s: (...a) => %s(...a),\n", instanceCallMember(r.Member), r.Func.Name)
			continue
		}
		fmt.Fprintf(b, "\t\t%s: () => %s,\n", instanceReadMember(r.Member), r.Var.Name)
	}
	b.WriteString("\t};\n")
	if len(reads) > 0 {
		fmt.Fprintf(b, "\t%s.%s = __inst;\n\treturn __inst;\n", instanceRootLocal, instanceOnRoot)
	}
	b.WriteString("}\n")
}

// instanceOnRoot is the property of an instance's root element holding the
// instance, for the reads a test makes through it.
const instanceOnRoot = "__sngl"

func instanceReadMember(member string) string { return "__read_" + member }
func instanceCallMember(member string) string { return "__call_" + member }

// instanceReadsOf is each distinct member a test reads or calls through an
// instance of comp.
func (g *htmlGen) instanceReadsOf(comp *ir.Component) []codegen.InstanceRead {
	var out []codegen.InstanceRead
	seen := map[string]bool{}
	for _, r := range g.instanceReads() {
		key := r.Member
		if r.Func != nil {
			key = "()" + key
		}
		if r.Comp == comp && !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

// instanceReads is codegen.InstanceReads of the package, once per build.
func (g *htmlGen) instanceReads() []codegen.InstanceRead {
	if g.reads == nil {
		g.reads = codegen.InstanceReads(g.pkg)
		if g.reads == nil {
			g.reads = []codegen.InstanceRead{}
		}
	}
	return g.reads
}

// instanceTeardownCall is the body of an instance's __destroy: the teardown
// passEffect built for this component's own brackets, or nothing when it holds
// none. Empty rather than absent so the shape of an instance does not vary.
func instanceTeardownCall(comp *ir.Component) string {
	if componentHasFunc(comp, lower.TeardownFunc) {
		return lower.TeardownFunc + "();"
	}
	return ""
}

func componentHasFunc(comp *ir.Component, name string) bool {
	for _, f := range comp.Funcs {
		if f != nil && f.Name == name {
			return true
		}
	}
	return false
}

// staticInstance is one instance of a factory component written at a position
// no reactive slot governs: built once at startup, placed once, and never
// rebuilt or destroyed while the page lives.
type staticInstance struct {
	id   string
	node *ir.NodeInst
}

// renderStaticInstance writes the anchor a static instance's root is appended
// into and records the instance for emitStaticInstances.
//
// The alternative -- what the static renderer used to do on meeting one -- was
// to inline the component's body as markup. That body has been flattened into
// imperative statements for the factory by then, so the markup came out empty
// and the props and vars the renderer mirrored into `state` on the way past
// were dead. An instance is built by its factory or not at all.
func (g *htmlGen) renderStaticInstance(b *strings.Builder, n *ir.NodeInst, depth int) {
	if n.ID == "" {
		// The lowering stamps an id on every instance a handler can update;
		// one it never names still needs a binding to be placed through.
		n.ID = g.allocID()
	}
	fmt.Fprintf(b, "%s<span data-sngl-inst=%q style=\"display:contents\"></span>\n",
		strings.Repeat("  ", depth), n.ID)
	g.staticInsts = append(g.staticInsts, staticInstance{id: n.ID, node: n})
}

// emitStaticInstances builds each recorded instance and puts its root where
// the node was written. The binding is the instance itself, which is what an
// UpdateComponent in a handler goes on to call a setter on.
func (g *htmlGen) emitStaticInstances(b *strings.Builder) {
	for _, si := range g.staticInsts {
		fields := make([]string, 0, len(si.node.Props))
		for _, p := range si.node.Props {
			if p.Name == "" {
				continue
			}
			fields = append(fields, fmt.Sprintf("%s: %s", p.Name, g.instancePropJS(p.Value)))
		}
		// The anchor is bound rather than used inline because the position
		// outlives the instance: a #[construct] prop written from state
		// destroys this instance and puts a fresh one back here, and
		// ir.InstancePlaceName is the name that lowering's InsertBefore
		// against it uses. `let` for the instance itself, for the same
		// reason -- the rebuild assigns it.
		fmt.Fprintf(b, "const %s = document.querySelector('[data-sngl-inst=%q]');\n",
			ir.InstancePlaceName(si.id), si.id)
		fmt.Fprintf(b, "let %s = %s({%s});\n",
			si.id, javascript.FactoryName(si.node.Component), strings.Join(fields, ", "))
		fmt.Fprintf(b, "%s.appendChild(%s.%s);\n",
			ir.InstancePlaceName(si.id), si.id, instanceRootField)
		// Marked as OnComponentRoot marks the root of one a slot builds.
		if isProgramID(si.id) {
			fmt.Fprintf(b, "%s.%s.setAttribute(%q, %q);\n", si.id, instanceRootField, instanceMark, si.id)
			if key := si.node.Prop(ir.CopyProp); key != nil && si.node.Component.KeyedCopy {
				fmt.Fprintf(b, "%s.%s.setAttribute(%q, %s);\n", si.id, instanceRootField, copyAttr, g.exprToJS(key))
			}
		}
	}
	if len(g.staticInsts) > 0 {
		b.WriteString("\n")
	}
}

// instancePropJS is a static instance's prop value as the page script
// writes it. A handler it is handed is lowered as the page's own handlers
// are, so a patch in it writes the page's element by the variable it was
// emitted as rather than by the `#id` the lowering named it by.
func (g *htmlGen) instancePropJS(v ir.Expr) string {
	lam, ok := v.(*ir.Lambda)
	if !ok || lam.Func == nil {
		return g.exprToJS(v)
	}
	jc := g.scopedJC()
	for _, p := range lam.Func.Params {
		jc = jc.WithLocal(p.Name)
	}
	fn := *lam.Func
	fn.Block = codegen.WalkLowered(context.Background(), lam.Func.Block, g.newHTMLTranslator(jc))
	for _, s := range fn.Block {
		g.collectLoweredRefs(s)
	}
	cp := *lam
	cp.Func = &fn
	return jc.EvalExpr(&cp)
}

// isStaticInstanceID reports whether id names a static instance or the
// position it is placed at, both of which emitStaticInstances binds. The
// loweredRefs emitter must not also declare either: the instance is a record
// the factory returned rather than an element to look up, and the position is
// the anchor the markup carries rather than a node the program named.
func (g *htmlGen) isStaticInstanceID(id string) bool {
	for _, si := range g.staticInsts {
		if si.id == id || id == ir.InstancePlaceName(si.id) {
			return true
		}
	}
	return false
}

const (
	// instanceRootLocal is the wrapper element inside a factory. It shares the
	// name of the slot lowering's root sentinel on purpose: a reactive slot in
	// the component body is emitted as `__renderSlotN(__root)`, and inside a
	// factory that name resolves to this wrapper with no rewriting.
	instanceRootLocal = "__root"
	slotRootSentinel  = "__root"
)
