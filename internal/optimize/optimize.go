package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform      string // "html", "bubbletea"
	Language      string // "js", "go"
	Dir           string // project directory (for compile-time go run execution)
	NativeImports map[string]*codegen.NativeDecls // namespace → decls (for pure func eval)

	// FileAssets is populated by Optimize with file:// assets that need
	// copying to the output directory.
	FileAssets []FileAsset
}

// FileAsset records a file that must be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute path on disk
	OutPath string // relative path in output (e.g. "assets/sngl.svg")
}

// foldCtx carries state needed during constant folding.
type foldCtx struct {
	vars          map[string]any
	nativeImports map[string]*codegen.NativeDecls
	dir           string
	fileAssets    []FileAsset
}

// Optimize mutates doc in place: evaluates constant SNGL expressions referencing
// PLATFORM/LANGUAGE and eliminates dead branches.
func Optimize(doc *ast.Document, cfg Config) error {
	ctx := &foldCtx{
		vars: map[string]any{
			"PLATFORM": cfg.Platform,
			"LANGUAGE": cfg.Language,
		},
		nativeImports: cfg.NativeImports,
		dir:           cfg.Dir,
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

	// Expand for-loop windows in the main component into flat window statements.
	expandForWindows(doc, ctx)

	cfg.FileAssets = ctx.fileAssets
	return nil
}

// expandForWindows walks the main component body and expands for-loops
// that contain window declarations over const iterables into individual
// window statements. This enables multi-page HTML generation.
func expandForWindows(doc *ast.Document, ctx *foldCtx) {
	var mainComp *ast.ComponentDecl
	for _, s := range doc.Stmts {
		if comp, ok := s.(*ast.ComponentDecl); ok && comp.Name == "main" {
			mainComp = comp
			break
		}
	}
	if mainComp == nil {
		return
	}

	var expanded []ast.Stmt
	for _, s := range mainComp.Body.Stmts {
		if fs, ok := s.(*ast.ForStmt); ok {
			if stmts := expandForStmt(fs, ctx); stmts != nil {
				expanded = append(expanded, stmts...)
				continue
			}
		}
		expanded = append(expanded, s)
	}
	mainComp.Body.Stmts = expanded
}

// expandForStmt tries to expand a for-loop over a const iterable.
// Returns nil if the iterable can't be evaluated.
func expandForStmt(fs *ast.ForStmt, ctx *foldCtx) []ast.Stmt {
	// Evaluate the iterable
	val, ok := evalConst(fs.Iter, ctx)
	if !ok {
		return nil
	}
	items, ok := val.([]any)
	if !ok {
		return nil
	}

	var result []ast.Stmt
	for i, item := range items {
		// Create a new fold context with the loop variable bound
		loopCtx := &foldCtx{
			vars:          make(map[string]any, len(ctx.vars)+2),
			nativeImports: ctx.nativeImports,
			dir:           ctx.dir,
		}
		for k, v := range ctx.vars {
			loopCtx.vars[k] = v
		}
		loopCtx.vars[fs.Key] = item
		if fs.Value != "" {
			loopCtx.vars[fs.Value] = i
		}

		// Clone and optimize the body for this iteration
		for _, bodyStmt := range fs.Body.Stmts {
			cloned := cloneStmt(bodyStmt)
			optimized := optimizeStmt(cloned, loopCtx)
			if optimized != nil {
				result = append(result, optimized)
			}
		}
	}
	return result
}

// cloneStmt creates a shallow copy of a statement so that in-place optimization
// of one for-loop iteration doesn't corrupt another. Only handles types common
// in for-loop bodies; unknown types are returned as-is (shared).
func cloneStmt(s ast.Stmt) ast.Stmt {
	switch n := s.(type) {
	case *ast.VisualNode:
		cp := *n
		cp.Args = cloneArgList(n.Args)
		cp.Block = cloneStmtBlock(n.Block)
		return &cp
	case *ast.IfStmt:
		cp := *n
		cp.Body = cloneStmtBlock(n.Body)
		cp.Else = cloneStmtBlock(n.Else)
		return &cp
	case *ast.ForStmt:
		cp := *n
		cp.Body = cloneStmtBlock(n.Body)
		cp.Else = cloneStmtBlock(n.Else)
		return &cp
	case *ast.PlatformStmt:
		cp := *n
		cp.Body = cloneStmtBlock(n.Body)
		return &cp
	}
	return s
}

func cloneStmtBlock(b ast.StmtBlock) ast.StmtBlock {
	if len(b.Stmts) == 0 {
		return b
	}
	stmts := make([]ast.Stmt, len(b.Stmts))
	for i, s := range b.Stmts {
		stmts[i] = cloneStmt(s)
	}
	return ast.StmtBlock{Pos: b.Pos, IsMultiline: b.IsMultiline, Stmts: stmts}
}

func cloneArgList(al ast.ArgList) ast.ArgList {
	if len(al.Args) == 0 {
		return al
	}
	args := make([]ast.ArgOrEventHandler, len(al.Args))
	copy(args, al.Args)
	return ast.ArgList{Pos: al.Pos, IsMultiline: al.IsMultiline, Args: args}
}

// optimizeStmt folds constants and eliminates dead branches in a statement.
// Returns nil to remove the statement.
func optimizeStmt(s ast.Stmt, ctx *foldCtx) ast.Stmt {
	switch n := s.(type) {
	case *ast.ConstDecl:
		for i, spec := range n.Specs {
			if spec.Default != nil {
				// Try to evaluate the full expression first (handles complex types like lists/maps).
				if val, ok := evalConst(spec.Default, ctx); ok {
					for _, name := range spec.Names {
						ctx.vars[name] = val
					}
				}
				n.Specs[i].Default = foldExpr(spec.Default, ctx)
				// Also register simple literal values.
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
	if lit := valueToLiteral(val); lit != nil {
		return lit
	}
	return expr
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
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1]
		}
		return raw
	}
	return nil
}
