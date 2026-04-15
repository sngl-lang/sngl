package fyne

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

func (g *Generator) Platform() string                       { return "fyne" }
func (g *Generator) Identifier() string                     { return "fyne" }
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) PreviewCSS() string                     { return previewCSS }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol    { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.Identifier() == "go" }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "go" {
		return &codegen.Response{Error: fmt.Sprintf("fyne: unsupported lang %q", req.Lang.Lang())}, nil
	}

	cfg := Config{
		Package:      req.Options["package"],
		GenerateMain: req.Options["main"] == "true",
		AppName:      req.Opts.Name,
	}

	src, err := Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("fyne", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	return &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", src),
		},
	}, nil
}
