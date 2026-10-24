package lower

import (
	"fmt"
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passNoInlineComponents inlines every non-recursive, non-native user-defined
// component other than a harness root into the body that instantiates it.
// After the pass, codegen sees only the harness root, the components built at
// run time, and any recursive cycles. See
// docs/superpowers/specs/2026-05-16-component-inlining-design.md.
var passNoInlineComponents = pass{
	name:    "NoInlineComponents",
	enabled: func(c Features) bool { return !c.InlineComponents },
	apply:   lowerInlineComponents,
}

func lowerInlineComponents(pkg *ir.Package, feats Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	// main is nil for every ordinary build: the visual tree lives in the
	// package's own body. It is a component only where a harness made one the
	// whole program.
	main := rootComponent(pkg, opts)
	if main == nil && len(pkg.Body) == 0 {
		// No root to inline into. Every component is its own entry point, so
		// there is nothing to flatten and nothing is unreachable -- running
		// the pass anyway would retain an empty keep set and drop them all.
		return nil
	}
	cycles := findRecursiveCycles(pkg, opts)
	reactive := collectReactiveVars(pkg)
	onList := make(map[*ir.Component]bool, len(pkg.Components))
	for _, c := range pkg.Components {
		onList[c] = true
	}
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles, reactive: reactive, platform: opts.Platform, instanceState: feats.InstanceState, nativeEffects: feats.Effects, genericComponents: feats.GenericComponents, onList: onList, names: namesOrOwn(opts.names), demoted: map[*ir.Func]bool{}, cells: map[*ir.Var][]*ir.Var{}, compInsts: map[*ir.Component][]componentInstance{}, methods: map[*ir.Var]map[*ir.Func][]*ir.Func{}}
	cellOwners := implicitCellOwners(pkg)
	st.reach = testReachRoots(pkg, main, opts)
	st.reach.instanceState = feats.InstanceState
	if err := st.run(); err != nil {
		return err
	}
	if err := repointCellReads(pkg, cellOwners, st.cells); err != nil {
		return err
	}
	if err := repointHandleCalls(pkg, st.methods); err != nil {
		return err
	}
	clearDemotedReceivers(pkg, st.demoted)
	for c := range unreachedCycles(pkg, main, cycles) {
		delete(st.keep, c)
	}
	dropNestedMethods(pkg, st.keep)
	pkg.Components = retainComponents(pkg.Components, st.keep)
	if err := refuseUnsplicedHandleCalls(pkg); err != nil {
		return err
	}
	for _, c := range pkg.Components {
		if c != main {
			contextProps(c)
		}
	}
	if err := uniqueNodeIDs(pkg); err != nil {
		return err
	}
	var spliced []splice
	if opts.splices != nil {
		spliced = *opts.splices
	}
	return repointTestReaches(pkg, st.reach, append(spliced, st.insts...))
}

// unreachedCycles is every recursive component nothing the program renders
// instantiates. A cycle is kept up front so its body survives the walk, but
// one nothing reaches was never elected a runtime instance, and its body still
// holds the insertions no backend renders.
func unreachedCycles(pkg *ir.Package, main *ir.Component, cycles map[*ir.Component]bool) map[*ir.Component]bool {
	if len(cycles) == 0 {
		return nil
	}
	owned := map[*ir.Component]ir.Owner{}
	var roots []ir.Owner
	for _, o := range ir.Owners(pkg) {
		switch {
		case o.Comp == nil:
			roots = append(roots, o)
		case o.Comp == main:
			roots = append(roots, o)
			owned[o.Comp] = o
		default:
			owned[o.Comp] = o
		}
	}
	reached := map[*ir.Component]bool{}
	var queue []ir.Owner
	visit := func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil && !reached[inst.Component] {
				reached[inst.Component] = true
				if o, ok := owned[inst.Component]; ok {
					queue = append(queue, o)
				}
			}
			return nil
		})
	}
	walkOwner := func(o ir.Owner) {
		visit(o.Stmts())
		for _, f := range o.Funcs {
			if f != nil {
				visit(f.Block)
			}
		}
	}
	for _, o := range roots {
		walkOwner(o)
	}
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		walkOwner(o)
	}
	out := map[*ir.Component]bool{}
	for c := range cycles {
		if c != main && !reached[c] {
			out[c] = true
		}
	}
	return out
}

// dropNestedMethods removes from pkg.Funcs the methods of every component the
// inliner is about to drop.
//
// The checker registers a nested method in both pkg.Funcs and its component's
// Funcs -- the same *ir.Func in each, which is why this matches by pointer.
// Dropping the declaration and leaving the members behind left every Go
// target emitting the original method of a component whose props no longer
// exist anywhere: each instance got its own clone with the arguments folded
// in, hoisted onto the owner it was inlined into, while the original still
// read the bare prop names and named nothing the Model declares.
//
// A component something instantiates at run time is kept, so its methods stay
// and the instance record emits them.
func dropNestedMethods(pkg *ir.Package, keep map[*ir.Component]bool) {
	dropped := make(map[*ir.Func]bool)
	for _, c := range pkg.Components {
		if keep[c] {
			continue
		}
		for _, f := range c.Funcs {
			dropped[f] = true
		}
	}
	if len(dropped) == 0 {
		return
	}
	out := pkg.Funcs[:0]
	for _, f := range pkg.Funcs {
		if !dropped[f] {
			out = append(out, f)
		}
	}
	pkg.Funcs = out
}

// uniqueNodeIDs makes each id name one node again.
//
// An id is the program's name for a node, and splicing a body puts a copy of
// every id in it into the caller -- so two instances of one component named one
// node twice. The callee's state is renamed per instance for exactly this
// reason; its ids were not, and a backend resolving the name answered for
// whichever node it reached first: two instances of a counter shared one
// element, and bumping either updated the same one.
//
// The first occurrence keeps the name. A program addresses a node by the name
// it wrote, and with a single instance -- overwhelmingly the common case --
// that name is unambiguous and must survive; a test naming `#inc` means the
// `#inc` there is. With several instances the program has no way to say which
// it meant, so the later ones are the ones that give up the name.
//
// Renaming here rather than while splicing, because "is this id ambiguous" is a
// question about the finished owner and not about any one instance: the splice
// that introduces a duplicate cannot tell it is doing so.
func uniqueNodeIDs(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	owners := ir.Owners(pkg)
	// The package's own names are held against every owner, not just against
	// the package. passHoistState moves a single-window program's state onto
	// the package while the nodes stay on the window, so the var and the id
	// that collide are reached under two different owners -- and html emits
	// both into one page regardless.
	shared := ownerNames(packageOwner(owners))
	for _, o := range owners {
		reads := handleIdents(o.Stmts())
		// The owner's own names are taken before any id is. A node id shares
		// one emitted namespace with the vars, consts and funcs the owner
		// declares -- html spells a state var `state.label` and a node handle
		// by its bare id, so `#label` beside `var label` came out as
		// `state.label.textContent = …`, writing through the var's cell as if
		// it were the element. The id is the one of the two that can move: a
		// var's name is read by everything the program wrote, while a handle
		// is reached through its binding.
		seen := ownerNames(o)
		for name := range shared {
			seen[name] = 1
		}
		rendered := map[*ir.Var]*ir.NodeInst{}
		var dup error
		// Every body the owner has, because an id is ambiguous wherever the
		// two nodes that share it are written.
		_ = ir.Walk(o.Stmts(), func(node ir.Node) error {
			n, ok := node.(*ir.NodeInst)
			if !ok || n.ID == "" {
				return nil
			}
			// The rename is keyed by *name*, because what it repairs is the
			// emitted namespace: html addresses a node by its id, and two
			// unrelated components each writing `#bar` collide there whether or
			// not they are related.
			k := seen[n.ID]
			seen[n.ID] = k + 1
			if k > 0 {
				renameNodeID(n, n.ID+"__"+strconv.Itoa(k), reads)
			}
			// The refusal is keyed by *symbol*, which is a narrower question
			// and a different one. A read is an ident bound to one var, and
			// declareNodeIDs runs per body: two unrelated `#bar`s are two vars
			// and each read says which it meant, while two spliced copies of
			// one body share theirs and neither read can. Only the second is
			// ambiguous -- keyed by name instead, an innocent `quiet()`
			// rendered twice was refused for a `#bar` that `reader()` read.
			if n.Handle == nil {
				return nil
			}
			if first, ok := rendered[n.Handle]; ok {
				if r := reads[n.Handle]; len(r) > 0 && dup == nil {
					dup = fmt.Errorf("%s: `#%s` is read here, and %s renders more than one of it -- the read cannot say which; give each copy its own id, or pass the value it is read for as a prop", identPos(r[0]), n.Handle.WrittenName(), nodePos(first))
				}
				return nil
			}
			rendered[n.Handle] = n
			return nil
		})
		if dup != nil {
			return dup
		}
	}
	return nil
}

