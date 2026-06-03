package none

import (
	"fmt"

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
func (g *Generator) SupportedLangs() []string            { return nil }
func (g *Generator) Package() []*ast.Document            { return nil }
func (g *Generator) Resolve(identifier string) ir.Symbol { return nil }
func (g *Generator) Capabilities() lower.Caps            { return lower.Caps{NoStructSpread: true} }

// Generate rejects code generation requests. The none platform is a marker
// for headless test execution; it emits no files.
func (g *Generator) Generate(*codegen.Request, codegen.Sink) error {
	return fmt.Errorf("none platform does not generate code")
}

func (g *Generator) RunTests(pkg *ir.Package, _ codegen.LangTranslator, _ *ir.StructLit) ([]*codegen.TestResult, error) {
	return testrunner.Run(pkg)
}

// ProbeTest reports the headless interpreter as always available; it is
// pure Go and has no system dependencies.
func (g *Generator) ProbeTest() (bool, string) { return true, "" }
