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
		AppName:      req.Options["appName"],
		GenerateMain: req.Options["main"] == "true",
		Gradle:       req.Options["gradle"] != "false",
		Icon:         req.Options["icon"],
		Color:        req.Options["color"],
		ProjectDir:   req.Options["projectDir"],
	}.withDefaults()

	src, err := Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	resp := &codegen.Response{}

	if !cfg.GenerateMain {
		resp.Files = []*codegen.OutputFile{
			{Name: "MainScreen.kt", Content: src},
		}
	} else if cfg.Gradle {
		// Full Gradle project scaffold
		pkgPath := pkgToPath(cfg.Package)
		resp.Files = append(resp.Files, &codegen.OutputFile{
			Name:    "app/src/main/java/" + pkgPath + "/MainScreen.kt",
			Content: src,
		})
		resp.Files = append(resp.Files, scaffoldFiles(cfg)...)
		// Icon/color resources go under app/src/main/res/
		if resFiles, err := resourceFiles(cfg); err == nil {
			for _, f := range resFiles {
				f.Name = "app/src/main/" + f.Name
			}
			resp.Files = append(resp.Files, resFiles...)
		}
	} else {
		// Gradle-free: just Kotlin sources + manifest + resources
		resp.Files = append(resp.Files, &codegen.OutputFile{
			Name: "MainScreen.kt", Content: src,
		})
		resp.Files = append(resp.Files, directBuildFiles(cfg)...)
		if resFiles, err := resourceFiles(cfg); err == nil {
			resp.Files = append(resp.Files, resFiles...)
		}
	}

	return resp, nil
}