// packageOwner is the owner that is not a component, which is the package
// itself. ir.Owners always yields one.
func packageOwner(owners []ir.Owner) ir.Owner {
	for _, o := range owners {
		if o.Comp == nil {
			return o
		}
	}
	return ir.Owner{}
}

// ownerNames is the names an owner's own declarations already hold in the
// emitted namespace, as the seen-count a node id is measured against: at one
// each, so the first id to want one is already a duplicate and moves.
func ownerNames(o ir.Owner) map[string]int {
	out := map[string]int{}
	for _, v := range o.Vars {
		out[v.Name] = 1
	}
	for _, v := range o.Consts {
		out[v.Name] = 1
	}
	for _, f := range o.Funcs {
		out[f.Name] = 1
	}
	return out
}

// renameNodeID moves a node id and everything that spells it: the handle the
// checker bound, and every read of that handle. An emitter spells a read from
// the ident's own Name, so renaming the node alone left `tag__1` in the tree
// and `tag` in the code that reads it -- one name for the element and another
// for the reference to it, on every target.
func renameNodeID(n *ir.NodeInst, name string, reads map[*ir.Var][]*ir.Ident) {
	n.ID = name
	if n.Handle == nil {
		return
	}
	if n.Handle.Written == "" {
		n.Handle.Written = n.Handle.Name
	}
	for _, id := range reads[n.Handle] {
		id.Name = name
	}
	n.Handle.Name = name
}

// writtenID is the id the program wrote for n.
func writtenID(n *ir.NodeInst) string {
	if n.Handle != nil && n.Handle.Written != "" {
		return n.Handle.Written
	}
	return n.ID
}

// handleIdents is every read of every `#id` handle, keyed by the binding and in
// the order the walk reached them. All of them, because a rename has to reach
// each one; in order, because the duplicate diagnostic points at the first, and
// a read may come after the copy that makes it ambiguous. Collecting them ahead
// of the walk that renames is what lets that walk be the only other one.
func handleIdents(stmts []ir.Stmt) map[*ir.Var][]*ir.Ident {
	out := map[*ir.Var][]*ir.Ident{}
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		id, ok := e.(*ir.Ident)
		if !ok {
			return nil
		}
		if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
			out[v] = append(out[v], id)
		}
		return nil
	})
	return out
}

// identPos is where an ident was written, or "<unknown>" for one a pass
// synthesized.
func identPos(id *ir.Ident) string {
	if id == nil || id.AST == nil {
		return "<unknown>"
	}
	if p := id.AST.ExprPos(); p != nil && p.IsValid() {
		return p.String()
	}
	return "<unknown>"
}

type inlineCompState struct {
	pkg  *ir.Package
	main *ir.Component
	// onList is pkg.Components as the pass found it: what the prune at the end
	// is allowed to keep.
	onList map[*ir.Component]bool
	// hoist is where an inlined callee's own declarations land. Set per
	// container as run() walks.
	hoist hoistTarget
	// captureOnly, while spliceNestedCaptures runs, is the set of nested
	// components that round may splice. Nil for the main walk.
	captureOnly map[*ir.Component]bool
	cycles      map[*ir.Component]bool
	keep        map[*ir.Component]bool
	reactive    map[*ir.Var]bool
	platform    string
	// instanceState is Features.InstanceState: whether a component built at run
	// time keeps its own state on this target.
	instanceState bool
	// genericComponents is Features.GenericComponents: whether the target
	// emits a generic component once for every instantiation. Where it does
	// not, compInsts is each generic component's copies (instantiateRuntime),
	// and taken the names a copy may not take.
	genericComponents bool
	compInsts         map[*ir.Component][]componentInstance
	taken             map[string]bool
	// names is passInlinePure's as well; see Options.names.
	names renamer
	// demoted is the clones dropReceiver made receiverless, so the call sites
	// renameIdents repointed at them can give their receiver up too.
	demoted map[*ir.Func]bool
	// cells is each implicit cell (ir.Var.Cell) and the copies splicing its
	// component made of it, for repointCellReads.
	cells map[*ir.Var][]*ir.Var
	// methods is, per node handle, each method of the component splicing that
	// node cloned and the clones, for repointHandleCalls.
	methods map[*ir.Var]map[*ir.Func][]*ir.Func
	// insts is what each spliced node carrying an id became, for a test
	// reaching into it (repointTestReaches).
	insts []splice
	reach *testReach
	// effect is sngl:builtin's `effect`, for restartOnEntry.
	effect *ir.Component
	// nativeEffects is Features.Effects: the platform brackets a lifetime
	// itself, and an Enter bracket runs only in the settle passEffect writes.
	nativeEffects bool
}

// Pointers rather than values because the append must be visible to the owner.
type hoistTarget struct {
	vars  *[]*ir.Var
	funcs *[]*ir.Func
	// method says a func hoisted here is still one: a component-body func is a
	// method and a package-level func is not, which is the pair of spellings
	// every backend already distinguishes. A callee's helper spliced into a
	// window or into the package body has to give up its receiver with the
	// declaration it was a method of, which this pass has just dropped --
	// left standing, route mode skipped it for having one and emitted
	// `s.Keep__inst0(…)` against a file that declared nothing of the name.
	method bool
}

func componentHoist(c *ir.Component) hoistTarget {
	return hoistTarget{vars: &c.Vars, funcs: &c.Funcs, method: true}
}

// packageHoist is the package's own declarations, where what a callee spliced
// into the package body declares goes.
func packageHoist(pkg *ir.Package) hoistTarget {
	return hoistTarget{vars: &pkg.Vars, funcs: &pkg.Funcs}
}

