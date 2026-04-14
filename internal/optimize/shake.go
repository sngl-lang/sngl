package optimize

import (
	"log/slog"

	"git.duckfam.us/jonathan/sngl/ast"
)

// shakeUnused removes consts, data, and functions that are not referenced
// in the remaining AST after compile-time expansion and folding.
func shakeUnused(doc *ast.Document) {
	refs := collectRefs(doc)

	var out []ast.Stmt
	for _, s := range doc.Stmts {
		switch n := s.(type) {
		case *ast.ConstDecl:
			if anyNameReferenced(n.Specs, refs) {
				out = append(out, s)
			} else {
				slog.Debug("shaken: const decl")
			}
		case *ast.VarDecl:
			if anyNameReferenced(n.Specs, refs) {
				out = append(out, s)
			} else {
				slog.Debug("shaken: var decl")
			}
		case *ast.FuncDef:
			if refs[n.Name] || n.IsTest() {
				out = append(out, s)
			} else {
				slog.Debug("shaken: func", "name", n.Name)
			}
		case *ast.StructDef:
			if refs[n.Name] {
				out = append(out, s)
			} else {
				slog.Debug("shaken: struct", "name", n.Name)
			}
		default:
			out = append(out, s)
		}
	}
	doc.Stmts = out
}

func anyNameReferenced(specs []ast.VarSpec, refs map[string]bool) bool {
	for _, spec := range specs {
		for _, name := range spec.Names {
			if refs[name] {
				return true
			}
		}
	}
	return false
}

// collectRefs walks the document AST and returns all identifier names
// that are still referenced in expressions.
func collectRefs(doc *ast.Document) map[string]bool {
	refs := map[string]bool{}
	for _, s := range doc.Stmts {
		collectStmtRefs(s, refs)
	}
	return refs
}

func collectStmtRefs(s ast.Stmt, refs map[string]bool) {
	switch n := s.(type) {
	case *ast.VarDecl:
		for _, spec := range n.Specs {
			if spec.Default != nil {
				collectExprRefs(spec.Default, refs)
			}
			for _, h := range spec.Handlers {
				for _, bs := range h.Body.Stmts {
					collectStmtRefs(bs, refs)
				}
			}
		}
	case *ast.FuncDef:
		if n.Body != nil {
			collectExprRefs(n.Body, refs)
		}
		for _, bs := range n.Block.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.ComponentDecl:
		for _, p := range n.Props.Props {
			if param, ok := p.(ast.Param); ok && param.Default != nil {
				collectExprRefs(param.Default, refs)
			}
		}
		for _, bs := range n.Body.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.VisualNode:
		for _, a := range n.Args.Args {
			switch x := a.(type) {
			case ast.Arg:
				collectExprRefs(x.Value, refs)
			case ast.EventHandler:
				for _, bs := range x.Body.Stmts {
					collectStmtRefs(bs, refs)
				}
			}
		}
		for _, bs := range n.Block.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.IfStmt:
		collectExprRefs(n.Cond, refs)
		for _, bs := range n.Body.Stmts {
			collectStmtRefs(bs, refs)
		}
		for _, bs := range n.Else.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.ForStmt:
		collectExprRefs(n.Iter, refs)
		for _, bs := range n.Body.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.PlatformStmt:
		for _, bs := range n.Body.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.AssignStmt:
		collectExprRefs(n.Value, refs)
	case *ast.CallStmt:
		if n.Call != nil {
			collectExprRefs(n.Call, refs)
		}
	case *ast.ReturnStmt:
		collectExprRefs(n.Value, refs)
	case *ast.VarStmt:
		collectExprRefs(n.Init, refs)
	case *ast.EmitStmt:
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				collectExprRefs(arg.Value, refs)
			}
		}
	}
}

func collectExprRefs(e ast.Expr, refs map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		refs[n.Name] = true
	case *ast.BinaryExpr:
		collectExprRefs(n.Left, refs)
		collectExprRefs(n.Right, refs)
	case *ast.UnaryExpr:
		collectExprRefs(n.Operand, refs)
	case *ast.TernaryExpr:
		collectExprRefs(n.Cond, refs)
		collectExprRefs(n.Then, refs)
		collectExprRefs(n.Else, refs)
	case *ast.CallExpr:
		collectExprRefs(n.Func, refs)
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				collectExprRefs(arg.Value, refs)
			}
		}
	case *ast.SelectExpr:
		collectExprRefs(n.Operand, refs)
	case *ast.IndexExpr:
		collectExprRefs(n.Operand, refs)
		collectExprRefs(n.Index, refs)
	case *ast.InterpolationExpr:
		for _, p := range n.Parts {
			collectExprRefs(p, refs)
		}
	case *ast.ListExpr:
		for _, el := range n.Elements {
			collectExprRefs(el, refs)
		}
	case *ast.StructExpr:
		refs[n.Name] = true
		for _, f := range n.Fields {
			collectExprRefs(f.Value, refs)
		}
	case *ast.LambdaExpr:
		if n.Body != nil {
			collectExprRefs(n.Body, refs)
		}
		for _, bs := range n.Block.Stmts {
			collectStmtRefs(bs, refs)
		}
	case *ast.ParenExpr:
		collectExprRefs(n.Inner, refs)
	case *ast.SpreadExpr:
		collectExprRefs(n.Operand, refs)
	}
}
