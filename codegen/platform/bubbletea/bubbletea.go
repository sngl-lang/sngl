package bubbletea

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

//go:embed preview.css
var previewCSS string

//go:embed bubbletea.sngl
var pkgSource string

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Bubbletea.
type Generator struct{}

func (g *Generator) Platform() string         { return "bubbletea" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) PkgSource() string        { return pkgSource }

func (g *Generator) PreviewCSS() string { return previewCSS }

// ResolveAPI makes any identifier valid as a terminal component.
// This allows bubbletea.TextInput, bubbletea.Spinner, etc.
func (g *Generator) ResolveAPI(name string) *ast.NativeDecls {
	return &ast.NativeDecls{}
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

	if h := codegen.Header("bubbletea", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	resp := &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", src),
		},
	}

	// Generate test file if tests exist and not disabled
	if len(req.Doc.TestFuncs()) > 0 && req.Options["tests"] != "false" {
		testSrc, err := CompileTests(req.Doc, cfg)
		if err == nil && testSrc != nil {
			resp.Files = append(resp.Files, codegen.BytesFile("model_test.go", testSrc))
		}
	}

	return resp, nil
}
