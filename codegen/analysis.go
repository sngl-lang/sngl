package codegen

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// CommonAnalysis holds platform-independent analysis extracted from a Package.
// Platforms embed this and add their own fields on top.
type CommonAnalysis struct {
	ModelFields    map[string]bool            // data fields + computed fields
	ComputedFields map[string]bool            // subset of ModelFields that are computed
	ComputedDeps   map[string]map[string]bool // computed name → root field deps
	FuncNames      map[string]bool            // all user-defined function names
	ExternFuncs    map[string]bool            // extern func names
	ExternVars     map[string]bool            // extern var names
	StructFields   map[string][]string        // struct name → ordered field names
	Components     []*ir.Component
	Structs        []*ir.StructDef
	Enums          []*ir.EnumDef
	Units          []*ir.UnitDef
	Timers         []TimerInfo
	NeedsToast     bool
	Helpers        map[string]bool // needed helper functions (populated during codegen)
	UsedComponents map[string]bool // primitive component names used in the visual tree
	Styles         []string        // CSS rules registered by components during codegen
}

// TimerInfo captures platform-independent parts of a timer declaration.
type TimerInfo struct {
	Index      int
	IntervalMs int
	ActiveVar  string
	Body       []ir.Stmt
}

// AnalyzeCommon extracts CommonAnalysis from a Package. Platforms call this
// first, then add platform-specific analysis on top.
func AnalyzeCommon(pkg *ir.Package) *CommonAnalysis {
	a := &CommonAnalysis{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		ComputedDeps:   make(map[string]map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		StructFields:   make(map[string][]string),
		Helpers:        make(map[string]bool),
		UsedComponents: make(map[string]bool),
	}

	for _, v := range pkg.Vars {
		a.ModelFields[v.Name] = true
	}

	for _, f := range pkg.Funcs {
		a.FuncNames[f.Name] = true
		if f.Receiver != "" {
			a.FuncNames[f.Receiver+"."+f.Name] = true
		}
		if IsComputed(f) {
			a.ModelFields[f.Name] = true
			a.ComputedFields[f.Name] = true
			// Use purity analysis Reads for computed deps.
			deps := make(map[string]bool)
			for _, r := range f.Reads {
				deps[r.Name] = true
			}
			a.ComputedDeps[f.Name] = deps
		}
	}

	// Include main component's vars and funcs so platform codegen
	// sees them as model fields (mirrors DepTracker logic).
	for _, comp := range pkg.Components {
		if comp.Name == "main" {
			for _, v := range comp.Vars {
				a.ModelFields[v.Name] = true
			}
			for _, f := range comp.Funcs {
				a.FuncNames[f.Name] = true
				if f.Receiver != "" {
					a.FuncNames[f.Receiver+"."+f.Name] = true
				}
				if IsComputed(f) {
					a.ModelFields[f.Name] = true
					a.ComputedFields[f.Name] = true
					deps := make(map[string]bool)
					for _, r := range f.Reads {
						deps[r.Name] = true
					}
					a.ComputedDeps[f.Name] = deps
				}
			}
		}
	}

	for _, s := range pkg.Structs {
		fields := make([]string, len(s.Fields))
		for i, f := range s.Fields {
			fields[i] = f.Name
		}
		a.StructFields[s.Name] = fields
		a.Structs = append(a.Structs, s)
	}

	for _, e := range pkg.Enums {
		a.Enums = append(a.Enums, e)
	}

	for _, u := range pkg.Units {
		a.Units = append(a.Units, u)
	}

	for _, c := range pkg.Components {
		a.Components = append(a.Components, c)
	}

	for _, imp := range pkg.Imports {
		if imp.Native != nil {
			for _, f := range imp.Native.Funcs {
				a.ExternFuncs[f.Name] = true
			}
			for _, v := range imp.Native.Vars {
				a.ExternVars[v.Name] = true
			}
		}
	}

	// Walk visual tree to collect used primitive component names.
	for _, comp := range pkg.Components {
		collectUsedIRStmts(comp.Body, a.UsedComponents)
	}

	// Detect Alert usage.
	a.NeedsToast = usesAlert(pkg)

	return a
}

// PackageUsesErrorHandling reports whether the package contains any
// error-handling construct (window/boundary @error, per-call handler,
// raise or fallible call). Platforms use this to conditionally emit the
// ErrorEvent type into their output since stdlib types aren't otherwise
// materialised into user code.
func PackageUsesErrorHandling(pkg *ir.Package) bool {
	for _, w := range pkg.Windows {
		if w.ErrorHandler != nil {
			return true
		}
		if stmtsUseErrorHandling(w.Body) {
			return true
		}
	}
	for _, comp := range pkg.Components {
		if stmtsUseErrorHandling(comp.Body) {
			return true
		}
		for _, f := range comp.Funcs {
			if f.CanError || stmtsUseErrorHandling(f.Block) {
				return true
			}
		}
	}
	for _, f := range pkg.Funcs {
		if f.CanError || stmtsUseErrorHandling(f.Block) {
			return true
		}
	}
	return false
}

func stmtsUseErrorHandling(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.ErrorBoundary:
			return true
		case *ir.NodeInst:
			for i := range x.Handlers {
				if x.Handlers[i].CanError {
					return true
				}
				if x.Handlers[i].Func != nil && stmtsUseErrorHandling(x.Handlers[i].Func.Block) {
					return true
				}
			}
			if stmtsUseErrorHandling(x.Children) {
				return true
			}
		case *ir.CallStmt:
			if x.Call != nil && x.Call.ErrorMode != ir.ErrorNone {
				return true
			}
		case *ir.If:
			if stmtsUseErrorHandling(x.Body) || stmtsUseErrorHandling(x.Else) {
				return true
			}
		case *ir.For:
			if stmtsUseErrorHandling(x.Body) || stmtsUseErrorHandling(x.Else) {
				return true
			}
		case *ir.PlatformFilter:
			if stmtsUseErrorHandling(x.Body) {
				return true
			}
		case *ir.SlotInst:
			if stmtsUseErrorHandling(x.Children) {
				return true
			}
		}
	}
	return false
}

