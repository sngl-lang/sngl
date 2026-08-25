package codegen

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// DepTracker tracks reactive dependencies between vars and computed funcs.
// All set fields are keyed on *ir.Var pointers (not names) so that
// same-named vars in different scopes don't collide.
type DepTracker struct {
	ModelVars     map[*ir.Var]struct{}
	ComputedFuncs map[*ir.Func]struct{}
	ComputedDeps  map[*ir.Func]map[*ir.Var]struct{}

	// Components is the list of all components in the package, used to
	// resolve `this.<field>` when walking inside a tracked context whose
	// owning component is known.
	Components []*ir.Component
}

// NewDepTracker creates a DepTracker. The caller's maps are retained; the
// tracker does not copy them.
func NewDepTracker(modelVars map[*ir.Var]struct{}, computedFuncs map[*ir.Func]struct{}, computedDeps map[*ir.Func]map[*ir.Var]struct{}) *DepTracker {
	return &DepTracker{
		ModelVars:     modelVars,
		ComputedFuncs: computedFuncs,
		ComputedDeps:  computedDeps,
	}
}

// NewDepTrackerFromPkg derives a DepTracker from an ir.Package. State vars
// come from Pkg.Vars and each component's Vars; computed funcs are zero-param
// pure funcs whose dependencies are taken from Func.Reads.
func NewDepTrackerFromPkg(pkg *ir.Package) *DepTracker {
	model := make(map[*ir.Var]struct{})
	computed := make(map[*ir.Func]struct{})
	computedDeps := make(map[*ir.Func]map[*ir.Var]struct{})

	for _, v := range pkg.Vars {
		model[v] = struct{}{}
	}
	for _, f := range pkg.Funcs {
		if IsComputed(f) {
			computed[f] = struct{}{}
			deps := make(map[*ir.Var]struct{})
			for _, r := range f.Reads {
				deps[r] = struct{}{}
			}
			computedDeps[f] = deps
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			model[v] = struct{}{}
		}
		for _, f := range comp.Funcs {
			if IsComputed(f) {
				computed[f] = struct{}{}
				deps := make(map[*ir.Var]struct{})
				for _, r := range f.Reads {
					deps[r] = struct{}{}
				}
				computedDeps[f] = deps
			}
		}
	}
	return &DepTracker{
		ModelVars:     model,
		ComputedFuncs: computed,
		ComputedDeps:  computedDeps,
		Components:    pkg.Components,
	}
}

// ExprDeps returns the set of model vars that a tracked expression reads,
// expanded transitively through computed funcs.
//
// currentComp is the component owning the tracked context (used to resolve
// implicit `this`); pass nil if the context isn't inside a component.
func (dt *DepTracker) ExprDeps(currentComp *ir.Component, expr ir.Expr) map[*ir.Var]struct{} {
	if expr == nil {
		return nil
	}
	w := newExtractor(dt, currentComp, true)
	w.walkExpr(expr)
	return dt.ExpandDeps(w.deps)
}

// ExpandDeps copies the input set and unions in the dependencies of every
// computed func whose own deps overlap. For now we don't iterate to a fixed
// point — the existing tracker didn't either.
func (dt *DepTracker) ExpandDeps(deps map[*ir.Var]struct{}) map[*ir.Var]struct{} {
	result := make(map[*ir.Var]struct{}, len(deps))
	for v := range deps {
		result[v] = struct{}{}
	}
	for fn, fnDeps := range dt.ComputedDeps {
		if _, isComputed := dt.ComputedFuncs[fn]; !isComputed {
			continue
		}
		for d := range fnDeps {
			if _, ok := deps[d]; ok {
				for x := range fnDeps {
					result[x] = struct{}{}
				}
				break
			}
		}
	}
	return result
}

// MutatedFields returns the set of model vars mutated by a statement
// (including writes that reach a var through called helpers).
func MutatedFields(currentComp *ir.Component, dt *DepTracker, s ir.Stmt) map[*ir.Var]struct{} {
	if s == nil || dt == nil {
		return nil
	}
	w := newExtractor(dt, currentComp, false)
	w.walkStmt(s)
	return w.mutated
}

// --- substituting walker ---

// depExtractor is the substituting dep walker. One instance per top-level
// entry into ExprDeps/MutatedFields; recurses into Call bodies with cloned
// frames that share deps/mutated/visited.
type depExtractor struct {
	tracker      *DepTracker
	bindings     map[string]ir.Expr
	visited      map[*ir.Func]struct{}
	deps         map[*ir.Var]struct{}
	mutated      map[*ir.Var]struct{}
	implicitThis *ir.Component
	tracking     bool

	// paramTypes captures the declared types of the params currently in
	// scope (matched with bindings). Used to decide whether writes through
	// a param propagate (component/ref/list/map: yes; value types: no).
	paramTypes map[string]*ir.Type
}