// dropReceiver makes fn an ordinary func rather than a method of a component
// that no longer exists. The body needs no rewriting: it reaches the state it
// reads through the renamed clones above and never through `this`.
//
// ir.Param.Receiver is the flag rather than the name, as the checker sets it
// on exactly the synthetic parameter it prepended.
func dropReceiver(fn *ir.Func) {
	fn.Receiver = nil
	fn.RecvParam = nil
	fn.Params = slices.DeleteFunc(slices.Clone(fn.Params), func(p *ir.Param) bool {
		return p != nil && p.Receiver
	})
}

func (st *inlineCompState) run() error {
	st.keep = map[*ir.Component]bool{}
	if st.main != nil {
		st.keep[st.main] = true
	}
	for c := range st.cycles {
		st.keep[c] = true
	}
	if err := st.spliceNestedCaptures(); err != nil {
		return err
	}
	for {
		ch, anyFuncCh := false, false
		if st.main != nil {
			st.hoist = componentHoist(st.main)
			body, mch, err := st.inlineStmts(st.main.Stmts)
			if err != nil {
				return err
			}
			st.main.Stmts = body
			ch = mch
			// Indexed: inlining a call inside a func body appends to this very
			// slice, and the appended clones need walking too.
			for i := 0; i < len(st.main.Funcs); i++ {
				fbody, fch, err := st.inlineStmts(st.main.Funcs[i].Block)
				if err != nil {
					return err
				}
				st.main.Funcs[i].Block = fbody
				anyFuncCh = anyFuncCh || fch
			}
		}
		// The package's own body, which is where a root-family component is
		// instantiated: `main()` at the top of a file is a NodeInst here and
		// nowhere else, and its windows reach a backend only once it has been
		// spliced in.
		st.hoist = packageHoist(st.pkg)
		pbody, pch, err := st.inlineStmts(st.pkg.Body)
		if err != nil {
			return err
		}
		st.pkg.Body = pbody
		// A component that survives is a root the backend reads its body from,
		// so what is inlinable *inside* that body has to be inlined too. Only
		// main and the package body were walked -- every root a *program* declares,
		// and none of the ones this pass elects. An imported component is one:
		// nothing inlines it, so it reached codegen holding the bodied callees
		// its body wrote, and `sngl:ui/markup`'s `list` written there emitted a
		// node no backend has heard of.
		//
		// A cycle is excluded, and that is the pre-existing behavior kept
		// rather than a case answered: a recursive component is on the keep
		// list whether or not anything renders it, so walking one elects a
		// runtime instance for a recursion the optimizer may already have
		// unrolled away -- a factory in the page for a declaration nothing
		// instantiates (`testdata/recursive_component_page_scope.txtar` denies
		// exactly that). What a bodied callee inside a recursive body does is
		// the same gap one construct over and is its own question.
		//
		// Indexed, because a splice here may elect another one.
		anyKeptCh := false
		for i := 0; i < len(st.pkg.Components); i++ {
			c := st.pkg.Components[i]
			if c == nil || c == st.main || !st.keep[c] || st.cycles[c] {
				continue
			}
			st.hoist = componentHoist(c)
			body, cch, err := st.inlineStmts(c.Stmts)
			if err != nil {
				return err
			}
			c.Stmts = body
			anyKeptCh = anyKeptCh || cch
			for j := 0; j < len(c.Funcs); j++ {
				fbody, fch, err := st.inlineStmts(c.Funcs[j].Block)
				if err != nil {
					return err
				}
				c.Funcs[j].Block = fbody
				anyKeptCh = anyKeptCh || fch
			}
		}
		if !ch && !anyFuncCh && !pch && !anyKeptCh {
			break
		}
	}
	if !st.instanceState {
		if err := st.refuseStateInCycles(); err != nil {
			return err
		}
		return st.refuseCanvasInCycles()
	}
	return nil
}

