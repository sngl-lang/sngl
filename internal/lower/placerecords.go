package lower

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A component built at run time -- a row a keyed `for` keeps, a slot body
// lifted into a component of its own -- is placed as a record: a struct
// holding its vars, the nodes its body makes and the node it renders as,
// which the package's functions build, update and take apart. The struct is
// a box (ir.StructDef.RefBox), a reference every holder shares, as a
// language whose struct is a value holds a box through a pointer.
//
//	struct row__inst { __prop_label string; __n0 Button; __root node }
//	func row__new(label string) row__inst       // the body, building the record
//	func row____set_label(__self row__inst, __v string)
//	func row____snglTeardown(__self row__inst)  // where the component has one
//
// CreateComponent is the call of its constructor, ComponentRoot the read of
// its root, UpdateComponent the call of the prop's setter and
// DestroyComponent the call of its teardown: taking the root out of the
// tree is the parent's RemoveChild, which the render that destroys the
// instance makes too. Inside the record, each of its vars and nodes is read
// through __self, and a closure its body makes captures the field, as it
// captured the var: one that reads a var of the record's directly, or calls
// a function of it, reaches no __self, and is refused.

// recordSelf is the parameter a record's functions take it as, and the local
// its constructor builds it in.
const recordSelf = "__self"

// record is a component built at run time, as placement writes it.
type record struct {
	comp *ir.Component
	sd   *ir.StructDef
	typ  *ir.Type
	new  *ir.Func
	// teardown is the component's teardown, now a function of the
	// package's taking the record, or nil where it has none; destroy is
	// what DestroyComponent calls, the teardown and letting go of what a
	// test held of the record (holdRead).
	teardown *ir.Func
	destroy  *ir.Func
	// tops are the nodes the body makes and puts nowhere, which its root is
	// one of, and nodeType the type the root is held as.
	tops     []string
	nodeType *ir.Type
	// owned are the component's functions, the package's once buildRecord
	// moved them.
	owned []*ir.Func
}

// recordRoot is the field a record holds the node it renders as in, the var
// reactivity gives a component whose body renders a slot at its top.
const recordRoot = "__root"

// funcs are the record's functions: its constructor and its component's,
// each the package's once buildRecord ran.
func (r *record) funcs() []*ir.Func {
	return append([]*ir.Func{r.new, r.destroy}, r.owned...)
}

// collectRecords is a record for each component the program builds at run
// time, its struct and constructor declared and filled in later
// (buildRecords): the calls the node operations become need them first.
func collectRecords(pkg *ir.Package) map[*ir.Component]*record {
	out := map[*ir.Component]*record{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || nodeOp(call) != ir.NodeOpCreateComponent || len(call.Args) == 0 {
			return nil
		}
		id, _ := call.Args[0].Value.(*ir.Ident)
		if id == nil {
			return nil
		}
		comp, _ := id.Sym.(*ir.Component)
		if comp == nil || out[comp] != nil {
			return nil
		}
		sd := &ir.StructDef{Name: comp.Name + "__inst", RefBox: true}
		r := &record{comp: comp, sd: sd, typ: &ir.Type{Kind: ir.TypeStruct, Decl: sd}}
		r.new = &ir.Func{Name: comp.Name + "__new", Return: r.typ}
		r.destroy = &ir.Func{Name: comp.Name + "__destroy", Return: ir.TypVoid, Params: []*ir.Param{{Name: recordSelf, Type: r.typ}}}
		for _, p := range comp.Props {
			param := p.Sym
			if param == nil {
				param = &ir.Param{Name: p.Name, Type: p.Type}
			}
			r.new.Params = append(r.new.Params, param)
		}
		for _, fn := range comp.Funcs {
			if fn.Name == TeardownFunc {
				r.teardown = fn
			}
		}
		r.tops, r.nodeType = bodyTops(comp)
		out[comp] = r
		return nil
	})
	return out
}

