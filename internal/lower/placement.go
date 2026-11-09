package lower

import (
	"fmt"
	"maps"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passPlacement is for a platform whose every node operation is a protocol
// function (#[gen.wants(placement)]): win32. passDeclarative leaves each
// node operation an intrinsic carrying the protocol call it stands for, and
// a platform with a generator of its own -- gtk4, fyne -- places the node
// through it in Go, once per language. This writes each as the call it
// carries instead, so what is left is a program of functions, structs and
// calls every language writes alike, and the platform's generator writes
// only its entry:
//
//   - a node made, `var x Button = CreateNode(tag, made)`, is `var x = made`;
//   - a child appended, removed or put before another, and a handler
//     connected, is the call its protocol makes of them, a connect's handler
//     parameter bound to the handler -- and a handler the program did not
//     write, which lowering attaches in its place, connects nothing;
//   - a prop written is its set's call;
//   - a node attached to or taken from the application is the platform's
//     attach or detach;
//   - and every type the program writes as one of the platform's nodes is
//     the platform's handle, the return type of its #[gen.protocol(handle)].
//
// What it does not place yet -- a component built at run time -- is refused
// by name.
var passPlacement = pass{
	name:    "Placement",
	enabled: func(f Features) bool { return f.Placement },
	apply:   lowerPlacement,
}

func lowerPlacement(pkg *ir.Package, feats Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	plat := feats.platform
	handle := plat.Op(ir.ProtocolHandle)
	if handle == nil {
		return fmt.Errorf("a platform asking for placement declares the type its nodes are held as: #[gen.protocol(handle)]")
	}
	p := &placement{plat: plat, records: collectRecords(pkg), pkg: pkg}
	// Read off the node operations, which the rewrite below makes calls.
	if root := pkg.RootDecl(); root != nil {
		p.rootTops, p.rootType = bodyTops(root)
	}
	ir.RewriteBlocks(pkg, p.block)
	if p.err != nil {
		return p.err
	}
	p.buildRecords(pkg)
	if p.err != nil {
		return p.err
	}
	p.placeRoot(pkg)
	if p.err != nil {
		return p.err
	}
	liftRoot(pkg)
	p.clearCopies(pkg)
	hoistNodes(pkg)
	p.hoistShared(pkg)
	p.placeTests(pkg)
	if p.err != nil {
		return p.err
	}
	// The application is the platform's attach, and no value: nothing holds
	// it once each attach is a call.
	app := ir.Published().ApplicationType()
	kept := pkg.Vars[:0]
	for _, v := range pkg.Vars {
		if v.Type == nil || app == nil || !v.Type.Equal(app) {
			kept = append(kept, v)
		}
	}
	pkg.Vars = kept
	// A conversion to a node was the protocol's own view of a node as one;
	// every node is the handle now.
	if err := ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		if c, ok := e.(*ir.Conversion); ok && isPlacedNode(c.Type) && isPlacedNode(c.Operand.ExprType()) {
			return c.Operand, nil
		}
		return e, nil
	}); err != nil {
		return err
	}
	var retype func(t *ir.Type) *ir.Type
	retype = func(t *ir.Type) *ir.Type {
		if r := p.recordOf(t); r != nil {
			return r.typ
		}
		if isPlacedNode(t) {
			return handle.Return
		}
		return t.MapChildren(retype)
	}
	ir.RewriteTypes(pkg, retype)
	return nil
}

