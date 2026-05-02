package android

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
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

func (g *Generator) PlatformIdentifier() string { return "android" }
func (g *Generator) Description() string {
	return "Android app. Emits Jetpack Compose; can mix in Go via gomobile when --lang go is used."
}
func (g *Generator) SupportedLangs() []string            { return []string{"kotlin", "go"} }
func (g *Generator) PreviewCSS() string                  { return previewCSS }
func (g *Generator) Package() []*ast.Document            { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool {
	id := l.LanguageIdentifier()
	return id == "kotlin" || id == "go"
}
func (g *Generator) Capabilities() lower.Caps { return lower.Caps{} }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{}
	m, err := c.BuildRenderModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromRender(m, req)
}

// NewRenderCompiler returns a fresh per-request RenderModelEmitter.
func (g *Generator) NewRenderCompiler() codegen.RenderModelEmitter {
	return &compilation{}
}

// compilation holds per-request build state flowing between
// BuildRenderModel and EmitFromRender.
type compilation struct {
	ctx  *codegen.CodegenCtx
	cfg  Config
	lang string
}

var (
	_ codegen.RenderModelEmitter    = (*compilation)(nil)
	_ codegen.RenderCompilerFactory = (*Generator)(nil)
)

func (c *compilation) BuildRenderModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.RenderModel, error) {
	c.lang = req.Lang.LanguageIdentifier()
	if c.lang != "kotlin" && c.lang != "go" {
		return nil, fmt.Errorf("android: unsupported lang %q", c.lang)
	}
	c.ctx = codegen.NewCodegenCtx(req, "android")
	c.cfg = (&Generator{}).configFromRequest(req)
	if c.lang == "go" {
		c.cfg.GoLib = true
	}
	var stmts []ir.Stmt
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildRender(stmts), nil
}

func (c *compilation) EmitFromRender(_ *codegen.RenderModel, req *codegen.Request) (*codegen.Response, error) {
	switch c.lang {
	case "kotlin":
		return c.emitKotlin(req)
	case "go":
		return c.emitGo(req)
	default:
		return &codegen.Response{Error: fmt.Sprintf("android: unsupported lang %q", c.lang)}, nil
	}
}

func (g *Generator) configFromRequest(req *codegen.Request) Config {
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, req.Options); err != nil {
		panic(fmt.Errorf("android: configFromRequest: %w", err))
	}
	return cfg.withDefaults()
}

func (c *compilation) emitKotlin(req *codegen.Request) (*codegen.Response, error) {
	cfg := c.cfg
	ctx := c.ctx
	src, err := CompileIR(ctx, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("android", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	resp := &codegen.Response{}

	if !cfg.Main {
		resp.Files = []*codegen.OutputFile{
			codegen.BytesFile("MainScreen.kt", src),
		}
	} else if cfg.UseGradle() {
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
