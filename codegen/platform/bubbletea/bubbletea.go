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

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{}
	m, err := c.BuildRenderModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromRender(m, req)
}

// NewRenderCompiler returns a fresh per-request RenderModelEmitter.
func (g *Generator) NewRenderCompiler() codegen.RenderModelEmitter {
	return &compilation{}
}

// compilation holds per-request build state that flows between
// BuildRenderModel and EmitFromRender.
type compilation struct {
	ctx *codegen.CodegenCtx
	cfg Config
}

var (
	_ codegen.RenderModelEmitter    = (*compilation)(nil)
	_ codegen.RenderCompilerFactory = (*Generator)(nil)
)

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

func (c *compilation) EmitFromRender(_ *codegen.RenderModel, req *codegen.Request) (*codegen.Response, error) {
	src, err := CompileIR(c.ctx, c.cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("bubbletea", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	resp := &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", src),
		},
	}

	if req.Pkg != nil && hasTestFuncs(req.Pkg) && c.cfg.EmitTests() {
		testSrc, err := CompileTestsIR(c.ctx, c.cfg)
		if err == nil && testSrc != nil {
			resp.Files = append(resp.Files, codegen.BytesFile("model_test.go", testSrc))
		}
	}

	return resp, nil
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
