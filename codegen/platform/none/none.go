package none

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
)

func init() { codegen.RegisterPlatform(&Generator{}) }

type Generator struct{}

func (g *Generator) Platform() string         { return "none" }
func (g *Generator) SupportedLangs() []string { return nil }

func (g *Generator) Generate(*codegen.Request) (*codegen.Response, error) {
	return &codegen.Response{Error: "none platform does not generate code"}, nil
}

func (g *Generator) RunTests(doc *ast.Document, _ codegen.LangTranslator, tests []*ast.TestDef) ([]*testrunner.Result, error) {
	filtered := *doc
	filtered.Tests = tests
	return testrunner.Run(&filtered)
}
