package android

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Android (Jetpack Compose).
type Generator struct{}

func (g *Generator) Platform() string         { return "android" }
func (g *Generator) SupportedLangs() []string { return []string{"kotlin"} }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "kotlin" {
		return &codegen.Response{Error: fmt.Sprintf("android: unsupported lang %q", req.Lang.Lang())}, nil
	}

	cfg := Config{
		Package:      req.Options["package"],
		GenerateMain: req.Options["main"] == "true",
	}

	src, err := Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	resp := &codegen.Response{}

	if cfg.GenerateMain {
		pkgPath := pkgToPath(cfg.Package)
		resp.Files = append(resp.Files, &codegen.OutputFile{
			Name:    "app/src/main/java/" + pkgPath + "/MainScreen.kt",
			Content: src,
		})
		resp.Files = append(resp.Files, scaffoldFiles(cfg)...)
	} else {
		resp.Files = []*codegen.OutputFile{
			{Name: "MainScreen.kt", Content: src},
		}
	}

	return resp, nil
}