// bodyTops is each node comp's body makes, or an instance's root it takes,
// that nothing in comp puts into a parent.
func bodyTops(comp *ir.Component) ([]string, *ir.Type) {
	var made []string
	var nodeType *ir.Type
	for _, s := range comp.Stmts {
		lv, ok := s.(*ir.LocalVar)
		if !ok {
			continue
		}
		call, _ := lv.Init.(*ir.Call)
		if op := nodeOp(call); op == ir.NodeOpCreateNode || op == ir.NodeOpComponentRoot {
			made = append(made, lv.Name)
			if op == ir.NodeOpComponentRoot || nodeType == nil {
				nodeType = lv.Type
			}
		}
	}
	put := map[string]bool{}
	_ = ir.Walk(comp, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || len(call.Args) < 2 {
			return nil
		}
		if op := nodeOp(call); op == ir.NodeOpAppendChild || op == ir.NodeOpInsertBefore {
			if id, ok := call.Args[1].Value.(*ir.Ident); ok {
				put[id.Name] = true
			}
		}
		return nil
	})
	var tops []string
	for _, id := range made {
		if !put[id] {
			tops = append(tops, id)
		}
	}
	return tops, nodeType
}

// recordOf is the record a value of t is, or nil.
func (p *placement) recordOf(t *ir.Type) *record {
	if t == nil || t.Kind != ir.TypeComponent && t.Kind != ir.TypeInstance {
		return nil
	}
	comp, _ := t.Decl.(*ir.Component)
	return p.records[comp]
}

// instanceOp is the node operation on an instance call makes, as placed:
// the constructor's call, a read of the root, a setter's call or the
// teardown's. ok is false where call is none of the four.
func (p *placement) instanceOp(call *ir.Call) (ir.Expr, bool) {
	arg := func(i int) ir.Expr { return call.Args[i].Value }
	switch nodeOp(call) {
	case ir.NodeOpCreateComponent:
		id, _ := arg(0).(*ir.Ident)
		comp, _ := id.Sym.(*ir.Component)
		r := p.records[comp]
		lit, _ := arg(1).(*ir.StructLit)
		var args []ir.CallArg
		for _, prop := range comp.Props {
			var v ir.Expr
			if lit != nil {
				for _, f := range lit.Fields {
					if f.Name == prop.Name {
						v = f.Value
					}
				}
			}
			if v == nil {
				v = prop.Default
			}
			if v == nil {
				v = ir.ZeroExpr(prop.Type)
			}
			args = append(args, ir.CallArg{Value: v})
		}
		return &ir.Call{Func: r.new, Args: args, Type: r.typ}, true
	case ir.NodeOpComponentRoot:
		r := p.recordOf(arg(0).ExprType())
		if r == nil {
			p.fail("the root of a %s, which is built at run time by no record", componentName(arg(0).ExprType()))
			return nil, true
		}
		return &ir.Select{Operand: arg(0), Field: recordRoot, Type: r.nodeType}, true
	case ir.NodeOpUpdateComponent:
		r := p.recordOf(arg(0).ExprType())
		prop, _ := arg(1).(*ir.Literal)
		if r == nil || prop == nil {
			p.fail("a prop written to a %s built at run time by no record", componentName(arg(0).ExprType()))
			return nil, true
		}
		setter := ir.ComponentSetter(prop.Value)
		i := slices.IndexFunc(r.comp.Funcs, func(f *ir.Func) bool { return f.Name == setter })
		if i < 0 {
			p.fail("%s's prop %s is written, and it has no setter", r.comp.Name, prop.Value)
			return nil, true
		}
		return &ir.Call{Func: r.comp.Funcs[i], Args: []ir.CallArg{{Value: arg(0)}, {Value: arg(2)}}, Type: ir.TypVoid}, true
	case ir.NodeOpDestroyComponent:
		r := p.recordOf(arg(0).ExprType())
		if r == nil {
			p.fail("a %s destroyed, which is built at run time by no record", componentName(arg(0).ExprType()))
			return nil, true
		}
		return &ir.Call{Func: r.destroy, Args: []ir.CallArg{{Value: arg(0)}}, Type: ir.TypVoid}, true
	}
	return nil, false
}

