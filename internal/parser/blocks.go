package parser

import (
	"sort"

	"git.duckfam.us/jonathan/sngl/ast"
)

// stmtBlocks lists every brace-delimited block a statement opens, in source
// order: its own body, and the bodies of any event handler or lambda written
// in its arguments. It does not descend into those blocks' statements — the
// callers walk statement by statement and reach them on the way down.
//
// Comment placement and blank-line spacing both need this list. A comment
// inside `button(@click { … })` belongs to the handler's block, and the
// statement after that button is only adjacent to it if the closing brace is
// on the line above.
func stmtBlocks(s ast.Stmt) (blocks []*ast.StmtBlock, commit func()) {
	var out []*ast.StmtBlock
	var commits []func()
	add := func(b *ast.StmtBlock) {
		if b.IsDefined() {
			out = append(out, b)
		}
	}
	var walkExpr func(e ast.Expr)
	var walkArgs func(args *ast.ArgList)
	walkExpr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkExpr(p)
			}
		case *ast.I18nInterpExpr:
			for _, p := range x.Parts {
				walkExpr(p)
			}
		case *ast.I18nPlaceholderExpr:
			walkExpr(x.Value)
			for _, c := range x.Cases {
				for _, p := range c.Body {
					walkExpr(p)
				}
			}
		case *ast.LambdaExpr:
			add(&x.Block)
			walkExpr(x.Body)
		case *ast.CallExpr:
			walkExpr(x.Func)
			walkArgs(&x.Args)
		case *ast.BinaryExpr:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ast.UnaryExpr:
			walkExpr(x.Operand)
		case *ast.ParenExpr:
			walkExpr(x.Inner)
		case *ast.TernaryExpr:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ast.ListExpr:
			for _, el := range x.Elements {
				walkExpr(el)
			}
		case *ast.StructExpr:
			for _, fl := range x.Fields {
				walkExpr(fl.Value)
			}
		case *ast.MapLit:
			for _, en := range x.Entries {
				walkExpr(en.Key)
				walkExpr(en.Value)
			}
		case *ast.IndexExpr:
			walkExpr(x.Operand)
			walkExpr(x.Index)
		case *ast.SelectExpr:
			walkExpr(x.Operand)
		case *ast.SpreadExpr:
			walkExpr(x.Operand)
		case *ast.ConstExpr:
			walkExpr(x.Operand)
		}
	}
	walkArgs = func(args *ast.ArgList) {
		for i := range args.Args {
			switch a := args.Args[i].(type) {
			case ast.Arg:
				walkExpr(a.Value)
			case ast.EventHandler:
				// The list holds handlers by value, so a caller that edits the
				// block edits a copy; commit puts it back.
				handler := a
				if handler.Body.IsDefined() {
					out = append(out, &handler.Body)
					commits = append(commits, func() { args.Args[i] = handler })
				}
			}
		}
	}
	switch x := s.(type) {
	case *ast.FuncDef:
		walkExpr(x.Body)
		add(&x.Block)
	case *ast.ComponentDecl:
		add(&x.Body)
	case *ast.VisualNode:
		walkArgs(&x.Args)
		add(&x.Block)
	case *ast.SlotNode:
		walkArgs(&x.Args)
		add(&x.Block)
	case *ast.IfStmt:
		walkExpr(x.Cond)
		add(&x.Body)
		add(&x.Else)
	case *ast.ForStmt:
		walkExpr(x.Iter)
		add(&x.Body)
		add(&x.Else)
	case *ast.VarDecl:
		for i := range x.Specs {
			walkExpr(x.Specs[i].Default)
			for j := range x.Specs[i].Handlers {
				add(&x.Specs[i].Handlers[j].Body)
			}
		}
	case *ast.ConstDecl:
		for i := range x.Specs {
			walkExpr(x.Specs[i].Default)
			for j := range x.Specs[i].Handlers {
				add(&x.Specs[i].Handlers[j].Body)
			}
		}
	case *ast.CallStmt:
		walkExpr(x.Call)
	case *ast.AssignStmt:
		walkExpr(x.Value)
	case *ast.ReturnStmt:
		walkExpr(x.Value)
	case *ast.VarStmt:
		walkExpr(x.Init)
	case *ast.DisabledDecl:
		if x.Inner != nil {
			inner, innerCommit := stmtBlocks(x.Inner)
			out = append(out, inner...)
			commits = append(commits, innerCommit)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos.Line < out[j].Pos.Line })
	return out, func() {
		for _, c := range commits {
			c()
		}
	}
}