func newExtractor(dt *DepTracker, currentComp *ir.Component, tracking bool) *depExtractor {
	return &depExtractor{
		tracker:      dt,
		visited:      make(map[*ir.Func]struct{}),
		deps:         make(map[*ir.Var]struct{}),
		mutated:      make(map[*ir.Var]struct{}),
		implicitThis: currentComp,
		tracking:     tracking,
	}
}

// cloneFrame returns a sibling extractor for a recursive call. It shares
// deps/mutated/visited with the caller; bindings and paramTypes are reset
// (the caller fills them from the callee's params).
func (w *depExtractor) cloneFrame() *depExtractor {
	return &depExtractor{
		tracker:      w.tracker,
		visited:      w.visited,
		deps:         w.deps,
		mutated:      w.mutated,
		implicitThis: w.implicitThis,
		tracking:     w.tracking,
	}
}

// peelRoot walks left through Select/Index, returning the leftmost Ident
// and the field name immediately adjacent to it. For `this.p.x` returns
// (Ident{this}, "p") — the field rooted on the component is what matters;
// deeper field accesses are subsumed by whole-var deps on the root var.
func peelRoot(e ir.Expr) (root *ir.Ident, field string) {
	for {
		switch n := e.(type) {
		case *ir.Ident:
			return n, field
		case *ir.Select:
			field = n.Field
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		default:
			return nil, ""
		}
	}
}

// resolveVar returns the *ir.Var that an expression ultimately accesses,
// or nil if it doesn't bottom out in a var. Chases through param bindings
// and resolves implicit `this` against the owning component.
func (w *depExtractor) resolveVar(e ir.Expr) *ir.Var {
	return w.resolveVarGuarded(e, nil)
}

// resolveVarGuarded is the recursive worker. seen tracks names whose
// bindings are currently being expanded in this resolution chain; if a
// binding cycles back to a name we're already expanding, we stop and
// fall through to the non-binding branches so a self-referencing
// binding doesn't blow the stack.
func (w *depExtractor) resolveVarGuarded(e ir.Expr, seen map[string]struct{}) *ir.Var {
	root, field := peelRoot(e)
	if root == nil {
		return nil
	}
	if expr, bound := w.bindings[root.Name]; bound {
		if _, cycling := seen[root.Name]; !cycling {
			next := seen
			if next == nil {
				next = make(map[string]struct{}, 1)
			}
			next[root.Name] = struct{}{}
			if field == "" {
				return w.resolveVarGuarded(expr, next)
			}
			return w.resolveVarGuarded(&ir.Select{Operand: expr, Field: field}, next)
		}
		// Cycle: fall through to non-binding resolution on root itself.
	}
	if root.Name == ir.ReceiverParam && w.implicitThis != nil && field != "" {
		return lookupCompVar(w.implicitThis, field)
	}
	if v, ok := root.Sym.(*ir.Var); ok {
		return v
	}
	if comp, ok := root.Sym.(*ir.Component); ok && field != "" {
		return lookupCompVar(comp, field)
	}
	return nil
}

func lookupCompVar(c *ir.Component, name string) *ir.Var {
	if c == nil {
		return nil
	}
	for _, v := range c.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}

func (w *depExtractor) walkExpr(e ir.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Literal, *ir.ContextRead:
		// terminals
	case *ir.Ident:
		if w.tracking {
			if v := w.resolveVar(n); v != nil {
				if _, isModel := w.tracker.ModelVars[v]; isModel {
					w.deps[v] = struct{}{}
				}
			}
		}
	case *ir.Select:
		if w.tracking {
			if v := w.resolveVar(n); v != nil {
				if _, isModel := w.tracker.ModelVars[v]; isModel {
					w.deps[v] = struct{}{}
				}
				return
			}
		}
		w.walkExpr(n.Operand)
	case *ir.Index:
		if w.tracking {
			if v := w.resolveVar(n); v != nil {
				if _, isModel := w.tracker.ModelVars[v]; isModel {
					w.deps[v] = struct{}{}
				}
			}
		}
		w.walkExpr(n.Operand)
		w.walkExpr(n.Idx)
	case *ir.Binary:
		w.walkExpr(n.Left)
		w.walkExpr(n.Right)
	case *ir.Unary:
		w.walkExpr(n.Operand)
	case *ir.Ternary:
		w.walkExpr(n.Cond)
		w.walkExpr(n.Then)
		w.walkExpr(n.Else)
	case *ir.Conversion:
		w.walkExpr(n.Operand)
	case *ir.ListLit:
		for _, el := range n.Elems {
			w.walkExpr(el)
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			w.walkExpr(f.Value)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			w.walkExpr(en.Key)
			w.walkExpr(en.Value)
		}
	case *ir.Spread:
		w.walkExpr(n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			for _, s := range n.Func.Block {
				w.walkStmt(s)
			}
		}
	case *ir.Closure:
		if n.Func != nil {
			for _, s := range n.Func.Block {
				w.walkStmt(s)
			}
		}
		if n.State != nil {
			w.walkExpr(n.State)
		}
	case *ir.Call:
		w.walkCall(n)
	default:
		// Unhandled expr kinds are conservatively skipped.
	}
}

