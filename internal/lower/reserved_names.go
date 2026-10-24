package lower

import (
	"duckfam.us/sngl/ir"
)

// passReservedNames renames a declaration the program gave a name the build
// reserves.
//
//	var range = 1           ->  var range__r0 = 1      (a Go keyword)
//	func f(m int) { … }     ->  func f(m__r0 int)      (the Go Model's receiver)
//	button #go(…)           ->  button #go__r0(…)
//
// What a build reserves is what its two build-tree nodes declare in a const
// prop -- a language's `reservedWords`, a platform's `reservedNames` -- or what
// the program's output block set them to: a language's words, and the
// identifiers a platform's own generated code introduces beside a program's -- a receiver, an import, a
// field or method of a type it writes, a local it declares. Each target used
// to escape a collision its own way at the point it wrote the name, and most
// sites did not: Go escaped nothing, and JavaScript one site in twenty.
// Renaming the declaration here is every site at once, since each backend
// spells a declaration from its Name.
//
// It runs last, once everything that synthesizes or copies a declaration has
// run, and renames through the declaration: every Ident bound to it, and each
// place a name is held as a string beside the symbol -- a LocalVar's and a
// loop's own Name, a node's ID and a component node's Name, a method's
// Receiver, a member selected off a component value, which is how a test body
// reads `c.range`. So a test, written against the program, reaches what it
// named under its new spelling.
//
// What keeps its name, whatever the build reserves: a struct field and an
// enum member, which a host reads by spelling (a route's path, a serialized
// record) and which every target writes somewhere a keyword is legal or
// escaped; a component's props, events and slots, which a call site binds by
// name; and a declaration that is a host's own -- foreign, native, intrinsic
// or builtin -- whose name is the host's to choose.
var passReservedNames = pass{
	name:    "ReservedNames",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, f Features, opts Options) error { return reserveNames(pkg, f, opts, false) },
}

// passReservedNodeIDs is the same rename for a node's `#id`, which cannot
// wait for the end: passDeclarative turns a node into a local named by its ID
// and the reads of its handle into references by that name, with no symbol
// for a late rename to follow. So an ID is renamed once the inliners have
// settled every node's -- uniqueNodeIDs is the last of
// passNoInlineComponents -- and every later pass copies the new one.
var passReservedNodeIDs = pass{
	name:    "ReservedNodeIDs",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, f Features, opts Options) error { return reserveNames(pkg, f, opts, true) },
}

func reserveNames(pkg *ir.Package, f Features, opts Options, nodeIDs bool) error {
	if pkg == nil {
		return nil
	}
	reserved := map[string]bool{}
	for _, n := range reservedFor(pkg, f, opts) {
		reserved[n] = true
	}
	r := &reservedRenamer{
		reserved: reserved,
		names:    namesOrOwn(opts.names),
		syms:     map[ir.Symbol]string{},
		params:   map[*ir.Param]string{},
		ids:      map[string]string{},
		comps:    map[*ir.Component]string{},
		members:  map[*ir.Component]map[string]string{},
		nodeIDs:  nodeIDs,
	}
	r.root = opts.RootComponent
	if nodeIDs {
		r.tests = testInstances(pkg, opts)
	}
	r.collect(pkg)
	if len(r.syms) == 0 && len(r.ids) == 0 && len(r.comps) == 0 {
		return nil
	}
	r.rewrite(pkg)
	return nil
}

// reservedFor is what this build reserves: the reservedWords and
// reservedNames its output block holds for the target being built -- what the
// program wrote, or the props' defaults -- and the defaults FeaturesFrom read
// where the program wrote no output block for it.
func reservedFor(pkg *ir.Package, f Features, opts Options) []string {
	for _, o := range pkg.Outputs {
		if o == nil || o.Lang != opts.Language || o.Platform != opts.Platform || o.Options == nil {
			continue
		}
		var out []string
		for _, name := range []string{ir.ReservedWordsProp, ir.ReservedNamesProp} {
			for _, fi := range o.Options.Fields {
				if fi.Name == name {
					ss, _ := ir.ConstStrings(fi.Value)
					out = append(out, ss...)
				}
			}
		}
		return out
	}
	return f.Reserved
}

