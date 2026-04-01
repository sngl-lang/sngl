package fyne

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Fyne.
type Generator struct{}

func (g *Generator) Platform() string         { return "fyne" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }

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

	return &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", src),
		},
	}, nil
}