// hoistNodes makes each node the package body makes a package var: a
// handler, a render slot and an effect each read one, and a language writes
// the package body as a function of its own, whose locals they would not
// reach. A node is an element ref, addressed by its name, which the
// inliners left unique, so each ref of the name is the var's.
func hoistNodes(pkg *ir.Package) {
	hoisted := map[string]*ir.Var{}
	for i, s := range pkg.Body {
		lv, ok := s.(*ir.LocalVar)
		if !ok || !isPlacedNode(lv.Type) {
			continue
		}
		v := &ir.Var{Name: lv.Name, Type: lv.Type, Synthesized: true}
		hoisted[lv.Name] = v
		pkg.Vars = append(pkg.Vars, v)
		pkg.Body[i] = &ir.Assign{Target: &ir.Ident{Name: lv.Name, Type: lv.Type, Sym: v, IsElementRef: true, Synthesized: true}, Op: ast.AssignSet, Value: lv.Init}
	}
	if len(hoisted) == 0 {
		return
	}
	// A ref is the handle passDeclarative made, which no symbol names, or
	// the `#id` the program wrote, whose binding is a NodeHandle var.
	bind := func(n ir.Node) (ir.Node, error) {
		id, ok := n.(*ir.Ident)
		if !ok {
			return n, nil
		}
		v := hoisted[id.Name]
		if v == nil {
			return n, nil
		}
		switch sym := id.Sym.(type) {
		case nil:
			id.Sym = v
		case *ir.Var:
			if sym.NodeHandle {
				id.Sym = v
			}
		}
		return n, nil
	}
	_ = ir.Rewrite(pkg, bind)
	// A closure's function is a reference the walk does not follow, and a
	// lambda lifted out of a component the inliner spliced away is in no
	// list of the package's.
	for _, fn := range closureFuncs(pkg) {
		_ = ir.Rewrite(fn, bind)
	}
}

// hoistShared makes each node a function makes that another function reads
// a package var, as hoistNodes does the package body's: an effect's mount
// reads the node the render slot it is written in made. The var holds the
// node made last. A closure is left as it is, since it captured the node it
// reads -- its copy's, in a loop -- and a record's functions are, since a
// record holds what its own functions make.
func (p *placement) hoistShared(pkg *ir.Package) {
	inRecord := map[*ir.Func]bool{}
	for _, r := range p.records {
		for _, fn := range r.funcs() {
			inRecord[fn] = true
		}
	}
	var funcs []*ir.Func
	for _, fn := range pkg.Funcs {
		if !inRecord[fn] && !fn.IsTest {
			funcs = append(funcs, fn)
		}
	}
	// The local each name is, and the function making it.
	type local struct {
		lv *ir.LocalVar
		fn *ir.Func
	}
	made := map[string]local{}
	for _, fn := range funcs {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if lv, ok := n.(*ir.LocalVar); ok && isPlacedNode(lv.Type) && made[lv.Name].lv == nil && packageVar(pkg, lv.Name) == nil {
				made[lv.Name] = local{lv, fn}
			}
			return nil
		})
	}
	// refers reports whether id is a ref of the local l: its symbol, none,
	// as a ref passDeclarative made names a node, or the `#id` the program
	// wrote, whose binding is a NodeHandle var.
	refers := func(id *ir.Ident, l local) bool {
		if l.lv == nil || id.Name != l.lv.Name {
			return false
		}
		switch sym := id.Sym.(type) {
		case nil:
			return true
		case *ir.Var:
			return sym == l.lv.Sym || sym.NodeHandle
		}
		return false
	}
	shared := map[string]*ir.Var{}
	for _, fn := range funcs {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if id, ok := n.(*ir.Ident); ok && made[id.Name].fn != fn && refers(id, made[id.Name]) {
				shared[id.Name] = nil
			}
			return nil
		})
	}
	for _, name := range slices.Sorted(maps.Keys(shared)) {
		l := made[name]
		v := &ir.Var{Name: name, Type: l.lv.Type, Synthesized: true}
		shared[name] = v
		pkg.Vars = append(pkg.Vars, v)
		// The local stays, which the function and what it closes over
		// read: a loop's copy its own.
		l.fn.Block = ir.RewriteBlocks(l.fn.Block, func(stmts []ir.Stmt) []ir.Stmt {
			out := stmts[:0:0]
			for _, s := range stmts {
				out = append(out, s)
				if s == ir.Stmt(l.lv) {
					local := &ir.Ident{Name: name, Type: l.lv.Type, IsElementRef: true, Synthesized: true}
					if l.lv.Sym != nil {
						local.Sym = l.lv.Sym
					}
					out = append(out, &ir.Assign{Target: &ir.Ident{Name: name, Type: l.lv.Type, Sym: v, IsElementRef: true, Synthesized: true}, Op: ast.AssignSet, Value: local})
				}
			}
			return out
		})
	}
	if len(shared) == 0 {
		return
	}
	for _, fn := range funcs {
		_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok && shared[id.Name] != nil && made[id.Name].fn != fn && refers(id, made[id.Name]) {
				cp := *id
				cp.Sym = shared[id.Name]
				return &cp, nil
			}
			return n, nil
		})
	}
}

