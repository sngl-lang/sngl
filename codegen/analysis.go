package codegen

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
)

// CommonAnalysis holds platform-independent analysis extracted from a Document.
// Platforms embed this and add their own fields on top.
type CommonAnalysis struct {
	ModelFields    map[string]bool            // data fields + computed fields
	ComputedFields map[string]bool            // subset of ModelFields that are computed
	ComputedDeps   map[string]map[string]bool // computed name → root field deps
	FuncNames      map[string]bool            // all user-defined function names
	ExternFuncs    map[string]bool            // extern func names
	ExternVars     map[string]bool            // extern var names
	StructFields   map[string][]string        // struct name → ordered field names
	Components     []*ast.Component
	Structs        []*ast.StructDef
	Enums          []*ast.EnumDef
	Timers         []TimerInfo
	NeedsToast     bool
	Helpers        map[string]bool // needed helper functions (populated during codegen)
}

// TimerInfo captures platform-independent parts of a timer declaration.
type TimerInfo struct {
	Index      int
	IntervalMs int
	ActiveVar  string
	Body       ast.Node
}

// AnalyzeCommon extracts CommonAnalysis from a Document. Platforms call this
// first, then add platform-specific analysis on top.
func AnalyzeCommon(doc *ast.Document) *CommonAnalysis {
	a := &CommonAnalysis{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		ComputedDeps:   make(map[string]map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		StructFields:   make(map[string][]string),
		Helpers:        make(map[string]bool),
	}

	// Data fields
	for _, d := range doc.Data {
		a.ModelFields[d.Name] = true
		if d.Extern || d.IsFunc {
			if d.IsFunc {
				a.ExternFuncs[d.Name] = true
			} else {
				a.ExternVars[d.Name] = true
			}
		}
	}

	// Computed functions (zero-arg expression-form, non-stdlib)
	for _, fn := range doc.Functions {
		a.FuncNames[fn.Name] = true
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			a.ModelFields[fn.Name] = true
			a.ComputedFields[fn.Name] = true
		}
	}

	// Computed dependency map
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			a.ComputedDeps[fn.Name] = ExtractDeps(fn.Body.SNGL, a.ModelFields)
		}
	}

	// Struct fields
	for _, sd := range doc.Structs {
		fields := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			fields[i] = f.Name
		}
		a.StructFields[sd.Name] = fields
	}

	// Timers
	for i, t := range doc.Timers {
		a.Timers = append(a.Timers, TimerInfo{
			Index:      i,
			IntervalMs: IntervalToMs(t.Interval),
			ActiveVar:  t.Active,
			Body:       t.Body,
		})
	}

	// Components, structs, enums
	a.Components = doc.AllComponents()
	a.Structs = doc.Structs
	a.Enums = doc.Enums

	// Toast detection
	a.NeedsToast = ASTUsesAlert(doc)

	return a
}

// DepTracker returns a new DepTracker initialized from this analysis.
func (a *CommonAnalysis) DepTracker() *DepTracker {
	return NewDepTracker(a.ModelFields, a.ComputedFields, a.ComputedDeps)
}

// Scope returns an ExprScope initialized from this analysis. Platforms
// typically clone and extend this with local variables and renames.
func (a *CommonAnalysis) Scope() *ExprScope {
	localVars := make(map[string]bool)
	return &ExprScope{
		ModelFields:    a.ModelFields,
		ComputedFields: a.ComputedFields,
		FuncNames:      a.FuncNames,
		ExternFuncs:    a.ExternFuncs,
		ExternVars:     a.ExternVars,
		LocalVars:      localVars,
		NeededHelpers:  a.Helpers,
	}
}

// PruneUnusedComputeds removes computed fields that are not referenced by
// any of the given used field sets. A computed is "used" if it appears in
// usedFields directly, or if another used computed transitively depends on it.
// This mutates the CommonAnalysis in place.
func (a *CommonAnalysis) PruneUnusedComputeds(usedFields map[string]bool) {
	// Build the set of transitively needed computeds.
	needed := make(map[string]bool)
	var mark func(string)
	mark = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		// If this computed depends on other computeds, mark them too.
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

	// Remove unused computeds.
	for name := range a.ComputedFields {
		if !needed[name] {
			delete(a.ComputedFields, name)
			delete(a.ComputedDeps, name)
			delete(a.ModelFields, name)
		}
	}
}

// IntervalToMs converts a duration expression (e.g., 500ms, 1s, 2m) to
// milliseconds. Returns 0 if the expression is not a recognized unit literal.
func IntervalToMs(expr ast.Expr) int {
	if expr.SNGL == nil {
		return 0
	}
	lit, ok := expr.SNGL.(*ast.LiteralExpr)
	if !ok || lit.Kind != ast.LiteralUnit {
		return 0
	}
	ul, ok := lit.Value.(ast.UnitLiteral)
	if !ok {
		return 0
	}
	var num float64
	fmt.Sscanf(ul.Number, "%f", &num)
	switch ul.Suffix {
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

// ASTUsesAlert reports whether a document uses Alert.toast/info/warn/error
// calls in event handlers, timers, or functions.
func ASTUsesAlert(doc *ast.Document) bool {
	if doc.App != nil {
		if slices.ContainsFunc(doc.App.Children, nodeUsesAlert) {
			return true
		}
	}
	for _, t := range doc.Timers {
		if exprNodeUsesAlert(t.Body) {
			return true
		}
	}
	for _, fn := range doc.Functions {
		if fn.IsTest() {
			continue
		}
		if fn.Block != nil {
			if slices.ContainsFunc(fn.Block.Stmts, exprNodeUsesAlert) {
				return true
			}
		}
	}
	return false
}

func nodeUsesAlert(vn *ast.VisualNode) bool {
	for _, evt := range vn.Events {
		if evt.SNGL != nil && exprNodeUsesAlert(evt.SNGL) {
			return true
		}
	}
	return slices.ContainsFunc(vn.Children, nodeUsesAlert)
}

func exprNodeUsesAlert(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.MethodExpr:
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok && ident.Name == "Alert" {
			return true
		}
	case *ast.StmtBlock:
		if slices.ContainsFunc(e.Stmts, exprNodeUsesAlert) {
			return true
		}
	case *ast.CallStmt:
		return exprNodeUsesAlert(e.Call)
	}
	return false
}