// refuseCanvasInCycles: a canvas's surface and kitty image are kept per copy
// of the loop the view walks, and that walk never enters a recursion.
func (st *inlineCompState) refuseCanvasInCycles() error {
	firstCanvas := func(stmts []ir.Stmt) *ir.NodeInst {
		var found *ir.NodeInst
		_ = ir.Walk(stmts, func(node ir.Node) error {
			if n, ok := node.(*ir.NodeInst); ok && ir.IsShapeContainer(n) {
				found = n
				return ir.SkipAll
			}
			return nil
		})
		return found
	}
	for _, c := range st.pkg.Components {
		if c == nil || !st.cycles[c] {
			continue
		}
		if n := firstCanvas(c.Stmts); n != nil {
			return fmt.Errorf("%s: a canvas in the recursive component %q is drawn once per level, and this target keeps one surface per canvas it can reach from the page -- draw it in what renders %q", nodePos(n), c.Name, c.Name)
		}
	}
	var err error
	for _, o := range ir.Owners(st.pkg) {
		_ = ir.Walk(o.Stmts(), func(node ir.Node) error {
			n, ok := node.(*ir.NodeInst)
			if !ok || err != nil || n.Component == nil || !st.cycles[n.Component] {
				return nil
			}
			for _, s := range ir.SuppliedContent(n) {
				if at := firstCanvas(s.Body); at != nil {
					err = fmt.Errorf("%s: a canvas handed to the recursive component %q is drawn once per level, and this target keeps one surface per canvas it can reach from the page -- draw it outside %q", nodePos(at), n.Component.Name, n.Component.Name)
					return ir.SkipAll
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// usedVar is the first of c's vars its body or funcs read or write. One nothing
// names is state nobody observes, and costs the target nothing.
func usedVar(c *ir.Component) *ir.Var {
	own := map[*ir.Var]bool{}
	for _, v := range c.Vars {
		own[v] = true
	}
	var found *ir.Var
	visit := func(nd ir.Node) error {
		if id, ok := nd.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && own[v] && found == nil {
				found = v
				return ir.SkipAll
			}
		}
		return nil
	}
	_ = ir.Walk(c.Stmts, visit)
	for _, fn := range c.Funcs {
		if found == nil && fn != nil {
			_ = ir.Walk(fn.Block, visit)
		}
	}
	return found
}

func varPos(v *ir.Var) string {
	if v.AST != nil {
		if p := v.AST.StmtPos(); p != nil && p.IsValid() {
			return p.String()
		}
	}
	return v.Name
}

// refuseStateInCycles refuses state or a lifetime inside a recursion on a
// target that keeps no state of an instance's own: such a target splices a
// stateful component where it is written, and a cycle is never spliced.
func (st *inlineCompState) refuseStateInCycles() error {
	for _, c := range st.pkg.Components {
		if c == nil || !st.cycles[c] {
			continue
		}
		if v := usedVar(c); v != nil {
			return fmt.Errorf("%s: the recursive component %q declares state of its own (%q); this target keeps a component's state only by splicing it where it is written, and a recursion is never spliced -- keep the state in what renders %q and pass it in as props", varPos(v), c.Name, v.Name, c.Name)
		}
		// A component that is not spliced renders as a function of the Model,
		// whose methods are the Model's and cannot read the props the render
		// function is handed.
		if fn := propReadingFunc(c); fn != nil {
			pos := compPos(c)
			if fn.AST != nil {
				pos = fn.AST.Pos.String()
			}
			return fmt.Errorf("%s: the recursive component %q declares %q, which reads its props; this target makes a component's functions the program's own by splicing it where it is written, and a recursion is never spliced -- pass what %q reads as its arguments, or compute it where %q is rendered", pos, c.Name, fn.Name, fn.Name, c.Name)
		}
		var found, lifetime *ir.NodeInst
		_ = ir.WalkStmts(c.Stmts, func(s ir.Stmt) error {
			n, ok := s.(*ir.NodeInst)
			if !ok || found != nil || n.Component == nil || st.cycles[n.Component] {
				return nil
			}
			if ir.IsTimerPrimitive(n.Component) || isEffectNode(n) {
				lifetime = n
				return ir.SkipAll
			}
			if len(n.Component.Vars) > 0 || holdsLifetime(n.Component.Stmts) {
				found = n
				return ir.SkipAll
			}
			return nil
		})
		if lifetime != nil {
			// A timer is the platform override's primitive by now, positioned in
			// that package; the declaration is the nearest place the program wrote.
			pos := ir.StmtPos(lifetime)
			if c.AST != nil && pos.File != c.AST.Pos.File {
				pos = c.AST.Pos
			}
			return fmt.Errorf("%s: the recursive component %q places a timer or effect in its own body; this target keeps a lifetime's handle only by splicing it where it is written, and a recursion is never spliced -- place it in what renders %q", pos, c.Name, c.Name)
		}
		if found != nil {
			return fmt.Errorf("%s: %q has state of its own and is written in the recursive component %q; this target keeps a component's state only by splicing it where it is written, and a recursion is never spliced -- keep the state in what renders %q and pass it in as props", nodePos(found), found.Component.Name, c.Name, c.Name)
		}
	}
	return nil
}

// propReadingFunc is a function c declares that reads one of c's props.
func propReadingFunc(c *ir.Component) *ir.Func {
	props := map[ir.Symbol]bool{}
	for _, p := range c.Props {
		if p != nil && p.Sym != nil {
			props[p.Sym] = true
		}
	}
	for _, fn := range c.Funcs {
		if fn == nil {
			continue
		}
		found := false
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if id, ok := n.(*ir.Ident); ok && id.Sym != nil && props[id.Sym] {
				found = true
				return ir.SkipAll
			}
			return nil
		})
		if found {
			return fn
		}
	}
	return nil
}

// rootComponent returns the component a harness has made the program's entry
// point, or nil -- which is every ordinary build, where a window is the root.
//
// A test build names the component under test, because that is what the
// harness renders: left to itself, the inliner would flatten it into the
// program's own root and rename its state per instance. CodegenCtx.RootDecl
// is the same answer for codegen.
func rootComponent(pkg *ir.Package, opts Options) *ir.Component {
	if opts.RootComponent == "" {
		return nil
	}
	for _, c := range pkg.Components {
		if c.Name == opts.RootComponent {
			return c
		}
	}
	return nil
}

// findRecursiveCycles returns the set of components participating in any
// call cycle (including self-recursion). Edges follow NodeInst.Component.
func findRecursiveCycles(pkg *ir.Package, opts Options) map[*ir.Component]bool {
	edges := map[*ir.Component]map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		edges[c] = map[*ir.Component]bool{}
		collectCalleeEdges(c, edges[c])
	}
	return tarjanCycles(edges)
}

// collectCalleeEdges records every component root instantiates, wherever in
// what root owns the instantiation is written.
//
// ir.Walk suits this exactly: it stops at NodeInst.Component, which is the
// edge being collected rather than a body to follow.
func collectCalleeEdges(root any, out map[*ir.Component]bool) {
	_ = ir.Walk(root, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil {
			out[inst.Component] = true
		}
		return nil
	})
}

// tarjanCycles runs Tarjan's SCC algorithm and returns the set of nodes
// in any non-trivial SCC (size > 1, or size 1 with a self-edge).
func tarjanCycles(edges map[*ir.Component]map[*ir.Component]bool) map[*ir.Component]bool {
	cycles := map[*ir.Component]bool{}
	idx := 0
	indices := map[*ir.Component]int{}
	lowlinks := map[*ir.Component]int{}
	onStack := map[*ir.Component]bool{}
	var stack []*ir.Component

	var strongconnect func(v *ir.Component)
	strongconnect = func(v *ir.Component) {
		indices[v] = idx
		lowlinks[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true
		for w := range edges[v] {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}
		if lowlinks[v] == indices[v] {
			var scc []*ir.Component
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				for _, n := range scc {
					cycles[n] = true
				}
			} else if edges[scc[0]][scc[0]] {
				cycles[scc[0]] = true
			}
		}
	}
	for v := range edges {
		if _, seen := indices[v]; !seen {
			strongconnect(v)
		}
	}
	return cycles
}

// retainComponents returns a new slice containing only components in keep,
// preserving relative order.
func retainComponents(in []*ir.Component, keep map[*ir.Component]bool) []*ir.Component {
	out := make([]*ir.Component, 0, len(in))
	for _, c := range in {
		if keep[c] {
			out = append(out, c)
		}
	}
	return out
}

// --- inliner helpers ---

// renameIdents rewrites Ident.Name via renames and repoints Ident.Sym via
// symRenames. Repointing Sym is essential: after inlining, the cloned
// vars/funcs live in main, and downstream passes (notably reactivity) collect
// those clones and match dependencies by Sym pointer. Leaving Sym at the
// original decl makes a reactive `if` referencing an inlined var resolve to a
// var no longer in scope, so the dependency is missed and the branch loses
// reactivity. Mutates exprs in place; caller passes a deep clone.
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) []ir.Stmt {
	if len(renames) == 0 && len(symRenames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		// A call names its callee on Call.Func, not through an Ident, so
		// repointing idents alone left `bump()` inside an inlined body calling
		// the original declaration -- whose body still reads the component's
		// own vars, which after inlining exist only under the instance's
		// names. The clone that was correct went uncalled and the caller got
		// the one that was not. Returned unchanged so the walk still descends
		// into the arguments.
		if call, ok := e.(*ir.Call); ok && call.Func != nil {
			if newSym, ok2 := symRenames[call.Func]; ok2 {
				if fn, ok3 := newSym.(*ir.Func); ok3 {
					call.Func = fn
				}
			}
			return e
		}
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		old := id.Sym
		if newName, ok2 := renames[old]; ok2 {
			id.Name = newName
		}
		if newSym, ok2 := symRenames[old]; ok2 {
			id.Sym = newSym
		}
		return e
	})
	return w.stmts(stmts)
}

func renameInExpr(e ir.Expr, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = renameIdents(tmp, renames, symRenames)
	return tmp[0].(*ir.LocalVar).Init
}

func substituteParamsExpr(e ir.Expr, bindings map[string]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = substituteParams(tmp, bindings)
	return tmp[0].(*ir.LocalVar).Init
}

func (st *inlineCompState) inlinable(comp *ir.Component) bool {
	if comp == nil || (st.main != nil && comp == st.main) {
		return false
	}
	if st.captureOnly != nil && !st.captureOnly[comp] {
		return false
	}
	if st.cycles[comp] {
		return false
	}
	// Platform primitives and stdlib wrappers with no body cannot be inlined.
	// Asked of what the declaration holds now, not of comp.Bodyless: that says
	// how it was written, and stays true once specializeComp has swapped an
	// override's body in -- every `ui.text` a target implements is bodyless
	// and has a body to splice by here.
	if len(comp.Stmts) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 {
		return false
	}
	return true
}