// collectUsedIRStmts walks IR statements collecting used visual node names.
func collectUsedIRStmts(stmts []ir.Stmt, used map[string]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Name != "" {
				used[n.Name] = true
			}
			collectUsedIRStmts(n.Children, used)
		case *ir.If:
			collectUsedIRStmts(n.Body, used)
			collectUsedIRStmts(n.Else, used)
		case *ir.For:
			collectUsedIRStmts(n.Body, used)
			collectUsedIRStmts(n.Else, used)
		case *ir.PlatformFilter:
			collectUsedIRStmts(n.Body, used)
		case *ir.ErrorBoundary:
			collectUsedIRStmts(n.Children, used)
		}
	}
}

// DepTracker returns a new DepTracker initialized from this analysis.
func (a *CommonAnalysis) DepTracker() *DepTracker {
	return NewDepTracker(a.ModelFields, a.ComputedFields, a.ComputedDeps)
}

// DerivedVarRecomputes returns, for each non-const Var in vars whose Init
// expression references other non-const Vars in vars, the topologically
// sorted list of Vars that must be recomputed (in order) after any one
// of its dependencies mutates.
//
// The result maps each *dependency* var (a mutable var that some derived
// var reads) to the ordered list of derived vars whose initializers must
// be re-evaluated, with each derived var's own deps preceding it. Var
// pointers that are not dependencies of any derived var are absent from
// the result.
//
// Cycles between derived vars are broken arbitrarily; an order-of-
// declaration fallback is used. The checker is responsible for rejecting
// genuine cycles before reaching this point.
func DerivedVarRecomputes(vars []*ir.Var) map[*ir.Var][]*ir.Var {
	// 1. For every derived var, compute its direct deps on other mutable
	//    vars (consts are never recomputed and are excluded as both
	//    sources and sinks).
	mutable := make(map[*ir.Var]bool, len(vars))
	for _, v := range vars {
		if !v.IsConst && !v.Synthesized {
			mutable[v] = true
		}
	}
	directDeps := make(map[*ir.Var]map[*ir.Var]bool)
	for _, v := range vars {
		if !mutable[v] || v.Init == nil {
			continue
		}
		deps := make(map[*ir.Var]bool)
		gatherVarReads(v.Init, mutable, deps)
		delete(deps, v) // self-reference is not a dep
		if len(deps) > 0 {
			directDeps[v] = deps
		}
	}
	if len(directDeps) == 0 {
		return nil
	}
	// 2. Topologically sort the derived vars in declaration order.
	order := make([]*ir.Var, 0, len(directDeps))
	for _, v := range vars {
		if _, ok := directDeps[v]; ok {
			order = append(order, v)
		}
	}
	// Stable, declaration-ordered topo sort: if `b` depends on `a` and
	// `a` is also derived, `a` must come before `b`. Since declaration
	// order in SNGL already requires this (forward refs not allowed in
	// var initializers between derived vars), declaration order is
	// already topological.
	// 3. Reverse-index: for each mutable var X, list every derived var
	//    that transitively depends on X, preserving topo order.
	out := make(map[*ir.Var][]*ir.Var)
	// Compute transitive deps per derived var.
	transitive := make(map[*ir.Var]map[*ir.Var]bool, len(directDeps))
	for _, v := range order {
		t := make(map[*ir.Var]bool)
		for dep := range directDeps[v] {
			t[dep] = true
			for tdep := range transitive[dep] {
				t[tdep] = true
			}
		}
		transitive[v] = t
	}
	for _, derived := range order {
		for dep := range transitive[derived] {
			out[dep] = append(out[dep], derived)
		}
	}
	// Sort each list by declaration order (already in `order`).
	pos := make(map[*ir.Var]int, len(order))
	for i, v := range order {
		pos[v] = i
	}
	for k, lst := range out {
		slices.SortFunc(lst, func(a, b *ir.Var) int { return pos[a] - pos[b] })
		out[k] = lst
	}
	return out
}

