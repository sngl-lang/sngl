package fyne

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

//go:embed preview.css
var previewCSS string

//go:embed fyne.sngl
var pkgSource string

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Fyne.
type Generator struct{}

func (g *Generator) Platform() string         { return "fyne" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) PreviewCSS() string       { return previewCSS }
func (g *Generator) PkgSource() string        { return pkgSource }

// ResolveAPI makes any identifier valid as a Fyne widget/container.
func (g *Generator) ResolveAPI(name string) *codegen.NativeDecls {
	return &codegen.NativeDecls{}
}

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