// rendersNothing reports whether comp puts nothing in the rendered tree.
//
// Conservative: anything it cannot account for renders something, so a body
// shape not thought of costs an instance election rather than a wrong inline.
// It does not ask what a nested node renders, because a stdlib component is
// still abstract here -- a descent reads `text` as rendering nothing.
func (st *inlineCompState) rendersNothing(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	renders := false
	_ = ir.Walk(comp.Stmts, func(node ir.Node) error {
		if renders {
			return nil
		}
		switch n := node.(type) {
		case *ir.NodeInst:
			if !isEffectNode(n) {
				renders = true
			}
		case *ir.SlotInst, *ir.ErrorBoundary:
			renders = true
		}
		return nil
	})
	return !renders
}

func (st *inlineCompState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, bool, error) {
	return st.inlineStmtsCtx(stmts, reactiveCtx{})
}

// reactiveCtx is what the statements below a control-flow construct are written
// inside: `in` is whether a node here needs reconciling, `repeated` whether the
// position holds one copy of the body or many. A `for` sets `repeated` whatever
// it iterates -- what makes one hoisted var wrong is the copies, and a loop over
// a const has as many as a loop over state.
type reactiveCtx struct {
	in       bool
	repeated bool
	// loops are the view loops enclosing the position, outermost first: what a
	// copy is a copy of, for a target that keys a copy's state by it.
	loops []*ir.For
	// drawn is whether the position is inside a canvas, whose shapes are
	// spliced into its draw routine: a shape built at run time has nowhere to
	// be built there, so one with state is spliced per copy on every target.
	drawn bool
}

// perCopy reports whether an instantiation of c here is built at run time, one
// per copy: every one in a reactive position, and under any `for` one whose
// state is its own -- its vars, or a lifetime it holds, whose handle and key
// one splice would share between the copies -- or a library component's it
// would splice, which nothing builds at run time in its place.
func (rc reactiveCtx) perCopy(c *ir.Component) bool {
	return rc.in || rc.repeated && (len(c.Vars) > 0 || holdsLifetime(c.Stmts) || splicesLibraryState(c.Stmts))
}

func (st *inlineCompState) inlineStmtsCtx(stmts []ir.Stmt, rc reactiveCtx) ([]ir.Stmt, bool, error) {
	changed := false
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		repl, ch, err := st.inlineStmtCtx(s, rc)
		if err != nil {
			return nil, false, err
		}
		changed = changed || ch
		out = append(out, repl...)
	}
	return out, changed, nil
}

func (st *inlineCompState) inlineStmtCtx(s ir.Stmt, rc reactiveCtx) ([]ir.Stmt, bool, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		childRC := rc
		// A page a target answers itself is mounted and unmounted as its
		// stack moves, which is what a reactive `if` does: what it renders is
		// built per showing, so a component with state there starts again.
		// Lowered by passNavigation, it already is an `if`.
		if n.Component != nil && n.Component.Builtin == ir.BuiltinNavPage {
			childRC = reactiveCtx{in: true, repeated: rc.repeated, loops: rc.loops, drawn: rc.drawn}
		} else if n.Record != nil {
			// A target's own primitive standing for a page, which it writes
			// as a document or a route of its own (html): a rendering root,
			// so a loop of pages is not a position its body is repeated in.
			childRC = reactiveCtx{}
		}
		childRC.drawn = childRC.drawn || ir.IsShapeContainer(n)
		ch, chCh, err := st.inlineStmtsCtx(n.Children, childRC)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		slotsCh, err := st.inlineSlotContents(n.Slots, childRC)
		if err != nil {
			return nil, false, err
		}
		chCh = chCh || slotsCh
		anyHandlerCh := false
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			hbody, hCh, err := st.inlineStmtsCtx(h.Func.Block, reactiveCtx{})
			if err != nil {
				return nil, false, err
			}
			h.Func.Block = hbody
			anyHandlerCh = anyHandlerCh || hCh
		}
		// Skip inlining when this NodeInst sits inside a reactive control-flow
		// construct, or when its target is part of a recursive cycle. Either
		// case will be lowered to a CreateComponent intrinsic call by later
		// passes (passReactivity / passDeclarative).
		//
		// Renders nothing is the exception: no node here to reconcile, and
		// passEffect already reads the enclosing `if` as the bracket's
		// position. Not under a `for` -- the body is spliced once and its state
		// hoisted once, so the copies would share one var.
		if rc.in && !rc.repeated && st.inlinable(n.Component) && st.rendersNothing(n.Component) {
			varStart := len(*st.hoist.vars)
			spliced, err := st.expandCall(n)
			if err != nil {
				return nil, false, err
			}
			if enter := st.restartOnEntry(slices.Clone((*st.hoist.vars)[varStart:]), nil); enter != nil {
				spliced = append([]ir.Stmt{enter}, spliced...)
			}
			return spliced, true, nil
		}
		if st.captureOnly != nil && st.captureOnly[n.Component] && rc.perCopy(n.Component) {
			where := "inside a reactive if or for"
			if !rc.in {
				where = "inside a for and declares state of its own"
			}
			return nil, false, fmt.Errorf("%s: %q reads the state of the body it is declared in and is instantiated %s, where each copy needs state of its own -- capture would hand every copy the one cell the owner holds; lift it to a top-level component and pass what it reads as props", nodePos(n), n.Component.Name, where)
		}
		// A target that keeps no state of an instance's own is given the state
		// where the component is written instead, one cell per copy. Its
		// functions too: built at run time, a component is a render function
		// of the Model there, and its functions the Model's, which cannot read
		// the props the render function is handed.
		if !st.instanceState && st.captureOnly == nil && n.Component != nil && (len(n.Component.Vars) > 0 || len(n.Component.Funcs) > 0 || holdsLifetime(n.Component.Stmts)) && !st.cycles[n.Component] && st.inlinable(n.Component) {
			return st.expandPerCopy(n, rc)
		}
		// Nor does its focus order reach a render function of the Model, so a
		// component rendering a focus stop is spliced too.
		if !st.instanceState && st.captureOnly == nil && n.Component != nil && rc.perCopy(n.Component) && !st.cycles[n.Component] && st.inlinable(n.Component) && rendersFocusStop(n.Component, map[*ir.Component]bool{}) {
			return st.expandPerCopy(n, rc)
		}
		// A library declaration with no state of its own is spliced even here:
		// nothing emits a library body as a component built at run time, and
		// with nothing to keep per copy there is no instance to build. It
		// reaches this position only when it could not be substituted earlier
		// -- `ui.window`, whose methods keep passInlinePure off it, composed
		// under a reactive `if` on bubbletea, where the implicit-state wrapper
		// around it was spliced per copy.
		if rc.perCopy(n.Component) && st.captureOnly == nil && n.Component != nil && n.Component.PkgPath() != "" && len(n.Component.Vars) == 0 && !holdsLifetime(n.Component.Stmts) && !st.cycles[n.Component] && st.inlinable(n.Component) {
			spliced, err := st.expandCall(n)
			if err != nil {
				return nil, false, err
			}
			return spliced, true, nil
		}
		// Left to the main walk: a capturing body cannot be the runtime
		// instance this branch elects, and the pre-pass already refused it.
		//
		// A component with state of its own under any `for` is one too, and
		// not only under a reactive one: spliced, its vars are hoisted once and
		// every copy writes the same cell -- a timer's handle overwritten by the
		// second mount, so the first schedule can never be stopped. That a
		// const iterable cannot change says how many copies there are, not
		// that they may share state.
		if rc.drawn && st.captureOnly == nil && n.Component != nil && rc.perCopy(n.Component) && !st.cycles[n.Component] && st.inlinable(n.Component) {
			return st.expandPerCopy(n, rc)
		}
		if st.captureOnly == nil && n.Component != nil && (rc.perCopy(n.Component) || st.cycles[n.Component]) {
			if err := st.instantiateRuntime(n); err != nil {
				return nil, false, err
			}
			st.keep[n.Component] = true
			// The one place that knows: this instantiation is built while the
			// program runs, so the declaration needs a runtime of its own.
			n.Component.RuntimeInstance = true
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		if !st.inlinable(n.Component) {
			// The node survives, so the declaration behind it does too: the
			// backend will read that body, and the passes after this one have
			// to have lowered it. Dropping it from pkg.Components here is what
			// left an imported component's canvas un-lowered. Only what the
			// list already carries -- a primitive was never on it, and putting
			// one there would have the package declaring `text` and `button`.
			if st.captureOnly == nil && n.Component != nil && st.onList[n.Component] {
				st.keep[n.Component] = true
			}
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		// Its functions are hoisted out of the loops around it, so a prop
		// bound to a loop variable reaches them as a parameter.
		if len(n.Component.Funcs) > 0 {
			return st.expandPerCopy(n, rc)
		}
		spliced, err := st.expandCall(n)
		if err != nil {
			return nil, false, err
		}
		return spliced, true, nil
	case *ir.If:
		inner := reactiveCtx{in: rc.in || dependsOnReactiveVar(n.Cond, st.reactive), repeated: rc.repeated, loops: rc.loops, drawn: rc.drawn}
		// A record's content, in its container's loop over the records, is
		// rendered by one iteration of it, so that loop keys none of its
		// copies -- of a stateful card under a `for` in a tab's panel, the
		// cells a test's `c.card[0].size` reads by the card's index alone
		// (cmd/sngl/testdata/tabs_panel_cells_runs.txt).
		withBody := inner
		withBody.loops = ir.BlockLoops(n, &n.Body, rc.loops)
		body, ch1, err := st.inlineStmtsCtx(n.Body, withBody)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, inner)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.Match:
		inner := reactiveCtx{in: rc.in || dependsOnReactiveVar(n.Subject, st.reactive), repeated: rc.repeated, loops: rc.loops, drawn: rc.drawn}
		for _, arm := range n.Arms {
			for _, p := range arm.Patterns {
				inner.in = inner.in || dependsOnReactiveVar(p, st.reactive)
			}
		}
		changed := false
		for _, arm := range n.Arms {
			body, ch, err := st.inlineStmtsCtx(arm.Body, inner)
			if err != nil {
				return nil, false, err
			}
			arm.Body = body
			changed = changed || ch
		}
		return []ir.Stmt{n}, changed, nil
	case *ir.For:
		inner := reactiveCtx{in: rc.in || dependsOnReactiveVar(n.Iter, st.reactive), repeated: true, loops: append(slices.Clip(rc.loops), n), drawn: rc.drawn}
		body, ch1, err := st.inlineStmtsCtx(n.Body, inner)
		if err != nil {
			return nil, false, err
		}
		// A schedule per copy is keyed by the copy, like a cell.
		if !st.instanceState && placesTimer(body) {
			loopIndex(n, len(rc.loops))
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, inner)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.SlotInst:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, rc)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		slotsCh, err := st.inlineSlotContents(n.Slots, rc)
		if err != nil {
			return nil, false, err
		}
		return []ir.Stmt{n}, chCh || slotsCh, nil
	case *ir.ErrorBoundary:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, rc)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		hCh := false
		if n.Handler != nil && n.Handler.Func != nil {
			body, b, err := st.inlineStmtsCtx(n.Handler.Func.Block, reactiveCtx{})
			if err != nil {
				return nil, false, err
			}
			n.Handler.Func.Block = body
			hCh = b
		}
		return []ir.Stmt{n}, chCh || hCh, nil
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
		*ir.Break, *ir.Continue:
		// Leaf/imperative stmts — no NodeInsts to inline.
		return []ir.Stmt{s}, false, nil
	default:
		panic(fmt.Sprintf("inlineCompState.inlineStmt: unhandled %T", n))
	}
}

