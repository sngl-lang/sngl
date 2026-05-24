package bubbletea

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

//go:embed bubbletea.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, err := parser.Parse("bubbletea.sngl", []byte(pkgSource))
	if err != nil {
		panic(fmt.Errorf("platform bubbletea init: parsing bubbletea.sngl: %w", err))
	}
	pkgDocs = []*ast.Document{doc}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Bubbletea.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "bubbletea" }
func (g *Generator) Description() string {
	return "Terminal UI, written in Go using the Bubble Tea framework."
}
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) Package() []*ast.Document { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol {
	// Bubbletea accepts any tag name; its codegen reads metadata directly
	// from blueprint .sngl bodies (VJoin/HJoin/Styled/TextInput).
	return &ir.Component{Name: identifier}
}
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{StructComponents: true, StdlibContextParam: true, NoListLambdas: true, NoInlineComponents: true, NoImplicitRecv: true}
}

func (g *Generator) PreviewCSS() string { return previewCSS }

// Generate writes bubbletea source files directly into sink. This is
// the sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent-mode test build: suppress the user's main() loop and force
	// the package name to "main" so the agent's func main() compiles
	// alongside it. The launcher's `go build .` step expects a main
	// package on disk.
	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	if agentMode {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
		codegen.SetOptionField(req.Options, "package", "main")
	}

	c := &compilation{}
	m, err := c.BuildRenderModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	if err := c.EmitFromRender(m, req, sink); err != nil {
		return err
	}
	if golang.PackageUsesI18n(req.Pkg) {
		if err := golang.EmitI18nManifestEmbed(sink, c.cfg.Package, req.ProjectFS, ""); err != nil {
			return fmt.Errorf("bubbletea: i18n manifest embed: %w", err)
		}
	}

	if agentMode && codegen.OptionBool(req.Options, "test") {
		testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
		if len(testFns) > 0 {
			src := golang.LowerTestFile(c.cfg.Package, testFns, suffixes, methodFields, golang.TestEmitAgent)
			if err := writeRawFile(sink, "testagent_main.go", []byte(src)); err != nil {
				return err
			}
			// newTestComponent is the constructor LowerTestFile generates
			// against; bubbletea's Model is a value type so we just call
			// New() and let Go's local-addressability handle `c.field = …`.
			mainSrc := []byte("package " + c.cfg.Package + "\n\nimport \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\nfunc newTestComponent() Model { return New() }\n\nfunc main() { testagent.Main() }\n")
			if err := writeRawFile(sink, "agent_main.go", mainSrc); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeRawFile writes pre-formatted content directly to sink without
// running it through the language file emitter (which would re-attach
// source maps and headers we don't want on synthetic test/agent files).
func writeRawFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}

// compilation holds per-request build state that flows between
// BuildRenderModel and EmitFromRender.
type compilation struct {
	ctx *codegen.CodegenCtx
	cfg Config
}

func (c *compilation) BuildRenderModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.RenderModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("bubbletea: unsupported lang %q", req.Lang.LanguageIdentifier())
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("bubbletea: %w", err)
	}
	c.ctx = codegen.NewCodegenCtx(req, "bubbletea")

	var stmts []ir.Stmt
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildRender(stmts), nil
}

func (c *compilation) EmitFromRender(_ *codegen.RenderModel, req *codegen.Request, sink codegen.Sink) error {
	src, err := CompileIR(c.ctx, c.cfg)
	if err != nil {
		return err
	}

	if err := writeBubbleteaFile(sink, "model.go", req.Lang, req, src); err != nil {
		return err
	}

	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	if !agentMode && req.Pkg != nil && hasTestFuncs(req.Pkg) && c.cfg.EmitTests() {
		testSrc, err := CompileTestsIR(c.ctx, c.cfg)
		if err == nil && testSrc != nil {
			if err := writeBubbleteaFile(sink, "model_test.go", req.Lang, req, testSrc); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeBubbleteaFile(sink codegen.Sink, name string, lang codegen.LangTranslator, req *codegen.Request, content []byte) error {
	e := lang.NewFileEmitter(sink, codegen.FileOptions{
		Name:     name,
		Source:   req.Source,
		Platform: "bubbletea",
		Maps:     req.Maps,
	})
	if _, err := e.Write(content); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}

func hasTestFuncs(pkg *ir.Package) bool {
	for _, f := range pkg.Funcs {
		if f.IsTest {
			return true
		}
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			if f.IsTest {
				return true
			}
		}
	}
	return false
}