type reservedRenamer struct {
	reserved map[string]bool
	names    renamer
	// syms is each renamed declaration and its new name; the declaration's own
	// Name is set as it is recorded.
	syms map[ir.Symbol]string
	// params is each renamed parameter's old name, which a call naming its
	// arguments still spells.
	params map[*ir.Param]string
	// ids is each renamed node ID. By name rather than by node: a spliced body
	// is a clone per copy, and every copy of one `#go` is the one handle.
	ids map[string]string
	// comps is each renamed component, for the node Name that spells it.
	comps map[*ir.Component]string
	// members is what a component value's select reaches under a new name:
	// its vars, its funcs and the IDs its body declares.
	members map[*ir.Component]map[string]string
	// nodeIDs is the early phase, which renames node IDs and nothing else;
	// the late phase renames everything but.
	nodeIDs bool
	// root is the component a test build is rooted at, which the harness
	// finds by its name.
	root string
	// tests is each test's instance. A test's select of a node an instance
	// the build spliced renders names the node by the id its splice gave it,
	// and the node then stands in the body of whatever rendered the instance,
	// so no member of the instance's component says it was renamed.
	tests map[*ir.Param]bool
}

func (r *reservedRenamer) fresh(old string) string { return r.names.next(old, "r") }

// sym renames a declaration whose name is reserved, once, and answers its
// new name, or "" when it keeps the one it has.
func (r *reservedRenamer) sym(s ir.Symbol, name string, set func(string)) string {
	if s == nil {
		return ""
	}
	if n, ok := r.syms[s]; ok {
		return n
	}
	if !r.reserved[name] {
		return ""
	}
	n := r.fresh(name)
	r.syms[s] = n
	set(n)
	return n
}

func (r *reservedRenamer) member(comp *ir.Component, old, n string) {
	if comp == nil || n == "" {
		return
	}
	m := r.members[comp]
	if m == nil {
		m = map[string]string{}
		r.members[comp] = m
	}
	m[old] = n
}

func (r *reservedRenamer) variable(v *ir.Var, owner *ir.Component) {
	if v == nil || v.Builtin != "" || v.Foreign.Name != "" {
		return
	}
	old := v.Name
	r.member(owner, old, r.sym(v, old, func(n string) { v.Name = n }))
}

// hostOwned reports whether fn's name and parameters are a host's: a native
// or foreign declaration spells the host's identifier, an intrinsic is a
// backend's dispatch key, and a native called with named arguments passes
// them by the host's parameter names.
func hostOwned(fn *ir.Func) bool {
	return fn.Intrinsic != "" || fn.Foreign.Name != "" || fn.Foreign.Path != ""
}

func (r *reservedRenamer) fn(fn *ir.Func, owner *ir.Component) {
	if fn == nil || hostOwned(fn) {
		return
	}
	// A lambda's and a handler's func is anonymous, and a test's name is what
	// the runner reports it under.
	if fn.Name != "" && !fn.IsTest {
		old := fn.Name
		n := r.sym(fn, old, func(n string) { fn.Name = n })
		if owner != nil && fn.RecvName() == owner.Name {
			r.member(owner, old, n)
		}
	}
	// A test's parameters are the test emitter's to spell -- the instance it
	// builds, the `Test` it is handed -- in a function of its own, beside
	// nothing a platform's code declares.
	if fn.IsTest {
		return
	}
	for _, p := range fn.Params {
		r.param(p)
	}
	if fn.RecvParam != nil {
		r.param(fn.RecvParam)
	}
}

func (r *reservedRenamer) param(p *ir.Param) {
	if p == nil {
		return
	}
	old := p.Name
	if _, seen := r.params[p]; !seen && r.sym(p, old, func(n string) { p.Name = n }) != "" {
		r.params[p] = old
	}
}