// closureFuncs is the function of each closure reachable from root, and of
// each closure those reach.
func closureFuncs(root any) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	var walk func(any)
	walk = func(r any) {
		_ = ir.Walk(r, func(n ir.Node) error {
			if cl, ok := n.(*ir.Closure); ok && cl.Func != nil && !seen[cl.Func] {
				seen[cl.Func] = true
				out = append(out, cl.Func)
				walk(cl.Func)
			}
			return nil
		})
	}
	walk(root)
	return out
}

type placement struct {
	plat *ir.NodeProtocol
	// fires is where a test fires each event the program handles, by
	// "<id>.<event>" as the test names it (recordFire).
	fires map[string]fireAt
	// held is the package var holding each node a test reads that a
	// function other than the package body makes, by its id (holdRead).
	held map[string]*ir.Var
	// records are the components the program builds at run time, as
	// placement writes them (placerecords.go).
	records map[*ir.Component]*record
	// copies is the package's map from each copy's key to the node, for
	// each node loops render that a test reaches one copy of (copyWrite).
	copies map[string]*ir.Var
	// rootTops are the nodes the body of the component the build is rooted
	// at makes and puts nowhere, and rootType the type they are held as.
	rootTops []string
	rootType *ir.Type
	pkg      *ir.Package
	err      error
}

func (p *placement) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
}

// isPlacedNode reports whether t is a node of the platform's: one of its
// #[intrinsic] components, a family of them, `ui.node`, or a member of one --
// a node ref an override wrote, which a closure capturing it is typed as
// `ui.input`, the override it was written in. Nothing else is left typed as
// a component once the inliners ran, a component built at run time being
// refused here; a build target's family is no tree's.
func isPlacedNode(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeComponent {
		return false
	}
	c, _ := t.Decl.(*ir.Component)
	switch {
	case c == nil:
		return false
	case c.Intrinsic != "":
		return true
	case c.IsFamily():
		return !ir.IsBuildTargetFamily(c)
	}
	return c.Family != nil && c.Family.IsFamily() && !ir.IsBuildTargetFamily(c.Family)
}

// nodeOp is the node operation call is, or "".
func nodeOp(call *ir.Call) string {
	if call == nil || call.Func == nil {
		return ""
	}
	switch op := call.Func.Intrinsic; op {
	case ir.NodeOpCreateNode, ir.NodeOpAppendChild, ir.NodeOpRemoveChild, ir.NodeOpInsertBefore,
		ir.NodeOpAttachHandler, ir.NodeOpDetachHandler,
		ir.NodeOpCreateComponent, ir.NodeOpComponentRoot, ir.NodeOpUpdateComponent, ir.NodeOpDestroyComponent:
		return op
	}
	return ""
}

func (p *placement) block(stmts []ir.Stmt) []ir.Stmt {
	out := stmts[:0:0]
	for _, s := range stmts {
		out = append(out, p.stmt(s)...)
	}
	return out
}