// buildRecords writes each record: its struct, its constructor, and its
// component's functions as the package's, each taking the record.
func (p *placement) buildRecords(pkg *ir.Package) {
	for _, comp := range slices.Clone(pkg.Components) {
		r := p.records[comp]
		if r == nil {
			continue
		}
		p.buildRecord(pkg, r)
		pkg.Components = slices.DeleteFunc(pkg.Components, func(c *ir.Component) bool { return c == comp })
	}
}

func (p *placement) buildRecord(pkg *ir.Package, r *record) {
	comp := r.comp
	// The fields: the component's vars, then the nodes its body makes.
	fields := map[string]*ir.StructField{}
	vars := map[*ir.Var]bool{}
	add := func(name string, t *ir.Type) {
		if fields[name] == nil {
			f := &ir.StructField{Name: name, Type: t}
			fields[name] = f
			r.sd.Fields = append(r.sd.Fields, f)
		}
	}
	for _, v := range comp.Vars {
		vars[v] = true
		add(v.Name, v.Type)
	}
	for _, s := range comp.Stmts {
		if lv, ok := s.(*ir.LocalVar); ok && (isPlacedNode(lv.Type) || p.recordOf(lv.Type) != nil) {
			add(lv.Name, lv.Type)
		}
	}
	add(recordRoot, r.nodeType)
	pkg.Structs = append(pkg.Structs, r.sd)

	funcs := map[*ir.Func]bool{}
	for _, fn := range comp.Funcs {
		funcs[fn] = true
	}
	// through rewrites what root reads of the record to reads through self,
	// and each call of the record's functions to hand it self.
	through := func(root any, self func() ir.Expr) {
		_ = ir.Rewrite(root, func(n ir.Node) (ir.Node, error) {
			switch x := n.(type) {
			case *ir.Ident:
				f := fields[x.Name]
				if f == nil {
					return n, nil
				}
				if v, ok := x.Sym.(*ir.Var); ok && !vars[v] && !v.NodeHandle {
					return n, nil
				}
				if _, ok := x.Sym.(*ir.Param); ok {
					return n, nil
				}
				// The field's type: NoRef leaves a read of a var it boxed
				// typed as what the box holds, and the var says it is a box.
				return &ir.Select{Operand: self(), Field: f.Name, Type: f.Type}, ir.SkipDir
			case *ir.Call:
				if x.Func != nil && funcs[x.Func] && !takesSelf(x.Func, r) {
					cp := *x
					cp.Args = append([]ir.CallArg{{Value: self()}}, x.Args...)
					return &cp, nil
				}
			}
			return n, nil
		})
	}

	// The constructor: the record, its vars set as the component declares
	// them, its body, and its root.
	selfVar := &ir.Var{Name: recordSelf, Type: r.typ, Synthesized: true}
	local := func() ir.Expr { return &ir.Ident{Name: recordSelf, Sym: selfVar, Type: r.typ, Synthesized: true} }
	field := func(name string, t *ir.Type) ir.Expr {
		return &ir.Select{Operand: local(), Field: name, Type: t}
	}
	// A var whose initializer reads nothing of the record's starts in the
	// literal, which a field it leaves out would start as its zero -- a
	// box's a box of its own, made only to be replaced. The rest are set
	// after, in the order declared.
	lit := &ir.StructLit{Type: r.typ}
	var after []ir.Stmt
	for _, v := range comp.Vars {
		if v.Init == nil {
			continue
		}
		init := ir.CloneExprSharingDecls(v.Init)
		if readsRecord(init, vars) {
			after = append(after, &ir.Assign{Target: field(v.Name, v.Type), Op: ast.AssignSet, Value: init})
			continue
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: v.Name, Value: init})
	}
	body := append([]ir.Stmt{&ir.LocalVar{Name: recordSelf, Type: r.typ, Sym: selfVar, Init: lit}}, after...)
	rootRef := func() ir.Expr {
		return &ir.Ident{Name: recordRoot, Type: r.nodeType, IsElementRef: true, Synthesized: true}
	}
	stmts := comp.Stmts
	single := len(r.tops) == 1 && !usesRoot(comp)
	if !single {
		var ok bool
		if stmts, ok = p.intoContents(comp.Name, stmts, r.tops, rootRef); !ok {
			return
		}
	}
	for _, s := range stmts {
		if lv, ok := s.(*ir.LocalVar); ok && fields[lv.Name] != nil {
			body = append(body, &ir.Assign{Target: field(lv.Name, lv.Type), Op: ast.AssignSet, Value: lv.Init})
			continue
		}
		body = append(body, s)
	}
	if single {
		body = append(body, &ir.Assign{Target: field(recordRoot, r.nodeType), Op: ast.AssignSet, Value: &ir.Ident{Name: r.tops[0], Type: r.nodeType, IsElementRef: true, Synthesized: true}})
	}
	body = append(body, &ir.Return{Value: local()})
	r.new.Block = body
	through(r.new.Block, local)
	pkg.Funcs = append(pkg.Funcs, r.new)

	// Its functions, each the package's, handed the record first.
	for _, fn := range comp.Funcs {
		var self *ir.Param
		if takesSelf(fn, r) {
			self = fn.Params[0]
			self.Type = r.typ
		} else {
			self = &ir.Param{Name: recordSelf, Type: r.typ}
			fn.Params = append([]*ir.Param{self}, fn.Params...)
		}
		param := func() ir.Expr { return &ir.Ident{Name: self.Name, Sym: self, Type: r.typ, Synthesized: true} }
		through(fn.Block, param)
		fn.Name = comp.Name + "__" + strings.TrimPrefix(fn.Name, "__")
		pkg.Funcs = append(pkg.Funcs, fn)
	}
	r.owned = comp.Funcs
	comp.Funcs = nil

	// The destroy: its teardown, where it has one. A return besides, so the
	// function has a body for what holdRead adds before it.
	self := r.destroy.Params[0]
	if r.teardown != nil {
		r.destroy.Block = append(r.destroy.Block, &ir.CallStmt{Call: &ir.Call{Func: r.teardown, Args: []ir.CallArg{{Value: &ir.Ident{Name: self.Name, Sym: self, Type: r.typ, Synthesized: true}}}, Type: ir.TypVoid}})
	}
	r.destroy.Block = append(r.destroy.Block, &ir.Return{})
	pkg.Funcs = append(pkg.Funcs, r.destroy)

	// A closure the record makes captured what it reads; one reading the
	// record itself has no __self to read it through.
	for _, fn := range closureFuncs(r.new) {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.Ident:
				if v, ok := x.Sym.(*ir.Var); ok && vars[v] {
					p.fail("a closure %s makes reads its %s directly, which placement does not reach yet", comp.Name, v.Name)
				}
			case *ir.Call:
				if x.Func != nil && funcs[x.Func] {
					p.fail("a closure %s makes calls its %s, which placement does not reach yet", comp.Name, x.Func.Name)
				}
			}
			return nil
		})
	}
}