// typeDecl renames a struct, enum or unit, and the methods that spell their
// receiver by its name.
func (r *reservedRenamer) typeDecl(s ir.Symbol, name string, set func(string), methods map[string]*ir.Func) {
	// A method's receiver is the declaration, so it is renamed with it.
	r.sym(s, name, set)
}

// primitive reports whether comp is rendered by a backend from its
// declaration rather than composed, so its name is a dispatch key.
func primitive(comp *ir.Component) bool {
	return comp.Primitive() || comp.Builtin != "" || comp.Gen != nil || comp.IsFamily()
}

func (r *reservedRenamer) collect(pkg *ir.Package) {
	if r.nodeIDs {
		var owner *ir.Component
		visit := func(n ir.Node) error {
			if x, ok := n.(*ir.NodeInst); ok {
				r.node(x, owner)
			}
			return nil
		}
		_ = ir.Walk(pkg, visit)
		for _, comp := range pkg.Components {
			owner = comp
			_ = ir.Walk(comp, visit)
		}
		return
	}
	for _, v := range pkg.Consts {
		r.variable(v, nil)
	}
	for _, v := range pkg.Vars {
		r.variable(v, nil)
	}
	for _, fn := range pkg.Funcs {
		r.fn(fn, nil)
	}
	for _, sd := range pkg.Structs {
		if sd.Builtin != "" || sd.Foreign.Name != "" || sd.Anon {
			continue
		}
		r.typeDecl(sd, sd.Name, func(n string) { sd.Name = n }, sd.Methods)
	}
	for _, ed := range pkg.Enums {
		if ed.Foreign.Name != "" {
			continue
		}
		r.typeDecl(ed, ed.Name, func(n string) { ed.Name = n }, ed.Methods)
	}
	for _, ud := range pkg.Units {
		if ud.Builtin != "" {
			continue
		}
		r.typeDecl(ud, ud.Name, func(n string) { ud.Name = n }, ud.Methods)
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			r.variable(v, comp)
		}
		for _, fn := range comp.Funcs {
			r.fn(fn, comp)
		}
		if primitive(comp) || !r.reserved[comp.Name] || comp.Name == r.root {
			continue
		}
		old := comp.Name
		n := r.fresh(old)
		r.comps[comp] = n
		comp.Name = n
	}
	// What a body declares: locals, loop variables, and the parameters of
	// the funcs a body holds -- handlers, lambdas, a slot population's.
	visit := func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.LocalVar:
			if x.Sym != nil {
				r.sym(x.Sym, x.Sym.Name, func(s string) { x.Sym.Name = s })
			}
		case *ir.For:
			for _, lv := range []*ir.LoopVar{x.KeySym, x.ValueSym} {
				if lv != nil {
					r.sym(lv, lv.Name, func(s string) { lv.Name = s })
				}
			}
		case *ir.NodeInst:
			for i := range x.Handlers {
				r.fn(x.Handlers[i].Func, nil)
			}
			for _, sc := range x.Slots {
				if sc != nil {
					for _, p := range sc.Params {
						r.param(p)
					}
				}
			}
		case *ir.Lambda:
			r.fn(x.Func, nil)
		case *ir.Call:
			r.callArgNames(x)
			if x.ErrorHandler != nil {
				r.fn(x.ErrorHandler.Func, nil)
			}
		case *ir.ErrorBoundary:
			if x.Handler != nil {
				r.fn(x.Handler.Func, nil)
			}
		}
		return nil
	}
	for _, fn := range pkg.Funcs {
		_ = ir.Walk(fn, visit)
	}
	for _, v := range append(append([]*ir.Var{}, pkg.Consts...), pkg.Vars...) {
		_ = ir.Walk(v.Init, visit)
	}
	_ = ir.Walk(pkg.Body, visit)
	for _, comp := range pkg.Components {
		_ = ir.Walk(comp, visit)
	}
}

