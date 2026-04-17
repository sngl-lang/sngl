package android

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

//go:embed android.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, _ := parser.Parse("android.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Android (Jetpack Compose).
type Generator struct{}

func (g *Generator) Platform() string                    { return "android" }
func (g *Generator) Identifier() string                  { return "android" }
func (g *Generator) SupportedLangs() []string            { return []string{"kotlin", "go"} }
func (g *Generator) PreviewCSS() string                  { return previewCSS }
func (g *Generator) Package() []*ast.Document            { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool {
	id := l.Identifier()
	return id == "kotlin" || id == "go"
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	switch req.Lang.Lang() {
	case "kotlin":
		return g.generateKotlin(req)
	case "go":
		return g.generateGo(req)
	default:
		return &codegen.Response{Error: fmt.Sprintf("android: unsupported lang %q", req.Lang.Lang())}, nil
	}
}

func (g *Generator) configFromRequest(req *codegen.Request) Config {
	appName := req.Opts.Name
	if v := req.Options["appName"]; v != "" {
		appName = v
	}
	icon := req.Opts.Icon
	if v := req.Options["icon"]; v != "" {
		icon = v
	}
	return Config{
		Package:      req.Options["package"],
		AppName:      appName,
		GenerateMain: req.Options["main"] == "true",
		Gradle:       req.Options["gradle"] != "false",
		Icon:         icon,
		Color:        req.Options["color"],
		ProjectDir:   req.Options["projectDir"],
	}.withDefaults()
}

func (g *Generator) generateKotlin(req *codegen.Request) (*codegen.Response, error) {
	cfg := g.configFromRequest(req)

	src, err := Compile(req.Doc, req.Pkg, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("android", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	resp := &codegen.Response{}

	if !cfg.GenerateMain {
		resp.Files = []*codegen.OutputFile{
			codegen.BytesFile("MainScreen.kt", src),
		}
	} else if cfg.Gradle {
		pkgPath := pkgToPath(cfg.Package)
		resp.Files = append(resp.Files, codegen.BytesFile(
			"app/src/main/java/"+pkgPath+"/MainScreen.kt", src,
		))
		resp.Files = append(resp.Files, scaffoldFiles(cfg)...)
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				f.Name = "app/src/main/" + f.Name
			}
			resp.Files = append(resp.Files, iconRes...)
		}
	} else {
		resp.Files = append(resp.Files, codegen.BytesFile("MainScreen.kt", src))
		resp.Files = append(resp.Files, directBuildFiles(cfg)...)
		if iconRes, err := iconFiles(cfg); err == nil {
			resp.Files = append(resp.Files, iconRes...)
		}
	}

	return resp, nil
}
