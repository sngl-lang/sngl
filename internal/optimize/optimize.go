package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
	Dir      string // project directory (for compile-time go run execution)
}

// foldCtx carries state needed during constant folding.
type foldCtx struct {
	vars map[string]any
	dir  string
}

// Optimize mutates doc in place: evaluates constant SNGL expressions referencing
// PLATFORM/LANGUAGE and eliminates dead branches.
func Optimize(doc *ast.Document, cfg Config) error {
	ctx := &foldCtx{
		vars: map[string]any{
			"PLATFORM": cfg.Platform,
			"LANGUAGE": cfg.Language,
		},
		dir: cfg.Dir,
	}

	// Walk all statements
	var out []ast.Stmt
	for _, stmt := range doc.Stmts {
		result := optimizeStmt(stmt, ctx)
		if result != nil {
			out = append(out, result)
		}
	}
	doc.Stmts = out

	return nil
}

// optimizeStmt folds constants and eliminates dead branches in a statement.
// Returns nil to remove the statement.
func optimizeStmt(s ast.Stmt, ctx *foldCtx) ast.Stmt {
	switch n := s.(type) {
	case *ast.ConstDecl:
		for i, spec := range n.Specs {
			if spec.Default != nil {
				n.Specs[i].Default = foldExpr(spec.Default, ctx)
				// Register const values for downstream use
				if lit, ok := n.Specs[i].Default.(*ast.LiteralExpr); ok {
					for _, name := range spec.Names {
						ctx.vars[name] = parseLiteralValue(lit)
					}
				}
			}
		}
	case *ast.VarDecl:
		for i, spec := range n.Specs {
			if spec.Default != nil {
				n.Specs[i].Default = foldExpr(spec.Default, ctx)
			}
		}
	case *ast.FuncDef:
		if n.Body != nil {
			n.Body = foldExpr(n.Body, ctx)
		}
	case *ast.StructDef:
		for i, f := range n.Fields {
			if f.Default != nil {
				n.Fields[i].Default = foldExpr(f.Default, ctx)
			}
		}
	case *ast.ComponentDecl:
		n.Body.Stmts = optimizeStmts(n.Body.Stmts, ctx)
	case *ast.VisualNode:
		return optimizeVisualNode(n, ctx)
	case *ast.IfStmt:
		return optimizeIfStmt(n, ctx)
	case *ast.ForStmt:
		n.Body.Stmts = optimizeStmts(n.Body.Stmts, ctx)
		n.Else.Stmts = optimizeStmts(n.Else.Stmts, ctx)
	case *ast.PlatformStmt:
		return optimizePlatformStmt(n, ctx)
	}
	return s
}

func optimizeStmts(stmts []ast.Stmt, ctx *foldCtx) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range stmts {
		result := optimizeStmt(s, ctx)
		if result != nil {
			out = append(out, result)
		}
	}
	return out
}

func optimizeVisualNode(vn *ast.VisualNode, ctx *foldCtx) ast.Stmt {
	// Fold args
	for i, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			arg.Value = foldExpr(arg.Value, ctx)
			vn.Args.Args[i] = arg
		}
	}
	// Recurse into block
	vn.Block.Stmts = optimizeStmts(vn.Block.Stmts, ctx)
	return vn
}

func optimizeIfStmt(s *ast.IfStmt, ctx *foldCtx) ast.Stmt {
	s.Cond = foldExpr(s.Cond, ctx)

	// Check if condition was folded to a literal bool
	if lit, ok := s.Cond.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralBool {
		if lit.Raw == "true" {
			// Always true — inline the body (return as-is for now)
			s.Cond = nil
		} else {
			// Always false — dead branch
			return nil
		}
	}

	s.Body.Stmts = optimizeStmts(s.Body.Stmts, ctx)
	s.Else.Stmts = optimizeStmts(s.Else.Stmts, ctx)
	return s
}

func optimizePlatformStmt(s *ast.PlatformStmt, ctx *foldCtx) ast.Stmt {
	if plat, ok := ctx.vars["PLATFORM"]; ok {
		if plat.(string) != s.Platform {
			return nil // non-matching platform: eliminate
		}
	}
	s.Body.Stmts = optimizeStmts(s.Body.Stmts, ctx)
	return s
}

// foldExpr attempts to evaluate an expression as a constant.
func foldExpr(expr ast.Expr, ctx *foldCtx) ast.Expr {
	if expr == nil {
		return nil
	}
	val, ok := evalConst(expr, ctx)
	if !ok {
		return expr
	}
	return valueToLiteral(val)
}

// valueToLiteral converts a Go value to an AST LiteralExpr.
func valueToLiteral(val any) ast.Expr {
	switch v := val.(type) {
	case string:
		return &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: `"` + v + `"`}
	case int:
		return &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: intToStr(v)}
	case float64:
		return &ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: floatToStr(v)}
	case bool:
		raw := "false"
		if v {
			raw = "true"
		}
		return &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: raw}
	case nil:
		return &ast.LiteralExpr{Kind: ast.LiteralNull, Raw: "null"}
	}
	return nil
}

func intToStr(v int) string {
	if v == 0 {
		return "0"
	}
	s := ""
	n := v
	if n < 0 {
		s = "-"
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return s + digits
}

func floatToStr(v float64) string {
	// Simple conversion — good enough for constants
	return fmt.Sprintf("%v", v)
}

func parseLiteralValue(lit *ast.LiteralExpr) any {
	switch lit.Kind {
	case ast.LiteralBool:
		return lit.Raw == "true"
	case ast.LiteralNull:
		return nil
	case ast.LiteralInt:
		n := 0
		for _, c := range lit.Raw {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if len(lit.Raw) > 0 && lit.Raw[0] == '-' {
			n = -n
		}
		return n
	case ast.LiteralFloat:
		// Simplified — won't handle all cases
		return 0.0
	case ast.LiteralStringQuoted:
		if len(lit.Raw) >= 2 {
			return lit.Raw[1 : len(lit.Raw)-1]
		}
		return lit.Raw
	}
	return nil
}
