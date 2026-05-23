package bubbletea

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{NoContext: true, NoListLambdas: true, NoInlineComponents: true, NoImplicitRecv: true}
}

func (g *Generator) PreviewCSS() string { return previewCSS }

// GenerateSink writes bubbletea source files directly into sink. This is
// the sink-based path platforms migrate to during the codegen unification.
func (g *Generator) GenerateSink(req *codegen.Request, sink codegen.Sink) error {
	c := &compilation{}
	m, err := c.BuildRenderModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	return c.EmitFromRender(m, req, sink)
}

// Generate is the legacy entry point; delegates to GenerateSink via a
// MemSink and converts captured files back to the Response shape.
func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	mem := codegen.NewMemSink()
	if err := g.GenerateSink(req, mem); err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return &codegen.Response{Files: codegen.CollectOutputFiles(mem)}, nil
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

	if h := codegen.Header("bubbletea", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	if err := writeBubbleteaFile(sink, "model.go", src); err != nil {
		return err
	}

	if req.Pkg != nil && hasTestFuncs(req.Pkg) && c.cfg.EmitTests() {
		testSrc, err := CompileTestsIR(c.ctx, c.cfg)
		if err == nil && testSrc != nil {
			if err := writeBubbleteaFile(sink, "model_test.go", testSrc); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeBubbleteaFile(sink codegen.Sink, name string, content []byte) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		w.Close()
		return err
	}
	return w.Close()
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