func (p *placement) stmt(s ir.Stmt) []ir.Stmt {
	switch n := s.(type) {
	case *ir.LocalVar:
		call, _ := n.Init.(*ir.Call)
		switch nodeOp(call) {
		case "":
			return []ir.Stmt{s}
		case ir.NodeOpCreateComponent, ir.NodeOpComponentRoot:
			placed, _ := p.instanceOp(call)
			cp := *n
			cp.Init = placed
			return []ir.Stmt{&cp}
		case ir.NodeOpCreateNode:
			if len(call.Args) != 2 {
				tag, _ := call.Args[0].Value.(*ir.Literal)
				p.fail("%s is made by no #[gen.protocol(create)], which a platform asking for placement declares for each node", litValue(tag))
				return nil
			}
			cp := *n
			cp.Init = call.Args[1].Value
			return []ir.Stmt{&cp}
		default:
			p.fail("a component built at run time (%s) is not placed yet", call.Func.Intrinsic)
			return nil
		}
	case *ir.CallStmt:
		return p.call(s, n.Call)
	case *ir.Assign:
		sel, ok := n.Target.(*ir.Select)
		if !ok {
			return []ir.Stmt{s}
		}
		id, ok := sel.Operand.(*ir.Ident)
		if !ok || !id.IsElementRef || !isPlacedNode(id.Type) {
			return []ir.Stmt{s}
		}
		if sel.Field == ir.CopyProp {
			return []ir.Stmt{p.copyWrite(id, n.Value)}
		}
		// spliceSets put the prop's set in as the value written.
		comp, _ := id.Type.Decl.(*ir.Component)
		if set, ok := n.Value.(*ir.Call); ok && comp.Protocol.Setter(sel.Field) != nil {
			return []ir.Stmt{&ir.CallStmt{Call: set}}
		}
		p.fail("%s.%s is written, and %s declares no #[gen.protocol(set)] for it", id.Name, sel.Field, comp.Name)
		return nil
	}
	return []ir.Stmt{s}
}

func (p *placement) call(s ir.Stmt, call *ir.Call) []ir.Stmt {
	op := nodeOp(call)
	if op == "" {
		return []ir.Stmt{s}
	}
	arg := func(i int) ir.Expr { return call.Args[i].Value }
	switch op {
	case ir.NodeOpAppendChild, ir.NodeOpRemoveChild:
		if ir.IsAppParent(arg(0)) {
			pop := ir.ProtocolAttach
			if op == ir.NodeOpRemoveChild {
				pop = ir.ProtocolDetach
			}
			return p.platformCall(pop, arg(1))
		}
		if len(call.Args) == 3 {
			return through(arg(2))
		}
		if isNoWidget(arg(1)) {
			return nil
		}
		pop := ir.ProtocolAppend
		if op == ir.NodeOpRemoveChild {
			pop = ir.ProtocolRemove
		}
		if fn := p.rootContainerOp(arg(0), pop); fn != nil {
			put, err := protocolSplice(fn, []ir.Expr{arg(0), arg(1)})
			if err != nil {
				p.fail("%v", err)
				return nil
			}
			return through(put)
		}
		p.fail("%s takes no child: it declares no #[gen.protocol(append)]", componentName(arg(0).ExprType()))
		return nil
	case ir.NodeOpInsertBefore:
		return p.insertBefore(arg(0), arg(1), arg(2))
	case ir.NodeOpAttachHandler:
		handler := arg(2)
		if unwrittenHandler(handler) {
			return nil
		}
		if len(call.Args) != 4 {
			event, _ := arg(1).(*ir.Literal)
			p.fail("@%s of %s is handled, and it declares no #[gen.protocol(on)] for it", litValue(event), componentName(arg(0).ExprType()))
			return nil
		}
		// The connect's handler parameter is the one its splice left
		// unbound.
		connect, err := ir.RewriteExpr(ir.CloneExprSharingDecls(arg(3)), func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok {
				if _, ok := id.Sym.(*ir.Param); ok {
					return ir.CloneExprSharingDecls(handler), ir.SkipDir
				}
			}
			return n, nil
		})
		if err != nil {
			p.fail("%v", err)
			return nil
		}
		if event, ok := arg(1).(*ir.Literal); ok {
			p.recordFire(arg(0), event.Value, handler)
		}
		return through(connect)
	case ir.NodeOpCreateComponent, ir.NodeOpUpdateComponent, ir.NodeOpDestroyComponent:
		placed, _ := p.instanceOp(call)
		if c, ok := placed.(*ir.Call); ok {
			return []ir.Stmt{&ir.CallStmt{Call: c}}
		}
		return nil
	default:
		p.fail("a component built at run time (%s) is not placed yet", op)
		return nil
	}
}

