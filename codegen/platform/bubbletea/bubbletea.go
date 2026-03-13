package bubbletea

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/compiler"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Bubbletea.
type Generator struct{}

func (g *Generator) Platform() string         { return "bubbletea" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "go" {
		return &codegen.Response{Error: fmt.Sprintf("bubbletea: unsupported lang %q", req.Lang.Lang())}, nil
	}

	cfg := compiler.Config{
		Package:      req.Options["package"],
		GenerateMain: req.Options["main"] == "true",
	}

	src, err := compiler.Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	return &codegen.Response{
		Files: []*codegen.OutputFile{
			{Name: "model.go", Content: src},
		},
	}, nil
}
