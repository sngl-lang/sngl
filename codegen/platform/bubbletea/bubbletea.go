package bubbletea

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

//go:embed bubbletea.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, _ := parser.Parse("bubbletea.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Bubbletea.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string             { return "bubbletea" }
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol    { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }

func (g *Generator) PreviewCSS() string { return previewCSS }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return &codegen.Response{Error: fmt.Sprintf("bubbletea: unsupported lang %q", req.Lang.LanguageIdentifier())}, nil
	}

	cfg := Config{
		Package:      req.Options["package"],
		GenerateMain: req.Options["main"] == "true",
	}

	ctx := codegen.NewCodegenCtx(req, "bubbletea")
	src, err := CompileIR(ctx, cfg)
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

	// Generate test file if tests exist and not disabled
	if req.Pkg != nil && hasTestFuncs(req.Pkg) && req.Options["tests"] != "false" {
		testSrc, err := CompileTestsIR(ctx, cfg)
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
