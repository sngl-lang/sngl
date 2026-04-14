package codegen

import (
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
	Components     []*ast.ComponentDecl
	Structs        []*ast.StructDef
	Enums          []*ast.EnumDef
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
	Body       *ast.StmtBlock
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
		UsedComponents: make(map[string]bool),
	}

	for _, stmt := range doc.Stmts {
		switch s := stmt.(type) {
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				for _, name := range spec.Names {
					a.ModelFields[name] = true
				}
			}
		case *ast.FuncDef:
			a.FuncNames[s.Name] = true
			if s.Body != nil && len(s.Params.Params) == 0 {
				a.ModelFields[s.Name] = true
				a.ComputedFields[s.Name] = true
			}
		case *ast.StructDef:
			fields := make([]string, len(s.Fields))
			for i, f := range s.Fields {
				fields[i] = f.Name
			}
			a.StructFields[s.Name] = fields
			a.Structs = append(a.Structs, s)
		case *ast.EnumDef:
			a.Enums = append(a.Enums, s)
		case *ast.ComponentDecl:
			a.Components = append(a.Components, s)
		}
	}

	// Computed dependency map
	for _, stmt := range doc.Stmts {
		if fn, ok := stmt.(*ast.FuncDef); ok {
			if fn.Body != nil && len(fn.Params.Params) == 0 {
				a.ComputedDeps[fn.Name] = ExtractDeps(fn.Body, a.ModelFields)
			}
		}
	}

	// Walk visual tree to collect used primitive component names.
	for _, stmt := range doc.Stmts {
		if comp, ok := stmt.(*ast.ComponentDecl); ok {
			collectUsedStmts(comp.Body.Stmts, a.UsedComponents)
		}
	}

	return a
}

// collectUsedStmts walks statements collecting used visual node names.
func collectUsedStmts(stmts []ast.Stmt, used map[string]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VisualNode:
			if name := VisualNodeName(n); name != "" {
				used[name] = true
			}
			collectUsedStmts(n.Block.Stmts, used)
		case *ast.IfStmt:
			collectUsedStmts(n.Body.Stmts, used)
			collectUsedStmts(n.Else.Stmts, used)
		case *ast.ForStmt:
			collectUsedStmts(n.Body.Stmts, used)
			collectUsedStmts(n.Else.Stmts, used)
		case *ast.PlatformStmt:
			collectUsedStmts(n.Body.Stmts, used)
		}
	}
}

// VisualNodeName extracts the component/element name from a VisualNode's Target.
func VisualNodeName(vn *ast.VisualNode) string {
	if vn.Target == nil {
		return ""
	}
	switch t := vn.Target.(type) {
	case *ast.IdentExpr:
		return t.Name
	case *ast.SelectExpr:
		// pkg.Component
		if ident, ok := t.Operand.(*ast.IdentExpr); ok {
			return ident.Name + "." + t.Field
		}
	}
	return ""
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

// IntervalToMs converts a duration expression to milliseconds.
func IntervalToMs(expr ast.Expr) int {
	if expr == nil {
		return 0
	}
	ul, ok := expr.(*ast.UnitLiteral)
	if !ok {
		return 0
	}
	// Parse the numeric part from the raw literal
	raw := ul.LiteralExpr.Raw
	num := parseNumber(raw)
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

// ASTUsesAlert reports whether a document uses Alert.toast/info/warn/error.
func ASTUsesAlert(doc *ast.Document) bool {
	for _, stmt := range doc.Stmts {
		if stmtUsesAlert(stmt) {
			return true
		}
	}
	return false
}

func stmtUsesAlert(s ast.Stmt) bool {
	switch n := s.(type) {
	case *ast.VisualNode:
		for _, a := range n.Args.Args {
			if eh, ok := a.(ast.EventHandler); ok {
				for _, bs := range eh.Body.Stmts {
					if stmtUsesAlert(bs) {
						return true
					}
				}
			}
		}
		for _, bs := range n.Block.Stmts {
			if stmtUsesAlert(bs) {
				return true
			}
		}
	case *ast.CallStmt:
		if n.Call != nil {
			if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
				if ident, ok := sel.Operand.(*ast.IdentExpr); ok && ident.Name == "Alert" {
					return true
				}
			}
		}
	case *ast.FuncDef:
		if n.IsTest() {
			return false
		}
		for _, bs := range n.Block.Stmts {
			if stmtUsesAlert(bs) {
				return true
			}
		}
	case *ast.ComponentDecl:
		for _, bs := range n.Body.Stmts {
			if stmtUsesAlert(bs) {
				return true
			}
		}
	}
	return false
}