// through is the protocol call a node operation carried, as a statement:
// protocolSplice answers a call or a return's value, and an operation's is
// the call it makes.
func through(e ir.Expr) []ir.Stmt {
	c, _ := e.(*ir.Call)
	return []ir.Stmt{&ir.CallStmt{Call: c}}
}

// platformCall is the platform's op with node as its operand.
func (p *placement) platformCall(op ir.ProtocolOp, node ir.Expr) []ir.Stmt {
	fn := p.plat.Op(op)
	if fn == nil {
		p.fail("a node is %sed at the program's root, and the platform declares no #[gen.protocol(%s)]", op, op)
		return nil
	}
	spliced, err := protocolSplice(fn, []ir.Expr{node})
	if err != nil {
		p.fail("%v", err)
		return nil
	}
	return through(spliced)
}

// insertBefore puts child into parent ahead of ref, a node or null for the
// end, through the parent's insertBefore -- or its append, at the end.
func (p *placement) insertBefore(parent, child, ref ir.Expr) []ir.Stmt {
	comp, _ := parent.ExprType().Decl.(*ir.Component)
	var insert, appendFn *ir.Func
	if comp != nil {
		insert, appendFn = comp.Protocol.Op(ir.ProtocolInsertBefore), comp.Protocol.Op(ir.ProtocolAppend)
	}
	if insert == nil && appendFn == nil {
		insert, appendFn = p.rootContainerOp(parent, ir.ProtocolInsertBefore), p.rootContainerOp(parent, ir.ProtocolAppend)
	}
	if insert == nil || appendFn == nil {
		p.fail("%s places a child a render slot renders, and declares no #[gen.protocol(insertBefore)] and append to", componentName(parent.ExprType()))
		return nil
	}
	appended, err := protocolSplice(appendFn, []ir.Expr{parent, child})
	if err != nil {
		p.fail("%v", err)
		return nil
	}
	rt := ref.ExprType()
	if rt == nil || rt.Kind != ir.TypeOption {
		inserted, err := protocolSplice(insert, []ir.Expr{parent, child, ref})
		if err != nil {
			p.fail("%v", err)
			return nil
		}
		return through(inserted)
	}
	before := &ir.Conversion{Type: rt.Elems[0], Operand: ir.CloneExprSharingDecls(ref)}
	inserted, err := protocolSplice(insert, []ir.Expr{ir.CloneExprSharingDecls(parent), ir.CloneExprSharingDecls(child), before})
	if err != nil {
		p.fail("%v", err)
		return nil
	}
	return []ir.Stmt{&ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: ref, Right: &ir.Literal{Type: ir.TypNull}},
		Body: through(appended),
		Else: through(inserted),
	}}
}

// rootContainerOp is op of the container #[gen.protocol(contents)] makes,
// where parent is typed only as a node: a slot rendering at its component's
// top renders into that container (intoContents). Nil otherwise.
func (p *placement) rootContainerOp(parent ir.Expr, op ir.ProtocolOp) *ir.Func {
	if comp, _ := parent.ExprType().Decl.(*ir.Component); comp != nil && comp.Intrinsic != "" {
		return nil
	}
	contents := p.plat.Op(ir.ProtocolContents)
	if contents == nil {
		return nil
	}
	box, _ := contents.Return.Decl.(*ir.Component)
	return box.Protocol.Op(op)
}

// unwrittenHandler reports whether h is a handler the program did not
// write: the bodyless function lowering attaches in its place.
func unwrittenHandler(h ir.Expr) bool {
	id, ok := h.(*ir.Ident)
	if !ok {
		return false
	}
	fn, ok := id.Sym.(*ir.Func)
	return ok && len(fn.Block) == 0 && fn.Foreign.Name == "" && fn.Intrinsic == ""
}

func componentName(t *ir.Type) string {
	if t != nil {
		if c, ok := t.Decl.(*ir.Component); ok {
			return c.Name
		}
	}
	return "the node"
}

func litValue(l *ir.Literal) string {
	if l == nil {
		return "a node"
	}
	return l.Value
}