func (st *inlineCompState) inlineSlotContents(slots map[string]*ir.SlotContent, rc reactiveCtx) (bool, error) {
	changed := false
	for _, name := range ir.SlotNames(slots) {
		sc := slots[name]
		if sc == nil {
			continue
		}
		body, ch, err := st.inlineStmtsCtx(sc.Body, rc)
		if err != nil {
			return false, err
		}
		sc.Body = body
		changed = changed || ch
	}
	return changed, nil
}

// walkExprIdents calls visit on every *ir.Ident reachable from e.
func walkExprIdents(e ir.Expr, visit func(*ir.Ident)) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Ident:
		visit(n)
	case *ir.Select:
		walkExprIdents(n.Operand, visit)
	case *ir.Index:
		walkExprIdents(n.Operand, visit)
		walkExprIdents(n.Idx, visit)
	case *ir.Binary:
		walkExprIdents(n.Left, visit)
		walkExprIdents(n.Right, visit)
	case *ir.Unary:
		walkExprIdents(n.Operand, visit)
	case *ir.Ternary:
		walkExprIdents(n.Cond, visit)
		walkExprIdents(n.Then, visit)
		walkExprIdents(n.Else, visit)
	case *ir.MatchExpr:
		walkExprIdents(n.Subject, visit)
		for _, arm := range n.Arms {
			for _, p := range arm.Patterns {
				walkExprIdents(p, visit)
			}
			walkExprIdents(arm.Value, visit)
		}
	case *ir.Conversion:
		walkExprIdents(n.Operand, visit)
	case *ir.Call:
		if n.Receiver != nil {
			walkExprIdents(n.Receiver, visit)
		}
		for _, a := range n.Args {
			walkExprIdents(a.Value, visit)
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			walkExprIdents(el, visit)
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			walkExprIdents(f.Value, visit)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			walkExprIdents(en.Key, visit)
			walkExprIdents(en.Value, visit)
		}
	case *ir.Spread:
		walkExprIdents(n.Operand, visit)
	}
}

// dependsOnReactiveVar reports whether an expression reads any var
// from the reactive set. Used to identify *ir.If/*ir.For whose body
// must be left as a runtime construct rather than statically inlined.
func dependsOnReactiveVar(e ir.Expr, reactive map[*ir.Var]bool) bool {
	if e == nil || len(reactive) == 0 {
		return false
	}
	found := false
	walkExprIdents(e, func(id *ir.Ident) {
		if v, ok := id.Sym.(*ir.Var); ok && reactive[v] {
			found = true
		}
	})
	return found
}