// node renames a node's reserved ID, and its handle with it.
func (r *reservedRenamer) node(n *ir.NodeInst, owner *ir.Component) {
	if n.ID == "" {
		return
	}
	nn, ok := r.ids[n.ID]
	if !ok {
		if !r.reserved[n.ID] {
			return
		}
		nn = r.fresh(n.ID)
		r.ids[n.ID] = nn
	}
	r.member(owner, n.ID, nn)
	if h := n.Handle; h != nil {
		if _, done := r.syms[h]; !done {
			r.syms[h] = nn
			if h.Written == "" {
				h.Written = h.Name
			}
			h.Name = nn
		}
	}
}

func (r *reservedRenamer) rewrite(pkg *ir.Package) {
	renameLocalRefs := func(refs map[string]bool) {
		for old, n := range r.ids {
			if v, ok := refs[old]; ok {
				delete(refs, old)
				refs[n] = v
			}
		}
	}
	_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.Ident:
			if nn, ok := r.syms[x.Sym]; ok && x.Sym != nil {
				x.Name = nn
			}
		case *ir.Call:
			if x.ErrorHandler != nil {
				r.funcStrings(x.ErrorHandler.Func, renameLocalRefs)
			}
		case *ir.Lambda:
			r.funcStrings(x.Func, renameLocalRefs)
		case *ir.Select:
			// `c.range` off a component value: the select holds the member's
			// name rather than its symbol.
			if t := x.Operand.ExprType(); t != nil && t.Kind == ir.TypeComponent {
				if comp, ok := t.Decl.(*ir.Component); ok {
					if nn, ok := r.members[comp][x.Field]; ok {
						x.Field = nn
					} else if nn, ok := r.ids[x.Field]; ok && r.testReach(x) && x.Ref != ir.RefNone {
						x.Field = nn
					}
				}
			}
		case *ir.LocalVar:
			if x.Sym != nil {
				x.Name = x.Sym.Name
			}
		case *ir.For:
			if x.KeySym != nil {
				x.Key = x.KeySym.Name
			}
			if x.ValueSym != nil {
				x.Value = x.ValueSym.Name
			}
		case *ir.NodeInst:
			if nn, ok := r.ids[x.ID]; ok {
				x.ID = nn
			}
			if nn, ok := r.comps[x.Component]; ok && x.Component != nil {
				x.Name = nn
			}
			for i := range x.Handlers {
				r.funcStrings(x.Handlers[i].Func, renameLocalRefs)
			}
		}
		return n, nil
	})
	for _, fn := range pkg.Funcs {
		r.funcStrings(fn, renameLocalRefs)
	}
	for _, comp := range pkg.Components {
		if comp.LocalRefs != nil {
			renameLocalRefs(comp.LocalRefs)
		}
		for _, fn := range comp.Funcs {
			r.funcStrings(fn, renameLocalRefs)
		}
	}
	for _, caps := range pkg.LiftedCaptures {
		for s, name := range caps {
			if nn, ok := r.syms[s]; ok && name != nn {
				caps[s] = nn
			}
		}
	}
}

// testReach reports whether sel selects off a test's own instance.
func (r *reservedRenamer) testReach(sel *ir.Select) bool {
	id, ok := unnarrowed(sel.Operand).(*ir.Ident)
	if !ok {
		return false
	}
	p, ok := id.Sym.(*ir.Param)
	return ok && r.tests[p]
}

// funcStrings updates what a func holds of a renamed name as a string.
func (r *reservedRenamer) funcStrings(fn *ir.Func, renameLocalRefs func(map[string]bool)) {
	if fn == nil {
		return
	}
	if fn.Lowered.LocalRefs != nil {
		renameLocalRefs(fn.Lowered.LocalRefs)
	}
}

func (r *reservedRenamer) callArgNames(call *ir.Call) {
	if call.Func == nil || len(r.params) == 0 {
		return
	}
	for i, a := range call.Args {
		if a.Name == "" {
			continue
		}
		for _, p := range call.Func.Params {
			if old, ok := r.params[p]; ok && old == a.Name {
				call.Args[i].Name = p.Name
			}
		}
	}
}
