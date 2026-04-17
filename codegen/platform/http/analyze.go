package http

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

type analysisResult struct {
	*codegen.CommonAnalysis
	serverFields map[string]bool // var names that touch extern/go:// boundary
	clientFields map[string]bool // var names that are pure UI state
}

// analyze returns a stub analysis result.
// TODO: Port to v2 AST — requires Doc.Stmts iteration instead of Doc.App/Data/NativeImports.
func analyze(doc *ast.Document, pkg *ir.Package) *analysisResult {
	common := codegen.AnalyzeCommon(pkg)
	return &analysisResult{
		CommonAnalysis: common,
		serverFields:   make(map[string]bool),
		clientFields:   make(map[string]bool),
	}
}
