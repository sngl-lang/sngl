package html

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
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
			case *ir.If:
				scan(n.Body)
				scan(n.Else)
			case *ir.For:
				scan(n.Body)
				scan(n.Else)
			}
		}
	}
	scan(comp.Body)
	for _, fn := range comp.Funcs {
		if fn != nil {
			scan(fn.Block)
		}
	}
}

func (g *htmlGen) emitComponentFactory(b *strings.Builder, comp *ir.Component) {
	g.adoptElemDecl(comp)
	// Props and vars are locals of the call, so every read of one renders as a
	// bare identifier rather than `state.x` -- which is what makes the closure
	// the record.
	ctx := g.ctx.ForComponent(comp)
	ctx.InstanceClosure = true
	for _, p := range comp.Props {
		ctx = ctx.WithLocal(p.Name)
	}
	for _, v := range comp.Vars {
		ctx = ctx.WithLocal(v.Name)
	}
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

	// Function declarations hoist, so the setters and slot renderers may be
	// written before the body that calls them without a dead-zone read.
	for _, fn := range comp.Funcs {
		if fn == nil {
			continue
		}
		tr := g.newHTMLTranslator(jc)
		// The instance is the closure, so a method's receiver parameter names
		// nothing here.
		var params []*ir.Param
		for _, p := range fn.Params {
			if !p.Receiver {
				params = append(params, p)
			}
		}
		walked := &ir.Func{
			Name:   fn.Name,
			Params: params,
			Block:  codegen.WalkLowered(context.Background(), fn.Block, tr),
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
		drawn := &ir.Func{
			Name:   cv.Name,
			Params: []*ir.Param{{Name: "ctx", Type: ir.TypDyn}},
			Block:  codegen.WalkLowered(context.Background(), cv.Draw, dtr),
		}
		for _, line := range jc.EmitFuncDef(drawn) {
			b.WriteString("\t" + line + "\n")
		}
	}

	tr := g.newHTMLTranslator(jc)
	body := codegen.WalkLowered(context.Background(), comp.Body, tr)
	for _, stmt := range body {
		for _, line := range jc.EvalStmt(stmt) {
			b.WriteString("\t" + line + "\n")
		}
	}
	// Whatever the body did not attach itself is the instance's own content.
	for _, id := range tr.topLevel {
		fmt.Fprintf(b, "\t%s.appendChild(%s);\n", instanceRootLocal, id)
	}
	// After the attach, because the helper sizes the backing store from the
	// box the element was laid out in.
	for _, line := range g.canvasInitLines(tr, jc) {
		b.WriteString("\t" + line + "\n")
	}

	b.WriteString("\treturn {\n")
	fmt.Fprintf(b, "\t\t%s: %s,\n", instanceRootField, instanceRootLocal)
	fmt.Fprintf(b, "\t\t%s() { %s },\n", instanceDestroyMethod, instanceTeardownCall(comp))
	for _, p := range comp.Props {
		setter := ir.ComponentSetter(p.Name)
		if !componentHasFunc(comp, setter) {
			continue
		}
		fmt.Fprintf(b, "\t\t%s: %s,\n", instanceUpdateMethod(p.Name), setter)
	}
	b.WriteString("\t};\n}\n")
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
			fields = append(fields, fmt.Sprintf("%s: %s", p.Name, g.exprToJS(p.Value)))
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
	}
	if len(g.staticInsts) > 0 {
		b.WriteString("\n")
	}
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
