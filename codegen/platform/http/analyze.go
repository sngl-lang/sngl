package http

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

type analysisResult struct {
	*codegen.CommonAnalysis
	serverFields map[string]bool // var names that touch extern/go:// boundary
	clientFields map[string]bool // var names that are pure UI state
}

func analyze(doc *ast.Document) *analysisResult {
	common := codegen.AnalyzeCommon(doc)
	info := &analysisResult{
		CommonAnalysis: common,
		serverFields:   make(map[string]bool),
		clientFields:   make(map[string]bool),
	}

	classifyState(doc, common, info)
	return info
}

// classifyState partitions var declarations into server vs client state.
// Server state: any var whose dependency graph touches an extern fn/var or go:// call.
// Client state: everything else (pure UI state like sidebar toggles).
func classifyState(doc *ast.Document, common *codegen.CommonAnalysis, info *analysisResult) {
	// Collect all extern function/var names (from go:// imports and extern declarations).
	externNames := make(map[string]bool)
	for k := range common.ExternFuncs {
		externNames[k] = true
	}
	for k := range common.ExternVars {
		externNames[k] = true
	}
	// Also include namespace-qualified names from NativeImports.
	for ns, decls := range doc.NativeImports {
		if decls == nil {
			continue
		}
		for _, d := range decls.Data {
			externNames[ns+"."+d.Name] = true
		}
	}

	// Build dependency tracker.
	dt := codegen.NewDepTracker(common.ModelFields, common.ComputedFields, common.ComputedDeps)

	// For each data field, check if its init expression or any event mutation
	// references an extern. If so, mark it as server state.
	tainted := make(map[string]bool)

	// Check init expressions.
	for _, d := range doc.Data {
		if d.Extern || d.IsFunc {
			tainted[d.Name] = true
			continue
		}
		deps := dt.ExprDeps(d.Init)
		for dep := range deps {
			if externNames[dep] {
				tainted[d.Name] = true
				break
			}
		}
	}

	// Walk the visual tree to find expressions that reference both state and externs.
	if doc.App != nil {
		walkExternRefs(doc.App.Children, externNames, tainted, dt)
		for _, win := range doc.App.Windows {
			walkExternRefs(win.Children, externNames, tainted, dt)
		}
	}

	// Transitively expand: if a computed field depends on a tainted field, it's tainted too.
	changed := true
	for changed {
		changed = false
		for name := range common.ComputedDeps {
			if tainted[name] {
				continue
			}
			for dep := range common.ComputedDeps[name] {
				if tainted[dep] {
					tainted[name] = true
					changed = true
					break
				}
			}
		}
	}

	// Classify: tainted → server, rest → client.
	for name := range common.ModelFields {
		if tainted[name] {
			info.serverFields[name] = true
		} else {
			info.clientFields[name] = true
		}
	}
}

// walkExternRefs walks the visual tree and marks state fields as tainted
// if they appear in expressions alongside extern references.
func walkExternRefs(nodes []*ast.VisualNode, externNames map[string]bool, tainted map[string]bool, dt *codegen.DepTracker) {
	for _, vn := range nodes {
		// Check all props for extern references.
		for _, expr := range vn.Props {
			checkExprTaints(expr, externNames, tainted, dt)
		}
		// Check events — mutations that assign from extern results taint the target.
		for _, handler := range vn.Events {
			checkExprTaints(handler.Body, externNames, tainted, dt)
		}
		// Check bindings.
		for _, expr := range vn.Bindings {
			checkExprTaints(expr, externNames, tainted, dt)
		}
		// Check if condition.
		if vn.If != nil {
			checkExprTaints(*vn.If, externNames, tainted, dt)
		}
		// Recurse.
		walkExternRefs(vn.Children, externNames, tainted, dt)
	}
}

// checkExprTaints checks if an expression references any extern names.
// If it does and also references state fields, those state fields become tainted.
func checkExprTaints(expr ast.Expr, externNames map[string]bool, tainted map[string]bool, dt *codegen.DepTracker) {
	deps := dt.ExprDeps(expr)
	hasExtern := false
	for dep := range deps {
		if externNames[dep] {
			hasExtern = true
			break
		}
	}
	if hasExtern {
		// Mark all non-extern deps as tainted (they're state fields used with externs).
		for dep := range deps {
			if !externNames[dep] {
				tainted[dep] = true
			}
		}
	}
}
