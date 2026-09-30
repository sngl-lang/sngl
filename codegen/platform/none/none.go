package none

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
	// The interpreter answers navigation itself (internal/interp/nav.go):
	// `go` and `back` on a stack, and the `follow` this package's link
	// override calls. Each is a statement about a tree the interpreter holds,
	// not an expression an emitter could render, which is what declaring the
	// package rather than an emitter per id is for.
	codegen.DeclarePlatformImplements("none", "sngl:ui/nav")
	codegen.DeclarePlatformImplements("none", "sngl:platform/none")
}

type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "none" }
func (g *Generator) Description() string {
	return "Marker platform for headless test execution — generates no code."
}
func (g *Generator) SupportedLangs() []string { return nil }

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
