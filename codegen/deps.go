package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ast"
)

// DepTracker tracks reactive dependencies between state fields, computed
// fields, and updaters. Platforms build one during analysis and use it
// for selective update codegen and template data.
type DepTracker struct {
	ModelFields    map[string]bool            // all state + computed field names
	ComputedFields map[string]bool            // subset that are computed
	ComputedDeps   map[string]map[string]bool // computed name → root field deps
}

// NewDepTracker creates a DepTracker.
func NewDepTracker(modelFields, computedFields map[string]bool, computedDeps map[string]map[string]bool) *DepTracker {
	return &DepTracker{
		ModelFields:    modelFields,
		ComputedFields: computedFields,
		ComputedDeps:   computedDeps,
	}
}

// ExprDeps returns the set of root state fields referenced by an ast.Expr,
// expanding through computed fields automatically.
func (dt *DepTracker) ExprDeps(expr ast.Expr) map[string]bool {
	if expr.SNGL != nil {
		deps := dt.ExtractDeps(expr.SNGL)
		return dt.ExpandDeps(deps)
	}
	return nil
}

// ExtractDeps walks an AST node and returns direct model field references.
func (dt *DepTracker) ExtractDeps(e ast.Node) map[string]bool {
	deps := make(map[string]bool)
	walkDeps(e, dt.ModelFields, deps)
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
// fields that transitively depend on them. Used by findAffectedUpdaters.
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

// Dependent is implemented by types that have dependency fields (updaters, handlers).
type Dependent interface {
	DepFields() map[string]bool
}

// FindAffected returns items from the slice whose deps intersect with
// the expanded mutated fields.
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

// MutatedFields returns the set of field names mutated by a statement AST node.
func MutatedFields(e ast.Node) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	switch n := e.(type) {
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			maps.Copy(fields, MutatedFields(stmt))
		}
	case *ast.AssignStmt:
		if root := findMutationRoot(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		if root := findMutationRoot(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.CallStmt:
		maps.Copy(fields, MutatedFields(n.Call))
	case *ast.CallExpr:
		if len(n.Args) >= 1 {
			if root := findMutationRoot(n.Args[0]); root != "" {
				fields[root] = true
			}
		}
	case *ast.MethodExpr:
		if root := findMutationRoot(n.Receiver); root != "" {
			fields[root] = true
		}
	}
	return fields
}

// ExtractDeps walks an AST node and returns all model field references.
// Standalone version for use before a DepTracker is constructed.
func ExtractDeps(e ast.Node, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkDeps(e, modelFields, deps)
	return deps
}

// walkDeps recursively finds model field references in an expression tree.
func walkDeps(e ast.Node, modelFields map[string]bool, deps map[string]bool) {
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
		walkDeps(n.Left, modelFields, deps)
		walkDeps(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkDeps(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkDeps(n.Cond, modelFields, deps)
		walkDeps(n.Then, modelFields, deps)
		walkDeps(n.Else, modelFields, deps)
	case *ast.CallExpr:
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.MethodExpr:
		walkDeps(n.Receiver, modelFields, deps)
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.IndexExpr:
		walkDeps(n.Operand, modelFields, deps)
		walkDeps(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkDeps(el, modelFields, deps)
		}
	case *ast.StructExpr:
		for _, field := range n.Fields {
			walkDeps(field.Value, modelFields, deps)
		}
	case *ast.SpreadExpr:
		walkDeps(n.Operand, modelFields, deps)
	case *ast.InterpolationExpr:
		for _, part := range n.Parts {
			walkDeps(part, modelFields, deps)
		}
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			walkDeps(stmt, modelFields, deps)
		}
	case *ast.AssignStmt:
		walkDeps(n.Target, modelFields, deps)
		walkDeps(n.Value, modelFields, deps)
	case *ast.ToggleStmt:
		walkDeps(n.Target, modelFields, deps)
	case *ast.EmitStmt:
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.ParenExpr:
		walkDeps(n.Inner, modelFields, deps)
	}
}

// FindRootIdent extracts the root identifier from nested select/index expressions.
func FindRootIdent(e ast.Node) string {
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
	case *ast.MethodExpr:
		return FindRootIdent(n.Receiver)
	case *ast.ParenExpr:
		return FindRootIdent(n.Inner)
	}
	return ""
}

// findMutationRoot extracts the root field name from a mutation target.
func findMutationRoot(e ast.Node) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findMutationRoot(n.Operand)
	case *ast.IndexExpr:
		return findMutationRoot(n.Operand)
	}
	return ""
}