func (w *depExtractor) walkCall(c *ir.Call) {
	if c.Receiver != nil {
		w.walkExpr(c.Receiver)
	}
	for _, a := range c.Args {
		w.walkExpr(a.Value)
	}
	fn := c.Func
	if fn == nil || opaqueFunc(fn) {
		return
	}
	if _, seen := w.visited[fn]; seen {
		return
	}
	sub := w.cloneFrame()
	sub.bindings = make(map[string]ir.Expr, len(fn.Params))
	sub.paramTypes = make(map[string]*ir.Type, len(fn.Params))
	for i, p := range fn.Params {
		if i < len(c.Args) {
			sub.bindings[p.Name] = c.Args[i].Value
		}
		sub.paramTypes[p.Name] = p.Type
	}
	// If the callee is a component method (receiver is a component), set
	// implicitThis to that component so `this.x` references inside the body
	// resolve to the right component's vars.
	if fn.Receiver != "" {
		for _, comp := range w.tracker.Components {
			if comp.Name == fn.Receiver {
				sub.implicitThis = comp
				break
			}
		}
	}
	sub.visited[fn] = struct{}{}
	for _, s := range fn.Block {
		sub.walkStmt(s)
	}
}

func (w *depExtractor) walkStmt(s ir.Stmt) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ir.Assign:
		w.recordWrite(n.Target)
		w.walkExpr(n.Value)
	case *ir.Toggle:
		w.recordWrite(n.Target)
	case *ir.Return:
		w.walkExpr(n.Value)
	case *ir.LocalVar:
		w.walkExpr(n.Init)
	case *ir.If:
		w.walkExpr(n.Cond)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
		for _, c := range n.Else {
			w.walkStmt(c)
		}
	case *ir.For:
		w.walkExpr(n.Iter)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
		for _, c := range n.Else {
			w.walkStmt(c)
		}
	case *ir.Emit:
		for _, a := range n.Args {
			w.walkExpr(a.Value)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			w.walkExpr(n.Call)
		}
	case *ir.NodeInst:
		for _, p := range n.Props {
			w.walkExpr(p.Value)
		}
		for i := range n.Bindings {
			w.recordWrite(n.Bindings[i].Target)
		}
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	case *ir.SlotInst:
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			w.walkStmt(c)
		}
		if n.Handler != nil && n.Handler.Func != nil {
			for _, c := range n.Handler.Func.Block {
				w.walkStmt(c)
			}
		}
	case *ir.PlatformFilter:
		for _, c := range n.Body {
			w.walkStmt(c)
		}
	case *ir.Window:
		w.walkExpr(n.Href)
		w.walkExpr(n.Title)
		w.walkExpr(n.Favicon)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
	case *ir.ContextProvider:
		w.walkExpr(n.Value)
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	default:
		// Unhandled stmt kinds are conservatively skipped.
	}
}

// recordWrite records a mutation on the target's resolved var, gated on
// SNGL's value-vs-ref parameter semantics: writes through value-typed
// params don't propagate to the caller.
func (w *depExtractor) recordWrite(target ir.Expr) {
	if target == nil {
		return
	}
	root, _ := peelRoot(target)
	if root != nil {
		if _, bound := w.bindings[root.Name]; bound {
			if t := w.paramTypes[root.Name]; t != nil && !propagatesWrite(t) {
				return
			}
		}
	}
	v := w.resolveVar(target)
	if v == nil {
		return
	}
	if _, isModel := w.tracker.ModelVars[v]; isModel {
		w.mutated[v] = struct{}{}
	}
}

// propagatesWrite reports whether writes through a param of this type
// propagate to the caller. Component, ref<T>, list, and map are reference
// semantics; struct and primitives are value semantics.
func propagatesWrite(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeComponent, ir.TypeRef, ir.TypeList, ir.TypeMap:
		return true
	}
	return false
}

// opaqueFunc reports whether a function should not be recursed into.
// Stdlib intrinsics, native/scheme-imported funcs, and any func lacking
// an AST source are opaque.
func opaqueFunc(fn *ir.Func) bool {
	if fn == nil {
		return true
	}
	if fn.AST == nil {
		return true
	}
	if fn.Intrinsic != "" {
		return true
	}
	if fn.Foreign.Path != "" {
		return true
	}
	return false
}
