package fyne

import (
	_ "embed"
	"fmt"
	"go/format"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

//go:embed fyne.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, _ := parser.Parse("fyne.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Fyne.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "fyne" }
func (g *Generator) Description() string {
	return "Cross-platform desktop GUI, written in Go using Fyne."
}
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) PreviewCSS() string                     { return previewCSS }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol    { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{}
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromMutation(m, req)
}

// NewMutationCompiler returns a fresh per-request MutationModelEmitter.
func (g *Generator) NewMutationCompiler() codegen.MutationModelEmitter {
	return &compilation{}
}

// compilation holds per-request build state flowing between
// BuildMutationModel and EmitFromMutation.
type compilation struct {
	ctx  *codegen.CodegenCtx
	info *irAnalysis
	cfg  Config
}

var (
	_ codegen.MutationModelEmitter    = (*compilation)(nil)
	_ codegen.MutationCompilerFactory = (*Generator)(nil)
)

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("fyne: unsupported lang %q", req.Lang.LanguageIdentifier())
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("fyne: %w", err)
	}
	c.cfg = c.cfg.withDefaults()
	c.ctx = codegen.NewCodegenCtx(req, "fyne")
	c.info = analyzeIR(c.ctx)

	var stmts []ir.Stmt
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildMutation(stmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request) (*codegen.Response, error) {
	src := emitIR(c.info, c.ctx, c.cfg)
	formatted, err := format.Source(src)
	if err != nil {
		return &codegen.Response{Error: fmt.Sprintf("generated code formatting error: %v\n%s", err, src)}, nil
	}
	if h := codegen.Header("fyne", req.Source, "// ", ""); h != "" {
		formatted = append([]byte(h), formatted...)
	}
	return &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", formatted),
		},
	}, nil
}
