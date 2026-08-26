package lspcore

import "git.duckfam.us/jonathan/sngl/ast"

// WalkLiterals invokes fn for every LiteralExpr in the document.
// Minimal walker shared by hover and color features.
func WalkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	var walkBlock func(b ast.StmtBlock)
	var walkArgs func(args ast.ArgList)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.LiteralExpr:
			fn(x)
		case *ast.UnitLiteral:
			fn(&x.LiteralExpr)
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.CallExpr:
			walkE(x.Func)
			walkArgs(x.Args)
		case *ast.SelectExpr:
			walkE(x.Operand)
		case *ast.IndexExpr:
			walkE(x.Operand)
			walkE(x.Index)
		case *ast.TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *ast.ListExpr:
			for _, el := range x.Elements {
				walkE(el)
			}
		case *ast.StructExpr:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *ast.LambdaExpr:
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkE(p)
			}
		case *ast.ParenExpr:
			walkE(x.Inner)
		case *ast.ConstExpr:
			walkE(x.Operand)
		case *ast.SpreadExpr:
			walkE(x.Operand)
		case *ast.MapLit:
			for _, en := range x.Entries {
				walkE(en.Key)
				walkE(en.Value)
			}
		}
	}
	walkArgs = func(args ast.ArgList) {
		for _, a := range args.Args {
			switch arg := a.(type) {
			case ast.Arg:
				walkE(arg.Value)
			case ast.EventHandler:
				walkBlock(arg.Body)
			}
		}
	}
	walkBlock = func(b ast.StmtBlock) {
		for _, c := range b.Stmts {
			walkS(c)
		}
	}
	walkS = func(s ast.Stmt) {
		switch x := s.(type) {
		case nil:
			return
		case *ast.VarDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.ConstDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.AssignStmt:
			walkE(x.Value)
		case *ast.IfStmt:
			walkE(x.Cond)
			walkBlock(x.Body)
			walkBlock(x.Else)
		case *ast.ForStmt:
			walkE(x.Iter)
			walkBlock(x.Body)
			walkBlock(x.Else)
		case *ast.VisualNode:
			walkArgs(x.Args)
			walkBlock(x.Block)
		case *ast.ComponentDecl:
			walkBlock(x.Body)
		case *ast.FuncDef:
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.PlatformStmt:
			walkBlock(x.Body)
		case *ast.ReturnStmt:
			walkE(x.Value)
		case *ast.CallStmt:
			if x.Call != nil {
				walkE(x.Call)
			}
		case *ast.VarStmt:
			walkE(x.Init)
		case *ast.DisabledDecl:
			walkS(x.Inner)
		}
	}
	for _, st := range doc.Stmts {
		walkS(st)
	}
}