func (st *inlineCompState) expandCall(n *ir.NodeInst) ([]ir.Stmt, error) {
	comp := n.Component
	renames := map[ir.Symbol]string{}
	symRenames := map[ir.Symbol]ir.Symbol{}

	hoist := st.hoist
	varStart := len(*hoist.vars)
	provided := providedContextVars(comp, n.Props)
	typeArgs := spliceTypeArgs(comp, n)
	for _, v := range comp.Vars {
		if _, ok := provided[v]; ok {
			continue
		}
		clone := cloneHoistedVar(v, st.names.next(v.Name, "inst"), typeArgs)
		renames[v] = clone.Name
		symRenames[v] = clone
		if v.Cell && st.cells != nil {
			st.cells[v] = append(st.cells[v], clone)
		}
		// The clone is as reactive as the original. st.reactive was computed
		// once, before this pass created any of these, so a `for` iterating an
		// inlined component's own state read as non-reactive -- and the
		// instantiation inside it was inlined too, giving every element of the
		// loop one shared cell for what the component declared per instance.
		if st.reactive[v] {
			st.reactive[clone] = true
		}
		// And it holds what the original held: the points-to result is keyed
		// by declaration, so a clone is a slot nothing has analyzed.
		carryPointsTo(st.pkg, ir.SlotVarKey(v), ir.SlotVarKey(clone))
		*hoist.vars = append(*hoist.vars, clone)
	}
	funcStart := len(*hoist.funcs)
	for _, f := range comp.Funcs {
		clone := cloneFuncShallow(f)
		clone.Block = deepCloneStmts(f.Block)
		if typeArgs != nil {
			clone = ir.InstantiateFunc(f, typeArgs)
			clone.TypeParams, clone.RecvTypeParams = f.TypeParams, f.RecvTypeParams
		}
		clone.Name = st.names.next(f.Name, "inst")
		renames[f] = clone.Name
		symRenames[f] = clone
		if !hoist.method {
			dropReceiver(clone)
			st.demoted[clone] = true
		}
		carryPointsTo(st.pkg, ir.SlotReturnKey(f), ir.SlotReturnKey(clone))
		*hoist.funcs = append(*hoist.funcs, clone)
		if n.Handle != nil {
			if st.methods[n.Handle] == nil {
				st.methods[n.Handle] = map[*ir.Func][]*ir.Func{}
			}
			st.methods[n.Handle][f] = append(st.methods[n.Handle][f], clone)
		}
	}

	// Apply renames to every hoisted block.
	for i := varStart; i < len(*hoist.vars); i++ {
		if (*hoist.vars)[i].Init != nil {
			(*hoist.vars)[i].Init = substituteVarsExpr((*hoist.vars)[i].Init, provided)
			(*hoist.vars)[i].Init = renameInExpr((*hoist.vars)[i].Init, renames, symRenames)
		}
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		fn := (*hoist.funcs)[i]
		fn.Block = substituteVars(fn.Block, provided)
		fn.Block = renameIdents(fn.Block, renames, symRenames)
		fn.Reads = renameVarList(fn.Reads, symRenames)
		fn.Writes = renameVarList(fn.Writes, symRenames)
	}
	body := substituteVars(splicedBody(comp, typeArgs), provided)
	body = renameIdents(body, renames, symRenames)

	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		var val ir.Expr
		for _, arg := range n.Props {
			if arg.Name == p.Name {
				val = arg.Value
				break
			}
		}
		if val == nil {
			val = p.Default
		}
		if val == nil {
			// A stdlib prop with no declared default renders as its type's
			// zero value, which is what leaving one out means. Binding nothing
			// left the parameter's name standing in the inlined body, which
			// only ever mattered for a body that *reads* a prop the call site
			// omitted -- a platform override does, so `style` on a
			// `rect(x, y, w, h)` emitted an identifier nothing declares.
			val = ir.DeclaredDefault(p.Type)
		}
		if val != nil {
			bindings[p.Name] = val
		}
	}
	body = substituteParams(body, bindings)

	// Prop refs may appear in the hoisted callee-scope Vars/Funcs too.
	for i := varStart; i < len(*hoist.vars); i++ {
		(*hoist.vars)[i].Init = answerSlotEmptyIn((*hoist.vars)[i].Init, n)
		(*hoist.vars)[i].Init = substituteParamsExpr((*hoist.vars)[i].Init, bindings)
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		fn := (*hoist.funcs)[i]
		fn.Block = substituteParams(fn.Block, bindings)
		fn.Reads = withSubstitutedReads(fn.Reads, fn.Block, bindings)
	}
	// The caller's handlers land inside the body's own, so a parameter of the
	// body's sharing a name they read is renamed first (freshenShadowingParams).
	body = freshenShadowingParams(body, callsiteNames(n), st.names)
	// Events before slots: an emit in spliced slot content names the caller's
	// event, and would otherwise be matched against the callee's handlers.
	body = substituteEvents(body, n.Handlers)
	// A method fires the instance's events as the body does -- `open` writing
	// a bound `visible` is an emit of it by now -- so the call site's handlers
	// answer there too. Left as it was, the clone emitted an event nothing
	// declared a handler for.
	for i := funcStart; i < len(*hoist.funcs); i++ {
		(*hoist.funcs)[i].Block = substituteEvents((*hoist.funcs)[i].Block, n.Handlers)
	}
	body = substituteSlots(body, n)
	body = calleeReadsInContent(body, calleeProps(comp, bindings), renames, symRenames)
	// A library body -- a builtin node this target composes, and the
	// override it renders -- was written in the library, and a diagnostic
	// about what it became belongs where the program wrote the node.
	if comp.PkgPath() != "" {
		ir.AttachNodeSite(body, n)
	}
	instances := n.Instances
	if n.ID != "" {
		instances = append(slices.Clip(instances), n.ID)
	}
	if len(instances) > 0 {
		for _, s := range body {
			if root := ir.InstancesOf(s); root != nil {
				*root = append(slices.Clone(instances), *root...)
			}
		}
	}
	if n.ID != "" || st.reach.takes(comp) {
		st.recordInstance(n, symRenames, body)
	}

	return body, nil
}

// calleeReadsInContent is body, the callee's spliced around the call site's
// slot content, with the content's reads of the callee bound as the body's
// were. The content is the caller's, but the context lowering handed it the
// value of a provider the callee wraps the slot in as the callee wrote it
// (collectSlotEnv): `picked`, a var the splice renamed `picked__inst0`, or
// `level`, a prop it bound to the call site's argument. Both by symbol, so the
// caller's own names stand, a prop of the caller's named like the callee's
// included.
func calleeReadsInContent(body []ir.Stmt, props map[ir.Symbol]ir.Expr, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) []ir.Stmt {
	body = renameIdents(body, renames, symRenames)
	if len(props) == 0 {
		return body
	}
	return newExprWalker(func(e ir.Expr) ir.Expr {
		if id, ok := e.(*ir.Ident); ok && id.Sym != nil {
			if v, ok := props[id.Sym]; ok {
				return deepCloneExpr(v)
			}
		}
		return e
	}).stmts(body)
}

// calleeProps is what bindings binds each of comp's props to, keyed by the
// param the prop is read by.
func calleeProps(comp *ir.Component, bindings map[string]ir.Expr) map[ir.Symbol]ir.Expr {
	props := map[ir.Symbol]ir.Expr{}
	for _, p := range comp.Props {
		if v, ok := bindings[p.Name]; ok && p.Sym != nil {
			props[p.Sym] = v
		}
	}
	return props
}

// carryPointsTo copies one slot's candidates and color onto a second key, for
// a declaration a pass has cloned. A key with nothing recorded is left absent,
// so "no candidates" and "not a funcvar slot" stay distinguishable.
func carryPointsTo(pkg *ir.Package, from, to ir.PointsToKey) {
	if pkg == nil || pkg.PointsTo == nil {
		return
	}
	if cands, ok := pkg.PointsTo.Sites[from]; ok {
		pkg.PointsTo.Sites[to] = append([]*ir.Func{}, cands...)
	}
	if col, ok := pkg.PointsTo.SlotColor[from]; ok {
		pkg.PointsTo.SlotColor[to] = col
	}
}

