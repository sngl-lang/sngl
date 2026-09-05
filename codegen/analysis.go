package codegen

import (
	"fmt"
	"maps"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// CommonAnalysis holds platform-independent analysis extracted from a Package.
// Platforms embed this and add their own fields on top. It must hold no
// *ir.Package: `dump --stage analysis` serializes it, and the IR graph has
// cycles. Anything a generator accumulates while emitting belongs in Emission.
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
	UsedComponents map[string]bool // primitive component names used in the visual tree
}

// Emission is what a code generator accumulates while it emits, as opposed to
// the facts about the program that CommonAnalysis holds.
type Emission struct {
	Helpers map[string]bool // helper functions the emitted code needs
	Styles  []string        // CSS rules registered by components during codegen
}

func NewEmission() *Emission {
	return &Emission{Helpers: make(map[string]bool)}
}

// AddStyle ignores a rule that is already registered.
func (e *Emission) AddStyle(css string) {
	if slices.Contains(e.Styles, css) {
		return
	}
	e.Styles = append(e.Styles, css)
}

// TimerInfo captures platform-independent parts of a timer declaration.
type TimerInfo struct {
	Index      int
	IntervalMs int
	ActiveVar  string
	// Enabled is the gate as an expression, which ActiveVar can only carry
	// when it happens to be a bare state read. A backend that can emit an
	// expression should use this one: `enabled=true` reduced to an empty
	// ActiveVar, and android read that as "no gate" and wrote
	// `LaunchedEffect() { while () {`, which is not Kotlin.
	Enabled ir.Expr
	Body    []ir.Stmt
	// LocalRefs is passNodeEscape's non-escaping widget-ref set for this
	// handler scope, nil when that pass did not run.
	LocalRefs map[string]bool
}

// AnalyzeOpts carries analysis the caller has already computed for this
// package and does not want repeated. Whatever it supplies is copied, so the
// analysis codegen then mutates is still the caller's alone.
type AnalyzeOpts struct {
	// UsedComponents is the result of the visual-tree scan. Nil means scan.
	UsedComponents map[string]bool
}

// AnalyzeCommon extracts CommonAnalysis from a Package.
func AnalyzeCommon(pkg *ir.Package) *CommonAnalysis {
	return AnalyzeCommonFor(pkg, AnalyzeOpts{})
}

// AnalyzeCommonFor is AnalyzeCommon with the parts named in o taken as given.
func AnalyzeCommonFor(pkg *ir.Package, o AnalyzeOpts) *CommonAnalysis {
	a := &CommonAnalysis{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		ComputedDeps:   make(map[string]map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		StructFields:   make(map[string][]string),
		UsedComponents: make(map[string]bool),
	}

	// These sets are keyed by name, so only one component can contribute: a
	// child component's `count` is not the root model's `count`. The package
	// and the windows are unambiguous and always do.
	for _, o := range ir.Owners(pkg) {
		if o.Comp != nil && o.Comp.Name != rootComponentName {
			continue
		}
		for _, v := range o.Vars {
			a.ModelFields[v.Name] = true
		}
		// A window's funcs are synthesized and reached through
		// CodegenCtx.AllFuncs, not by name from an expression, so they do not
		// join FuncNames.
		if o.Win != nil {
			continue
		}
		for _, f := range o.Funcs {
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

	if o.UsedComponents != nil {
		maps.Copy(a.UsedComponents, o.UsedComponents)
	} else {
		for _, comp := range pkg.Components {
			collectUsedIRStmts(comp.Body, a.UsedComponents)
		}
	}

	// Every schedule the program describes, as passTimerPrimitive recorded it:
	// one entry per placed timer primitive, wherever in the tree it was
	// written, with its enclosing branch conditions folded into the gate.
	//
	// It used to be pkg.Timers plus `main`'s -- a list the checker hoisted every
	// timer onto -- so a timer in any other component was checked, type-correct
	// and never emitted at all.
	schedules := append([]*ir.Timer{}, pkg.Timers...)
	for _, comp := range pkg.Components {
		if comp != nil {
			schedules = append(schedules, comp.Timers...)
		}
	}
	for i, t := range schedules {
		if t == nil || t.Handler == nil {
			continue
		}
		activeVar := ""
		if id, ok := t.Enabled.(*ir.Ident); ok {
			activeVar = id.Name
		}
		a.Timers = append(a.Timers, TimerInfo{
			Index:      i,
			IntervalMs: IntervalToMs(t.Interval),
			ActiveVar:  activeVar,
			Enabled:    t.Enabled,
			Body:       t.Handler.Block,
			LocalRefs:  t.Handler.LocalRefs,
		})
	}

	a.NeedsToast = pkg.UsesAlert

	return a
}

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
		case *ir.ErrorBoundary:
			collectUsedIRStmts(n.Children, used)
		case *ir.ContextProvider:
			collectUsedIRStmts(n.Children, used)
		case *ir.SlotInst:
			collectUsedIRStmts(n.Children, used)
		case *ir.Window:
			collectUsedIRStmts(n.Body, used)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Pure statements have no named visual children.
		default:
			panic(fmt.Sprintf("collectUsedIRStmts: unhandled stmt %T", n))
		}
	}
}

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

func IntervalToMs(expr ir.Expr) int {
	if expr == nil {
		return 0
	}
	lit, ok := expr.(*ir.Literal)
	if !ok {
		return 0
	}
	if lit.Suffix == "" {
		return int(parseNumber(lit.Value))
	}
	num := parseNumber(lit.Value)
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
			break
		} else {
			break
		}
	}
	return n
}

// rootComponentName is the component a single-Model target builds from when
// nothing overrides it. CodegenCtx.MainComponent is the authority -- it also
// honours RootComponent -- but AnalyzeCommon runs from a package alone, before
// there is a CodegenCtx to ask.
const rootComponentName = "main"
