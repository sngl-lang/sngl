package none

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() { codegen.RegisterPlatform(&Generator{}) }

type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "none" }
func (g *Generator) Description() string {
	return "Marker platform for headless test execution — generates no code."
}
func (g *Generator) SupportedLangs() []string               { return nil }
func (g *Generator) Package() []*ast.Document               { return nil }
func (g *Generator) Resolve(identifier string) ir.Symbol    { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return false }
func (g *Generator) Capabilities() lower.Caps               { return lower.Caps{} }

func (g *Generator) Generate(*codegen.Request) (*codegen.Response, error) {
	return &codegen.Response{Error: "none platform does not generate code"}, nil
}

func (g *Generator) RunTests(pkg *ir.Package, _ codegen.LangTranslator) ([]*codegen.TestResult, error) {
	return testrunner.Run(pkg)
}

// ProbeTest reports the headless interpreter as always available; it is
// pure Go and has no system dependencies.
func (g *Generator) ProbeTest() (bool, string) { return true, "" }
