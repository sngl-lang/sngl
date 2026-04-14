package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// DepTracker tracks reactive dependencies between state fields, computed
// fields, and updaters.
type DepTracker struct {
	ModelFields    map[string]bool
	ComputedFields map[string]bool
	ComputedDeps   map[string]map[string]bool
}

// NewDepTracker creates a DepTracker.
func NewDepTracker(modelFields, computedFields map[string]bool, computedDeps map[string]map[string]bool) *DepTracker {
	return &DepTracker{
		ModelFields:    modelFields,
		ComputedFields: computedFields,
		ComputedDeps:   computedDeps,
	}
}

// NewDepTrackerFromPkg derives a DepTracker from a checker.Package.
// State fields come from Pkg.Vars, computed fields from zero-param funcs,
// and computed deps from Func.Reads (purity analysis).
func NewDepTrackerFromPkg(pkg *checker.Package) *DepTracker {
	model := make(map[string]bool)
	computed := make(map[string]bool)
	computedDeps := make(map[string]map[string]bool)

	for _, v := range pkg.Vars {
		model[v.Name] = true
	}

	for _, f := range pkg.Funcs {
		if IsComputed(f) {
			model[f.Name] = true
			computed[f.Name] = true
			deps := make(map[string]bool)
			for _, r := range f.Reads {
				deps[r.Name] = true
			}
			computedDeps[f.Name] = deps
		}
	}

	// Include component-level vars and computeds for the main component.
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			model[v.Name] = true
		}
		for _, f := range comp.Funcs {
			if IsComputed(f) {
				model[f.Name] = true
				computed[f.Name] = true
				deps := make(map[string]bool)
				for _, r := range f.Reads {
					deps[r.Name] = true
				}
				computedDeps[f.Name] = deps
			}
		}
	}

	return &DepTracker{
		ModelFields:    model,
		ComputedFields: computed,
		ComputedDeps:   computedDeps,
	}
}

// ExprDeps returns the set of root state fields referenced by an expression.
func (dt *DepTracker) ExprDeps(expr ast.Expr) map[string]bool {
	if expr != nil {
		deps := dt.ExtractDeps(expr)
		return dt.ExpandDeps(deps)
	}
	return nil
}

// ExtractDeps walks an expression and returns direct model field references.
func (dt *DepTracker) ExtractDeps(e ast.Expr) map[string]bool {
	deps := make(map[string]bool)
	walkExprDeps(e, dt.ModelFields, deps)
	return deps
}

// ExpandDeps adds transitive dependencies through computed fields.
func (dt *DepTracker) ExpandDeps(deps map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for d := range deps {
		result[d] = true
		if dt.ComputedFields[d] {
			if compDeps, ok := dt.ComputedDeps[d]; ok {
				maps.Copy(result, compDeps)
			}
		}
	}
	return result
}

// ExpandMutated expands a set of mutated field names to include any computed
// fields that transitively depend on them.
func (dt *DepTracker) ExpandMutated(mutated map[string]bool) map[string]bool {
	expanded := make(map[string]bool)
	maps.Copy(expanded, mutated)
	for compName, compDeps := range dt.ComputedDeps {
		for dep := range compDeps {
			if mutated[dep] {
				expanded[compName] = true
			}
		}
	}
	return expanded
}

// Dependent is implemented by types that have dependency fields.
type Dependent interface {
	DepFields() map[string]bool
}

// FindAffected returns items whose deps intersect with expanded mutated fields.
func FindAffected[T Dependent](dt *DepTracker, items []T, mutated map[string]bool) []T {
	if len(mutated) == 0 {
		return nil
	}
	expanded := dt.ExpandMutated(mutated)
	var result []T
	for _, item := range items {
		for dep := range item.DepFields() {
			if expanded[dep] {
				result = append(result, item)
				break
			}
		}
	}
	return result
}

// MutatedFields returns the set of field names mutated by a statement.
func MutatedFields(s ast.Stmt) map[string]bool {
	fields := make(map[string]bool)
	if s == nil {
		return fields
	}
	switch n := s.(type) {
	case *ast.AssignStmt:
		if root := findMutationRoot(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		if root := findMutationRoot(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.CallStmt:
		maps.Copy(fields, MutatedFieldsExpr(n.Call))
	}
	return fields
}

// MutatedFieldsExpr returns mutated fields from an expression (e.g., method call).
func MutatedFieldsExpr(e ast.Expr) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	if call, ok := e.(*ast.CallExpr); ok {
		// Method call — the receiver may be a model field
		if sel, ok := call.Func.(*ast.SelectExpr); ok {
			if root := FindRootIdent(sel.Operand); root != "" {
				fields[root] = true
			}
		}
	}
	return fields
}

// ExtractDeps walks an expression and returns all model field references.
func ExtractDeps(e ast.Expr, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkExprDeps(e, modelFields, deps)
	return deps
}

// walkExprDeps recursively finds model field references in an expression tree.
func walkExprDeps(e ast.Expr, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ast.SelectExpr:
		if root := FindRootIdent(n.Operand); root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ast.BinaryExpr:
		walkExprDeps(n.Left, modelFields, deps)
		walkExprDeps(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkExprDeps(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkExprDeps(n.Cond, modelFields, deps)
		walkExprDeps(n.Then, modelFields, deps)
		walkExprDeps(n.Else, modelFields, deps)
	case *ast.CallExpr:
		walkExprDeps(n.Func, modelFields, deps)
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				walkExprDeps(arg.Value, modelFields, deps)
			}
		}
	case *ast.IndexExpr:
		walkExprDeps(n.Operand, modelFields, deps)
		walkExprDeps(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkExprDeps(el, modelFields, deps)
		}
	case *ast.StructExpr:
		for _, field := range n.Fields {
			walkExprDeps(field.Value, modelFields, deps)
		}
	case *ast.SpreadExpr:
		walkExprDeps(n.Operand, modelFields, deps)
	case *ast.InterpolationExpr:
		for _, part := range n.Parts {
			walkExprDeps(part, modelFields, deps)
		}
	case *ast.ParenExpr:
		walkExprDeps(n.Inner, modelFields, deps)
	case *ast.LambdaExpr:
		if n.Body != nil {
			walkExprDeps(n.Body, modelFields, deps)
		}
	}
}

// walkStmtDeps recursively finds model field references in statements.
func walkStmtDeps(s ast.Stmt, modelFields map[string]bool, deps map[string]bool) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.AssignStmt:
		walkExprDeps(n.Value, modelFields, deps)
	case *ast.EmitStmt:
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				walkExprDeps(arg.Value, modelFields, deps)
			}
		}
	case *ast.CallStmt:
		if n.Call != nil {
			walkExprDeps(n.Call, modelFields, deps)
		}
	case *ast.ReturnStmt:
		walkExprDeps(n.Value, modelFields, deps)
	case *ast.VarStmt:
		walkExprDeps(n.Init, modelFields, deps)
	}
}

// FindRootIdent extracts the root identifier from nested select/index expressions.
func FindRootIdent(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return FindRootIdent(n.Operand)
	case *ast.IndexExpr:
		return FindRootIdent(n.Operand)
	case *ast.ParenExpr:
		return FindRootIdent(n.Inner)
	}
	return ""
}

// findMutationRoot extracts the root field name from a mutation target.
func findMutationRoot(e ast.TargetExpr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return FindRootIdent(n.Operand)
	case *ast.IndexExpr:
		return FindRootIdent(n.Operand)
	}
	return ""
}
