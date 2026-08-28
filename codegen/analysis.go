package codegen

import (
	"fmt"
	"maps"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// CommonAnalysis holds platform-independent analysis extracted from a Package.
// Platforms embed this and add their own fields on top.
//
// Everything here is derived from the package and settled before codegen
// starts: it is what a generator reads, never where it writes. What a
// generator accumulates as it emits lives in Emission, which is a separate
// type for that reason — the two used to be one struct, and a dump of the
// analysis was a dump of the emitter's scratch space as well.
//
// The package itself is deliberately absent. The analysis is a set of facts
// about a program, not the program, and holding a live *ir.Package made it
// neither serializable nor meaningfully dumpable: `dump --stage analysis`
// walked the whole IR graph — sngl://std included — and hit its cycles. A
// caller wanting the package has it already; a caller wanting dependencies
// wants NewDepTrackerFromPkg.
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

// Emission is what a code generator accumulates while it emits: which helper
// functions its output turned out to need, and the CSS its components
// registered on the way past. Both are outputs of codegen rather than facts
// about the program, which is why they are not on CommonAnalysis.
//
// A platform that writes either embeds this beside the analysis, so the
// fields stay reachable under the names they always had.
type Emission struct {
	Helpers map[string]bool // helper functions the emitted code needs
	Styles  []string        // CSS rules registered by components during codegen
}

// NewEmission returns an empty accumulator.
func NewEmission() *Emission {
	return &Emission{Helpers: make(map[string]bool)}
}

// AddStyle registers a CSS rule to be emitted. Duplicate rules are ignored.
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
	Body       []ir.Stmt
	// LocalRefs is the non-escaping widget-ref set lower's passNodeEscape
	// recorded for this timer's handler scope. MutationModel platforms use
	// it to emit those refs as function-locals rather than Model fields.
	// nil when the pass did not run.
	LocalRefs map[string]bool
}

// AnalyzeOpts carries analysis the caller has already computed for this
// package and does not want repeated. A platform that analyzes the same
// package once per output file — html does, once per window — fills it from
// the first pass. Whatever it supplies is copied, so the analysis codegen then
// mutates is still the caller's alone.
type AnalyzeOpts struct {
	// UsedComponents is the result of the visual-tree scan. Nil means scan.
	UsedComponents map[string]bool
}

// AnalyzeCommon extracts CommonAnalysis from a Package. Platforms call this
// first, then add platform-specific analysis on top.
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
	if o.UsedComponents != nil {
		maps.Copy(a.UsedComponents, o.UsedComponents)
	} else {
		for _, comp := range pkg.Components {
			collectUsedIRStmts(comp.Body, a.UsedComponents)
		}
	}

	// Timers: package-level plus the main component's. MutationModel platforms
	// (fyne) read these from CommonAnalysis to emit their timer runtime; html
	// reads ir.Timer directly off the components during rendering, so this field
	// previously went unpopulated and fyne emitted no timer runtime at all.
	allTimers := append([]*ir.Timer{}, pkg.Timers...)
	for _, comp := range pkg.Components {
		if comp != nil && comp.Name == "main" {
			allTimers = append(allTimers, comp.Timers...)
		}
	}
	for i, t := range allTimers {
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
			Body:       t.Handler.Block,
			LocalRefs:  t.Handler.LocalRefs,
		})
	}

	// Alert usage is stamped onto the package by the StampUsage lowering pass.
	a.NeedsToast = pkg.UsesAlert

	return a
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
		case *ir.ContextProvider:
			collectUsedIRStmts(n.Children, used)
		case *ir.SlotInst:
			collectUsedIRStmts(n.Children, used)
		case *ir.Window:
			collectUsedIRStmts(n.Body, used)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
			// Pure statements have no named visual children.
		default:
			panic(fmt.Sprintf("collectUsedIRStmts: unhandled stmt %T", n))
		}
	}
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
