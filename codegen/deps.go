package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ir"
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
func NewDepTrackerFromPkg(pkg *ir.Package) *DepTracker {
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
func (dt *DepTracker) ExprDeps(expr ir.Expr) map[string]bool {
	if expr != nil {
		deps := dt.ExtractDeps(expr)
		return dt.ExpandDeps(deps)
	}
	return nil
}

// ExtractDeps walks an expression and returns direct model field references.
func (dt *DepTracker) ExtractDeps(e ir.Expr) map[string]bool {
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
func MutatedFields(s ir.Stmt) map[string]bool {
	fields := make(map[string]bool)
	if s == nil {
		return fields
	}
	switch n := s.(type) {
	case *ir.Assign:
		if root := FindRootIdent(n.Target); root != "" {
			fields[root] = true
		}
	case *ir.Toggle:
		if root := FindRootIdent(n.Target); root != "" {
			fields[root] = true
		}
	case *ir.CallStmt:
		maps.Copy(fields, MutatedFieldsExpr(n.Call))
	}
	return fields
}

// MutatedFieldsExpr returns mutated fields from an expression (e.g., method call).
func MutatedFieldsExpr(e ir.Expr) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	if call, ok := e.(*ir.Call); ok {
		// Method call — the receiver may be a model field.
		if call.Receiver != nil {
			if root := FindRootIdent(call.Receiver); root != "" {
				fields[root] = true
			}
		}
	}
	return fields
}

// ExtractDeps walks an expression and returns all model field references.
func ExtractDeps(e ir.Expr, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkExprDeps(e, modelFields, deps)
	return deps
}

// walkExprDeps recursively finds model field references in an expression tree.
func walkExprDeps(e ir.Expr, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Ident:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ir.Select:
		if root := FindRootIdent(n.Operand); root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ir.Binary:
		walkExprDeps(n.Left, modelFields, deps)
		walkExprDeps(n.Right, modelFields, deps)
	case *ir.Unary:
		walkExprDeps(n.Operand, modelFields, deps)
	case *ir.Ternary:
		walkExprDeps(n.Cond, modelFields, deps)
		walkExprDeps(n.Then, modelFields, deps)
		walkExprDeps(n.Else, modelFields, deps)
	case *ir.Call:
		if n.Receiver != nil {
			walkExprDeps(n.Receiver, modelFields, deps)
		}
		for _, a := range n.Args {
			walkExprDeps(a.Value, modelFields, deps)
		}
	case *ir.Conversion:
		walkExprDeps(n.Operand, modelFields, deps)
	case *ir.Index:
		walkExprDeps(n.Operand, modelFields, deps)
		walkExprDeps(n.Idx, modelFields, deps)
	case *ir.ListLit:
		for _, el := range n.Elems {
			walkExprDeps(el, modelFields, deps)
		}
	case *ir.StructLit:
		for _, field := range n.Fields {
			walkExprDeps(field.Value, modelFields, deps)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			walkExprDeps(en.Key, modelFields, deps)
			walkExprDeps(en.Value, modelFields, deps)
		}
	case *ir.Spread:
		walkExprDeps(n.Operand, modelFields, deps)
	case *ir.Lambda:
		if n.Func != nil {
			walkStmtsDeps(n.Func.Block, modelFields, deps)
		}
	}
}

// walkStmtsDeps recursively finds model field references in statements.
func walkStmtsDeps(stmts []ir.Stmt, modelFields map[string]bool, deps map[string]bool) {
	for _, s := range stmts {
		walkStmtDeps(s, modelFields, deps)
	}
}

// walkStmtDeps recursively finds model field references in a statement.
func walkStmtDeps(s ir.Stmt, modelFields map[string]bool, deps map[string]bool) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ir.Assign:
		walkExprDeps(n.Value, modelFields, deps)
	case *ir.Emit:
		for _, a := range n.Args {
			walkExprDeps(a.Value, modelFields, deps)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			walkExprDeps(n.Call, modelFields, deps)
		}
	case *ir.Return:
		walkExprDeps(n.Value, modelFields, deps)
	case *ir.LocalVar:
		walkExprDeps(n.Init, modelFields, deps)
	case *ir.If:
		walkExprDeps(n.Cond, modelFields, deps)
		walkStmtsDeps(n.Body, modelFields, deps)
		walkStmtsDeps(n.Else, modelFields, deps)
	case *ir.For:
		walkExprDeps(n.Iter, modelFields, deps)
		walkStmtsDeps(n.Body, modelFields, deps)
		walkStmtsDeps(n.Else, modelFields, deps)
	case *ir.NodeInst:
		for _, p := range n.Props {
			walkExprDeps(p.Value, modelFields, deps)
		}
		collectUsedIRStmtsDeps(n.Children, modelFields, deps)
	case *ir.ErrorBoundary:
		collectUsedIRStmtsDeps(n.Children, modelFields, deps)
		if n.Handler != nil && n.Handler.Func != nil {
			walkStmtsDeps(n.Handler.Func.Block, modelFields, deps)
		}
	}
}

// collectUsedIRStmtsDeps walks IR child statements for deps.
func collectUsedIRStmtsDeps(stmts []ir.Stmt, modelFields map[string]bool, deps map[string]bool) {
	for _, s := range stmts {
		walkStmtDeps(s, modelFields, deps)
	}
}

// FindRootIdent extracts the root identifier from nested select/index expressions.
func FindRootIdent(e ir.Expr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ir.Ident:
		return n.Name
	case *ir.Select:
		return FindRootIdent(n.Operand)
	case *ir.Index:
		return FindRootIdent(n.Operand)
	}
	return ""
}
