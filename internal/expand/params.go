package expand

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkParamAttrs refuses every mark written on a parameter or a component
// prop. The grammar accepts one there, but no macro may consume it: there is no
// handler contract for the position, so the mark is refused rather than carried
// nowhere. A macro that resolves is still refused — a handler transforms a
// declaration, and a parameter is not one.
//
// A resolution failure keeps the wording it has in statement position, so the
// same mistake reads the same way wherever it is written.
func checkParamAttrs(doc *ast.Document, aliases map[string]imports.ImportRef, dotPkgs []string) []ir.Diagnostic {
	var diags []ir.Diagnostic
	forEachParam(doc, func(p ast.Param) {
		for _, attr := range p.Attrs {
			uri, resolved, resolveDiags := resolveMacroPkg(attr, aliases, dotPkgs)
			if len(resolveDiags) > 0 {
				diags = append(diags, resolveDiags...)
				continue
			}
			if resolved {
				if _, found := lookupPre(uri, attr.Name); !found {
					diags = append(diags, ir.Diagnostic{
						Pos:      attr.Pos,
						Msg:      unknownMacroMsg(attr.Name, uri),
						Severity: ir.Error,
					})
					continue
				}
			}
			// A real macro. The position refuses it: a handler transforms a
			// declaration, and a parameter is not one.
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("#[%s] cannot mark a parameter", macroName(attr)),
				Severity: ir.Error,
			})
		}
	})
	return diags
}

// forEachParam invokes fn for every parameter and component prop in doc.
//
// Only three forms hold a parameter the grammar lets carry a mark — FuncDef,
// LambdaExpr and ComponentDecl — but a lambda reaches anywhere an expression
// does, so finding them all means walking the document. An EventHandler's
// parameters parse from an IdentList and cannot carry a mark.
//
// A disabled (`/-`) declaration is not descended into: the checker skips it, so
// a mark inside one is in code that never compiles.
func forEachParam(doc *ast.Document, fn func(ast.Param)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	var walkBlock func(b ast.StmtBlock)
	var walkArgs func(args ast.ArgList)

	params := func(pl ast.ParamList) {
		for _, p := range pl.Params {
			fn(p)
		}
	}
	props := func(pl ast.PropList) {
		for _, p := range pl.Props {
			if param, ok := p.(ast.Param); ok {
				fn(param)
			}
		}
	}

	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
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
			params(x.Params)
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
		case *ast.FuncDef:
			params(x.Params)
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.ComponentDecl:
			props(x.Props)
			walkBlock(x.Body)
		case *ast.StructDef:
			for _, item := range x.Body {
				if inner, ok := ast.UnwrapAttrs(item).(ast.Stmt); ok {
					walkS(inner)
				}
			}
		case *ast.VarDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
				for _, h := range sp.Handlers {
					walkBlock(h.Body)
				}
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
		case *ast.AttrDecl:
			walkS(x.Inner)
		}
	}

	for _, st := range doc.Stmts {
		walkS(st)
	}
}