func cloneVarShallow(v *ir.Var) *ir.Var {
	c := *v
	return &c
}

// spliceTypeArgs is what n's call site bound comp's type parameters to, where
// comp is the declaration n names; nil otherwise. A generic component's body
// is spliced at those types: what it declares names them, and left naming T,
// a backend that copies generics per instantiation erased it.
func spliceTypeArgs(comp *ir.Component, n *ir.NodeInst) map[string]*ir.Type {
	if n == nil || n.Component != comp {
		return nil
	}
	return ir.ComponentBindings(n)
}

// splicedBody is a fresh copy of comp's body at typeArgs.
func splicedBody(comp *ir.Component, typeArgs map[string]*ir.Type) []ir.Stmt {
	return deepCloneStmts(ir.InstantiateStmts(comp.Stmts, typeArgs))
}

// cloneHoistedVar is a copy of a component's var to hoist onto the owner
// splicing it, named name, at typeArgs.
func cloneHoistedVar(v *ir.Var, name string, typeArgs map[string]*ir.Type) *ir.Var {
	clone := cloneVarShallow(v)
	clone.Name = name
	clone.Init = deepCloneExpr(v.Init)
	if typeArgs != nil {
		clone.Type = v.Type.Substitute(typeArgs)
		if clone.Init != nil {
			clone.Init = ir.SubstituteExprTypes(clone.Init, typeArgs)
		}
	}
	return clone
}

// renameVarList is vs with each var its clone renames, in a list of its own:
// the clone shares the original's slice.
// withSubstitutedReads is reads plus the vars the props' values bring into
// block: a method reading its component's prop reads, once spliced, whatever
// the call site handed the prop -- the caller's `var chosen` behind
// `rest()`'s `chosen` -- and the reactivity pass learns what a call reads
// from Func.Reads, so a slot over `rest()` re-rendered on nothing the caller
// wrote (cmd/sngl/testdata/slot_method_reads_prop_runs.txt).
func withSubstitutedReads(reads []*ir.Var, block []ir.Stmt, bindings map[string]ir.Expr) []*ir.Var {
	brought := map[*ir.Var]bool{}
	for _, val := range bindings {
		_ = ir.WalkExprs(val, func(e ir.Expr) error {
			if id, ok := e.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok && !v.NodeHandle {
					brought[v] = true
				}
			}
			return nil
		})
	}
	if len(brought) == 0 {
		return reads
	}
	_ = ir.WalkExprs(block, func(e ir.Expr) error {
		id, ok := e.(*ir.Ident)
		if !ok {
			return nil
		}
		if v, ok := id.Sym.(*ir.Var); ok && brought[v] && !slices.Contains(reads, v) {
			reads = append(slices.Clip(reads), v)
		}
		return nil
	})
	return reads
}

func renameVarList(vs []*ir.Var, symRenames map[ir.Symbol]ir.Symbol) []*ir.Var {
	if len(vs) == 0 {
		return vs
	}
	out := make([]*ir.Var, len(vs))
	for i, v := range vs {
		out[i] = v
		if r, ok := symRenames[v].(*ir.Var); ok {
			out[i] = r
		}
	}
	return out
}

func cloneFuncShallow(f *ir.Func) *ir.Func {
	c := *f
	c.Block = nil
	return &c
}

// clearDemotedReceivers drops the receiver at every call site that names a func
// dropReceiver made receiverless.
//
// Once the whole pass has run, rather than as each call is spliced: a call
// reaches its callee through ir.Call.Func, which renameIdents repoints, so the
// site that needs fixing may be in a body inlined after the declaration was.
func clearDemotedReceivers(pkg *ir.Package, demoted map[*ir.Func]bool) {
	if len(demoted) == 0 {
		return
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if call, ok := n.(*ir.Call); ok && demoted[call.Func] {
			call.Receiver = nil
		}
		return nil
	})
}

// expandPerCopy splices a component with state of its own where it is written,
// and under a `for` gives each of its vars a cell per copy (perCopyCells).
func (st *inlineCompState) expandPerCopy(n *ir.NodeInst, rc reactiveCtx) ([]ir.Stmt, bool, error) {
	varStart, funcStart := len(*st.hoist.vars), len(*st.hoist.funcs)
	body, err := st.expandCall(n)
	if err != nil {
		return body, true, err
	}
	vars := slices.Clone((*st.hoist.vars)[varStart:])
	inits := map[*ir.Var]ir.Expr{}
	for _, v := range vars {
		inits[v] = v.Init
	}
	if rc.in {
		if enter := st.restartOnEntry(vars, rc.loops); enter != nil {
			body = append([]ir.Stmt{enter}, body...)
		}
	}
	if len(rc.loops) == 0 {
		return body, true, nil
	}
	funcs := slices.Clone((*st.hoist.funcs)[funcStart:])
	body, err = perCopyCells(nodePos(n), body, vars, funcs, rc.loops)
	if err == nil {
		st.recordCells(n, inits, rc.loops)
	}
	return body, true, err
}

// restartOnEntry is an Enter effect giving each of vars its initializer each
// time the instance's position is taken, where the instance stands: spliced,
// its vars are the owner's, and would keep what the last showing left them,
// where a target building the instance there starts it afresh. Each is
// declared from its type's zero instead, since the first entry initializes
// it, and an initializer run twice makes an owned value twice. Under loops
// the effect is keyed by the copy, which the cells already are. A platform
// bracketing lifetimes itself keeps it as a bracket of its own, ahead of the
// instance's, since it never runs the settle passEffect would lower it into.
func (st *inlineCompState) restartOnEntry(vars []*ir.Var, loops []*ir.For) *ir.NodeInst {
	var block []ir.Stmt
	for _, v := range vars {
		if v.IsConst || v.NodeHandle {
			continue
		}
		zero, init := ir.DeclaredDefault(v.Type), v.Init
		if init == nil {
			init = zero
		}
		if init == nil {
			continue
		}
		block = append(block, &ir.Assign{Target: &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}, Op: ast.AssignSet, Value: deepCloneExpr(init)})
		if zero != nil {
			v.Init = zero
		}
	}
	if len(block) == 0 {
		return nil
	}
	if st.effect == nil {
		st.effect = effectComponent()
	}
	n := &ir.NodeInst{Name: "effect", Component: st.effect, Enter: true, Handlers: []ir.EventHandler{
		{Name: ir.EffectMount, Func: &ir.Func{Pkg: st.pkg, Block: block}},
	}}
	if len(loops) > 0 {
		n.Props = []ir.Arg{{Name: ir.EffectOn, Value: CopyKey(loops)}}
	}
	return n
}

// rendersFocusStop reports whether c's body, or a component it renders, holds
// a focusable node.
func rendersFocusStop(c *ir.Component, seen map[*ir.Component]bool) bool {
	if seen[c] {
		return false
	}
	seen[c] = true
	if hasFocusableNodes(c.Stmts) {
		return true
	}
	found := false
	ir.WalkView(c.Stmts, func(s ir.Stmt) bool {
		if inst, ok := s.(*ir.NodeInst); ok && inst.Component != nil && !found && rendersFocusStop(inst.Component, seen) {
			found = true
		}
		return !found
	})
	return found
}