// takesSelf reports whether fn, a function of r's component, already takes
// the record first: a method the program declared on it, whose receiver it
// is.
func takesSelf(fn *ir.Func, r *record) bool {
	if len(fn.Params) == 0 {
		return false
	}
	t := fn.Params[0].Type
	if t == r.typ {
		return true
	}
	return fn.Params[0].Receiver && t != nil && t.Decl == r.comp
}

// usesRoot reports whether comp renders into its own __root -- a slot at the
// top of its body, which renders into a container the record makes first.
func usesRoot(comp *ir.Component) bool {
	found := false
	_ = ir.Walk(comp, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && id.Name == recordRoot {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// intoContents is stmts, the body of the component named owner, rendering
// into the container the platform's #[gen.protocol(contents)] makes, which
// root names: made first, so a slot at the top renders into it, and each of
// tops put into it ahead of the first statement after it that renders into
// the root, the rest at the end, so they stand in the order written.
func (p *placement) intoContents(owner string, stmts []ir.Stmt, tops []string, root func() ir.Expr) ([]ir.Stmt, bool) {
	contents := p.plat.Op(ir.ProtocolContents)
	if contents == nil {
		p.fail("%s renders more than one node, or a slot, at its top, and the platform declares no #[gen.protocol(contents)] to hold them", owner)
		return nil, false
	}
	box, _ := contents.Return.Decl.(*ir.Component)
	appendFn := box.Protocol.Op(ir.ProtocolAppend)
	if appendFn == nil {
		p.fail("#[gen.protocol(contents)] returns a %s, which declares no #[gen.protocol(append)]", box.Name)
		return nil, false
	}
	made, err := protocolSplice(contents, nil)
	if err != nil {
		p.fail("%v", err)
		return nil, false
	}
	out := []ir.Stmt{&ir.Assign{Target: root(), Op: ast.AssignSet, Value: made}}
	isTop := map[string]bool{}
	for _, t := range tops {
		isTop[t] = true
	}
	var pending []*ir.LocalVar
	flush := func() bool {
		for _, lv := range pending {
			top := &ir.Ident{Name: lv.Name, Type: lv.Type, IsElementRef: true, Synthesized: true}
			// Set only where there is one: a nil *Var is a symbol.
			if lv.Sym != nil {
				top.Sym = lv.Sym
			}
			put, err := protocolSplice(appendFn, []ir.Expr{root(), top})
			if err != nil {
				p.fail("%v", err)
				return false
			}
			out = append(out, through(put)...)
		}
		pending = nil
		return true
	}
	for _, s := range stmts {
		if rendersInto(s) && !flush() {
			return nil, false
		}
		out = append(out, s)
		if lv, ok := s.(*ir.LocalVar); ok && isTop[lv.Name] {
			pending = append(pending, lv)
		}
	}
	if !flush() {
		return nil, false
	}
	return out, true
}

// rendersInto reports whether s renders into its component's root.
func rendersInto(s ir.Stmt) bool {
	found := false
	_ = ir.Walk(s, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && id.Name == recordRoot {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// placeRoot shows the component a build is rooted at, which a test takes: its
// one node attached to the application, or, where its body renders more or a
// slot at its top, the container holding them, as a record holds its own
// (intoContents). liftRoot then makes the body the package's.
func (p *placement) placeRoot(pkg *ir.Package) {
	root := pkg.RootDecl()
	if root == nil {
		return
	}
	tops, nodeType := p.rootTops, p.rootType
	attach := func(node ir.Expr) bool {
		shown, err := protocolSplice(p.plat.Op(ir.ProtocolAttach), []ir.Expr{node})
		if err != nil {
			p.fail("%v", err)
			return false
		}
		root.Stmts = append(root.Stmts, through(shown)...)
		return true
	}
	if p.plat.Op(ir.ProtocolAttach) == nil {
		return
	}
	if len(tops) == 1 && !usesRoot(root) {
		attach(&ir.Ident{Name: tops[0], Type: nodeType, IsElementRef: true, Synthesized: true})
		return
	}
	var v *ir.Var
	for _, rv := range root.Vars {
		if rv.Name == recordRoot {
			v = rv
		}
	}
	if v == nil {
		if nodeType == nil {
			return
		}
		v = &ir.Var{Name: recordRoot, Type: nodeType, Synthesized: true}
		root.Vars = append(root.Vars, v)
	}
	ref := func() ir.Expr {
		return &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, IsElementRef: true, Synthesized: true}
	}
	stmts, ok := p.intoContents(root.Name, root.Stmts, tops, ref)
	if !ok {
		return
	}
	root.Stmts = stmts
	attach(ref())
}

// readsRecord reports whether e reads one of a record's vars.
func readsRecord(e ir.Expr, vars map[*ir.Var]bool) bool {
	found := false
	_ = ir.Walk(e, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && vars[v] {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}
