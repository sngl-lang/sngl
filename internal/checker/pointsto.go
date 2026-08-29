package checker

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// constraint is a single subset rule: pts(dst) ⊇ {funcs...} ∪ pts(srcs...).
type constraint struct {
	dst   ir.PointsToKey
	funcs []*ir.Func       // direct candidates
	srcs  []ir.PointsToKey // pts(src) ⊆ pts(dst) for each
}

// collectConstraints walks the IR and emits subset constraints capturing
// every funcvar flow site. The resulting list is consumed by the fixpoint
// solver.
func collectConstraints(pkg *ir.Package) []constraint {
	var out []constraint
	w := &pointsToWalker{out: &out}
	w.walkPackage(pkg)
	return out
}

type pointsToWalker struct {
	out *[]constraint
	fn  *ir.Func // enclosing function for SlotReturn keys
}

func (w *pointsToWalker) walkPackage(pkg *ir.Package) {
	for _, v := range pkg.Vars {
		w.walkVarInit(v)
	}
	for _, fn := range pkg.Funcs {
		w.walkFunc(fn)
	}
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			w.walkVarInit(v)
		}
		for _, fn := range c.Funcs {
			w.walkFunc(fn)
		}
		w.walkStmts(c.Body)
	}
	for _, win := range pkg.Windows {
		for _, v := range win.Vars {
			w.walkVarInit(v)
		}
		for _, fn := range win.Funcs {
			w.walkFunc(fn)
		}
		w.walkStmts(win.Body)
	}
}

func (w *pointsToWalker) walkVarInit(v *ir.Var) {
	if v.Init == nil {
		return
	}
	// If the var itself is funcvar-typed, bind the RHS.
	if isFuncType(v.Type) {
		w.bindRHS(ir.SlotVarKey(v), v.Init)
	}
	// Always descend to capture nested funcvar flows (e.g. list literals,
	// struct literals containing funcvar fields).
	w.walkExpr(v.Init)
}

func (w *pointsToWalker) walkFunc(fn *ir.Func) {
	prev := w.fn
	w.fn = fn
	w.walkStmts(fn.Block)
	w.fn = prev
}

func (w *pointsToWalker) walkStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.walkStmt(s)
	}
}

func (w *pointsToWalker) walkStmt(s ir.Stmt) {
	switch x := s.(type) {
	case *ir.LocalVar:
		if isFuncType(x.Type) && x.Init != nil {
			w.bindRHS(ir.SlotLocalKey(x), x.Init)
		}
		w.walkExpr(x.Init)
	case *ir.Assign:
		w.walkAssign(x)
	case *ir.Return:
		if w.fn != nil && isFuncType(w.fn.Return) && x.Value != nil {
			w.bindRHS(ir.SlotReturnKey(w.fn), x.Value)
		}
		w.walkExpr(x.Value)
	case *ir.If:
		w.walkExpr(x.Cond)
		w.walkStmts(x.Body)
		w.walkStmts(x.Else)
	case *ir.For:
		w.walkExpr(x.Iter)
		w.walkStmts(x.Body)
		w.walkStmts(x.Else)
	case *ir.CallStmt:
		w.walkExpr(x.Call)
	case *ir.NodeInst:
		w.walkStmts(x.Children)
		for i := range x.Handlers {
			if x.Handlers[i].Func != nil {
				w.walkFunc(x.Handlers[i].Func)
			}
		}
	case *ir.ErrorBoundary:
		w.walkStmts(x.Children)
		if x.Handler != nil && x.Handler.Func != nil {
			w.walkFunc(x.Handler.Func)
		}
	}
}

// walkAssign handles every assignment target shape that may bind a funcvar.
func (w *pointsToWalker) walkAssign(a *ir.Assign) {
	if a.Value == nil {
		w.walkExpr(a.Target)
		return
	}
	if !exprIsFuncTyped(a.Value) {
		w.walkExpr(a.Target)
		w.walkExpr(a.Value)
		return
	}
	dst, ok := slotKeyForAssignTarget(a.Target)
	if ok {
		w.bindRHS(dst, a.Value)
	}
	w.walkExpr(a.Target)
	w.walkExpr(a.Value)
}

func (w *pointsToWalker) walkExpr(e ir.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		w.walkCall(x)
	case *ir.Binary:
		w.walkExpr(x.Left)
		w.walkExpr(x.Right)
	case *ir.Unary:
		w.walkExpr(x.Operand)
	case *ir.Ternary:
		w.walkExpr(x.Cond)
		w.walkExpr(x.Then)
		w.walkExpr(x.Else)
	case *ir.Conversion:
		w.walkExpr(x.Operand)
	case *ir.Select:
		w.walkExpr(x.Operand)
	case *ir.Index:
		w.walkExpr(x.Operand)
		w.walkExpr(x.Idx)
	case *ir.ListLit:
		elemKey, ok := slotListElemKeyForListType(x.Type)
		for _, el := range x.Elems {
			if ok && exprIsFuncTyped(el) {
				w.bindRHS(elemKey, el)
			}
			w.walkExpr(el)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if !f.Spread && exprIsFuncTyped(f.Value) {
				if k, ok := structFieldKey(x.Type, f.Name); ok {
					w.bindRHS(k, f.Value)
				}
			}
			w.walkExpr(f.Value)
		}
	case *ir.Spread:
		w.walkExpr(x.Operand)
	case *ir.Lambda:
		// Lambda candidate: the binding is recorded at the enclosing
		// assignment/arg site via bindRHS, not here. Walk the lambda body
		// to capture any nested funcvar flows.
		if x.Func != nil {
			w.walkFunc(x.Func)
		}
	case *ir.Closure:
		// Same as Lambda.
		if x.Func != nil {
			w.walkFunc(x.Func)
		}
	}
}

