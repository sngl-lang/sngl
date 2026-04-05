package checker

import "git.duckfam.us/jonathan/sngl/ast"

// analyzePurity determines the purity level of a SNGL function by walking its body.
// dataNames is the set of component-scoped var names that represent mutable state.
func analyzePurity(fn *ast.FuncDef, dataNames map[string]bool) ast.Purity {
	if fn.IsStdlib {
		return ast.PurityUnknown
	}

	w := &purityWalker{dataNames: dataNames}

	if fn.Body.SNGL != nil {
		w.walk(fn.Body.SNGL)
	}
	if fn.Block != nil {
		for _, s := range fn.Block.Stmts {
			w.walk(s)
		}
		if fn.Block.Return != nil {
			w.walk(fn.Block.Return)
		}
	}

	if w.mutates {
		return ast.PurityMutates
	}
	if w.readsData {
		return ast.PurityReadonly
	}
	return ast.PurityPure
}

// purityWalker walks AST nodes and tracks whether the code reads or mutates
// component-scoped data fields.
type purityWalker struct {
	dataNames map[string]bool
	readsData bool
	mutates   bool
}

func (w *purityWalker) walk(n ast.Node) {
	if n == nil {
		return
	}
	switch e := n.(type) {
	case *ast.LiteralExpr:
		// no-op
	case *ast.IdentExpr:
		if w.dataNames[e.Name] {
			w.readsData = true
		}
	case *ast.BinaryExpr:
		w.walk(e.Left)
		w.walk(e.Right)
	case *ast.UnaryExpr:
		w.walk(e.Operand)
	case *ast.TernaryExpr:
		w.walk(e.Cond)
		w.walk(e.Then)
		w.walk(e.Else)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			w.walk(arg)
		}
	case *ast.MethodExpr:
		w.walk(e.Receiver)
		for _, arg := range e.Args {
			w.walk(arg)
		}
	case *ast.SelectExpr:
		w.walk(e.Operand)
	case *ast.IndexExpr:
		w.walk(e.Operand)
		w.walk(e.Index)
	case *ast.InterpolationExpr:
		for _, p := range e.Parts {
			w.walk(p)
		}
	case *ast.ListExpr:
		for _, el := range e.Elements {
			w.walk(el)
		}
	case *ast.StructExpr:
		for _, f := range e.Fields {
			w.walk(f.Value)
		}
	case *ast.LambdaExpr:
		w.walk(e.Body)
	case *ast.ParenExpr:
		w.walk(e.Inner)

	// Statements
	case *ast.AssignStmt:
		w.mutates = true
		w.walk(e.Value)
	case *ast.ToggleStmt:
		w.mutates = true
	case *ast.EmitStmt:
		w.mutates = true
		for _, arg := range e.Args {
			w.walk(arg)
		}
	case *ast.CallStmt:
		if e.Call != nil {
			w.walk(e.Call)
		}
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			w.walk(s)
		}
	case *ast.VarStmt:
		w.walk(e.Init)
	case *ast.ReturnStmt:
		w.walk(e.Value)
	}
}
