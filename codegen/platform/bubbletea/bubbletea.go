package bubbletea

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Bubbletea.
type Generator struct{}

func (g *Generator) Platform() string         { return "bubbletea" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }

func (g *Generator) PreviewCSS() string {
	return `body { background: #1e1e2e; color: #cdd6f4; font-family: monospace; padding: 16px; }
button { background: none; border: 1px solid #585b70; color: #cdd6f4; padding: 4px 12px; font-family: monospace; cursor: pointer; }
input { background: #313244; border: 1px solid #585b70; color: #cdd6f4; padding: 4px 8px; font-family: monospace; }
label { font-family: monospace; }`
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "go" {
		return &codegen.Response{Error: fmt.Sprintf("bubbletea: unsupported lang %q", req.Lang.Lang())}, nil
	}

	cfg := Config{
		Package:      req.Options["package"],
		GenerateMain: req.Options["main"] == "true",
	}

	src, err := Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	resp := &codegen.Response{
		Files: []*codegen.OutputFile{
			{Name: "model.go", Content: src},
		},
	}

	// Generate test file if tests exist and not disabled
	if len(req.Doc.Tests) > 0 && req.Options["tests"] != "false" {
		testSrc, err := CompileTests(req.Doc, cfg)
		if err == nil && testSrc != nil {
			resp.Files = append(resp.Files, &codegen.OutputFile{
				Name:    "model_test.go",
				Content: testSrc,
			})
		}
	}

	return resp, nil
}