// walkCall emits pts(param-slot) ⊇ candidates(arg) for each funcvar parameter.
func (w *pointsToWalker) walkCall(c *ir.Call) {
	if c.Func != nil {
		for i, a := range c.Args {
			if i >= len(c.Func.Params) {
				break
			}
			p := c.Func.Params[i]
			if !isFuncType(p.Type) {
				continue
			}
			if exprIsFuncTyped(a.Value) {
				w.bindRHS(ir.SlotParamKey(p), a.Value)
			}
		}
	}
	w.walkExpr(c.Receiver)
	for _, a := range c.Args {
		w.walkExpr(a.Value)
	}
}

// bindRHS emits a constraint binding pts(dst) ⊇ candidates(rhs).
func (w *pointsToWalker) bindRHS(dst ir.PointsToKey, rhs ir.Expr) {
	switch x := rhs.(type) {
	case *ir.Ident:
		switch sym := x.Sym.(type) {
		case *ir.Func:
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{sym}})
		case *ir.Var:
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotVarKey(sym)}})
		case *ir.Param:
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotParamKey(sym)}})
		}
	case *ir.Lambda:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{x.Func}})
		}
	case *ir.Closure:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{x.Func}})
		}
	case *ir.Call:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotReturnKey(x.Func)}})
		}
	case *ir.Select:
		// Namespace field select (e.g. api.fetchHello): resolve to the
		// concrete *ir.Func in the namespace's package and treat it as a
		// direct candidate.
		if x.Operand != nil {
			if ident, ok := x.Operand.(*ir.Ident); ok {
				if ns, ok := ident.Sym.(*ir.Namespace); ok && ns.Pkg != nil {
					for _, fn := range ns.Pkg.Funcs {
						if fn.Name == x.Field {
							*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{fn}})
							return
						}
					}
				}
			}
			// Struct field read: subset of the field slot.
			if k, ok := structFieldKey(exprType(x.Operand), x.Field); ok {
				*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{k}})
			}
		}
	case *ir.Index:
		// Reading a list element: subset of the elem slot.
		if x.Operand != nil {
			if k, ok := slotListElemKeyForListType(exprType(x.Operand)); ok {
				*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{k}})
			}
		}
	}
	// Other shapes: skip conservatively.
}

// --- Type helpers ---

func isFuncType(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeFunc
}

func exprIsFuncTyped(e ir.Expr) bool {
	return isFuncType(exprType(e))
}

// slotKeyForAssignTarget maps an assignment target expression to its PointsToKey.
func slotKeyForAssignTarget(t ir.Expr) (ir.PointsToKey, bool) {
	switch x := t.(type) {
	case *ir.Ident:
		switch sym := x.Sym.(type) {
		case *ir.Var:
			return ir.SlotVarKey(sym), true
		case *ir.Param:
			return ir.SlotParamKey(sym), true
		}
	case *ir.Select:
		if k, ok := structFieldKey(exprType(x.Operand), x.Field); ok {
			return k, true
		}
	case *ir.Index:
		if k, ok := slotListElemKeyForListType(exprType(x.Operand)); ok {
			return k, true
		}
	}
	return ir.PointsToKey{}, false
}

// structFieldKey returns the PointsToKey for a struct field access.
// Uses the *Type pointer of the struct type as the identity (field-insensitive
// across instances, per the Andersen model).
func structFieldKey(t *ir.Type, name string) (ir.PointsToKey, bool) {
	if t == nil || t.Kind != ir.TypeStruct {
		return ir.PointsToKey{}, false
	}
	return ir.SlotFieldKey(t, name), true
}

// slotListElemKeyForListType returns the PointsToKey for the element slot of a list type.
func slotListElemKeyForListType(t *ir.Type) (ir.PointsToKey, bool) {
	if t == nil || t.Kind != ir.TypeList {
		return ir.PointsToKey{}, false
	}
	return ir.SlotListElemKey(t), true
}

// analyzePointsTo runs the constraint walker, solves to fixpoint, and
// computes per-slot colors. Mutates pkg.PointsTo in place and returns it.
func analyzePointsTo(pkg *ir.Package) *ir.PointsToInfo {
	info := ir.NewPointsToInfo()
	pkg.PointsTo = info

	cs := collectConstraints(pkg)

	for {
		changed := false
		for _, c := range cs {
			for _, fn := range c.funcs {
				if info.AddCandidate(c.dst, fn) {
					changed = true
				}
			}
			for _, src := range c.srcs {
				for _, fn := range info.Candidates(src) {
					if info.AddCandidate(c.dst, fn) {
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	// Compute slot color for storage slots only.
	// SlotParam and SlotReturn are left uncolored; consumers fall back to
	// candidate inspection.
	for key, fns := range info.Sites {
		switch key.Kind {
		case ir.SlotVar, ir.SlotLocal, ir.SlotField, ir.SlotListElem:
			color := ir.ColorSync
			for _, fn := range fns {
				if fn.IsAsync {
					color = ir.ColorAsync
					break
				}
			}
			info.SlotColor[key] = color
		}
	}

	return info
}