// gatherVarReads walks expr and records every *ir.Var read (via Ident.Sym)
// that is present in mutable.
func gatherVarReads(e ir.Expr, mutable map[*ir.Var]bool, out map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && mutable[v] {
			out[v] = true
		}
	case *ir.Binary:
		gatherVarReads(x.Left, mutable, out)
		gatherVarReads(x.Right, mutable, out)
	case *ir.Unary:
		gatherVarReads(x.Operand, mutable, out)
	case *ir.Ternary:
		gatherVarReads(x.Cond, mutable, out)
		gatherVarReads(x.Then, mutable, out)
		gatherVarReads(x.Else, mutable, out)
	case *ir.Call:
		if x.Receiver != nil {
			gatherVarReads(x.Receiver, mutable, out)
		}
		for _, a := range x.Args {
			gatherVarReads(a.Value, mutable, out)
		}
	case *ir.Conversion:
		gatherVarReads(x.Operand, mutable, out)
	case *ir.Select:
		gatherVarReads(x.Operand, mutable, out)
	case *ir.Index:
		gatherVarReads(x.Operand, mutable, out)
		gatherVarReads(x.Idx, mutable, out)
	case *ir.ListLit:
		for _, el := range x.Elems {
			gatherVarReads(el, mutable, out)
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			gatherVarReads(en.Key, mutable, out)
			gatherVarReads(en.Value, mutable, out)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			gatherVarReads(f.Value, mutable, out)
		}
	case *ir.Spread:
		gatherVarReads(x.Operand, mutable, out)
	}
}

// AddStyle registers a CSS rule to be emitted. Duplicate rules are ignored.
func (a *CommonAnalysis) AddStyle(css string) {
	if slices.Contains(a.Styles, css) {
		return
	}
	a.Styles = append(a.Styles, css)
}

// PruneUnusedComputeds removes computed fields that are not referenced by
// any of the given used field sets.
func (a *CommonAnalysis) PruneUnusedComputeds(usedFields map[string]bool) {
	needed := make(map[string]bool)
	var mark func(string)
	mark = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		for dep := range a.ComputedDeps[name] {
			if a.ComputedFields[dep] {
				mark(dep)
			}
		}
	}
	for name := range a.ComputedFields {
		if usedFields[name] {
			mark(name)
		}
	}
	for name := range a.ComputedFields {
		if !needed[name] {
			delete(a.ComputedFields, name)
			delete(a.ComputedDeps, name)
			delete(a.ModelFields, name)
		}
	}
}

// IntervalToMs converts an IR duration expression to milliseconds.
func IntervalToMs(expr ir.Expr) int {
	if expr == nil {
		return 0
	}
	lit, ok := expr.(*ir.Literal)
	if !ok {
		return 0
	}
	if lit.Suffix == "" {
		return int(parseNumber(lit.Raw))
	}
	num := parseNumber(lit.Raw)
	switch lit.Suffix {
	case "ms":
		return int(num)
	case "s":
		return int(num * 1000)
	case "m":
		return int(num * 60000)
	case "h":
		return int(num * 3600000)
	}
	return int(num)
}

func parseNumber(raw string) float64 {
	var n float64
	for _, c := range raw {
		if c >= '0' && c <= '9' {
			n = n*10 + float64(c-'0')
		} else if c == '.' {
			// Simple float parsing
			break
		} else {
			break
		}
	}
	return n
}

// usesAlert reports whether a package uses Alert.toast/info/warn/error.
func usesAlert(pkg *ir.Package) bool {
	for _, f := range pkg.Funcs {
		if irStmtsUseAlert(f.Block) {
			return true
		}
	}
	for _, c := range pkg.Components {
		if irStmtsUseAlert(c.Body) {
			return true
		}
		for _, f := range c.Funcs {
			if irStmtsUseAlert(f.Block) {
				return true
			}
		}
	}
	return false
}

func irStmtsUseAlert(stmts []ir.Stmt) bool {
	return slices.ContainsFunc(stmts, irStmtUsesAlert)
}

func irStmtUsesAlert(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, h := range n.Handlers {
			if irStmtsUseAlert(h.Func.Block) {
				return true
			}
		}
		if irStmtsUseAlert(n.Children) {
			return true
		}
	case *ir.CallStmt:
		if n.Call != nil && irExprUsesAlert(n.Call) {
			return true
		}
	case *ir.If:
		if irStmtsUseAlert(n.Body) || irStmtsUseAlert(n.Else) {
			return true
		}
	case *ir.For:
		if irStmtsUseAlert(n.Body) || irStmtsUseAlert(n.Else) {
			return true
		}
	case *ir.ErrorBoundary:
		if irStmtsUseAlert(n.Children) {
			return true
		}
		if n.Handler != nil && n.Handler.Func != nil && irStmtsUseAlert(n.Handler.Func.Block) {
			return true
		}
	}
	return false
}

func irExprUsesAlert(e ir.Expr) bool {
	call, ok := e.(*ir.Call)
	if !ok {
		return false
	}
	return call.Func != nil && call.Func.Receiver == "Alert"
}
