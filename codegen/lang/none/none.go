// Package none is a marker LangTranslator used by the html platform to
// select static-site mode. It carries no translation logic — its methods are
// never called on the static path (html routes inline-JS emission through
// the js translator internally) and html rejects route mode with lang=none.
package none

import (

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

type Translator struct{}

func (t *Translator) LanguageIdentifier() string { return "none" }
func (t *Translator) Description() string {
	return "Sentinel language for static output modes that need no backing code."
}
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }
func (t *Translator) Capabilities() lower.Caps            { return lower.Caps{} }

func (t *Translator) GenerateIdentifier(name *ir.Ident) string { return name.Name }
func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string { return "" }
func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return nil
}
func (t *Translator) TranslateIRLiteral(e ir.Expr) string { return "" }
func (t *Translator) TypeToNative(hint string) string     { return hint }
func (t *Translator) ExportName(name string) string       { return name }

// RenderHeader returns nil; the none lang emits no file header.
func (t *Translator) RenderHeader(name string, imports []codegen.ImportSpec) []byte {
	return nil
}

// RenderSourceMap returns no changes; the none lang produces no source maps.
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return codegen.SourceMapResult{}
}
