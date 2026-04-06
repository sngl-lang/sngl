package http

import (
	_ "embed"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

//go:embed http.sngl
var pkgSource string

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for server-rendered HTTP output.
type Generator struct{}

func (g *Generator) Platform() string         { return "http" }
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) PkgSource() string        { return pkgSource }

// ResolveAPI makes any identifier valid as an HTML element.
func (g *Generator) ResolveAPI(name string) *ast.NativeDecls {
	return &ast.NativeDecls{}
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	hc, ok := req.Lang.(codegen.HTTPCompiler)
	if !ok {
		return &codegen.Response{Error: fmt.Sprintf("http: lang %q does not implement HTTPCompiler", req.Lang.Lang())}, nil
	}

	cfg := Config{
		Package:   req.Options["package"],
		Main:      req.Options["main"] == "true",
		Framework: req.Options["framework"],
	}
	if cfg.Package == "" {
		if cfg.Main {
			cfg.Package = "main"
		} else {
			cfg.Package = "ui"
		}
	}
	if cfg.Framework == "" {
		cfg.Framework = "net/http"
	}

	// Build routes from windows.
	windows := req.Doc.App.EffectiveWindows()
	routes := make([]codegen.HTTPRoute, len(windows))
	for i, win := range windows {
		path := "/"
		name := win.Name
		if v := staticPropString(win.Props, "href"); v != "" {
			path = v
		} else if name != "" && name != "main" && name != "index" {
			path = "/" + name
		}
		handlerName := "handle" + exportName(name)
		if name == "" || name == "main" || name == "index" {
			handlerName = "handleIndex"
		}
		routes[i] = codegen.HTTPRoute{
			Name:      handlerName,
			Path:      path,
			Title:     staticPropString(win.Props, "title"),
			WindowIdx: i,
		}
	}

	// Analyze document for state partitioning and rendering.
	analysis := analyze(req.Doc)

	// Build the HTTPRequest with a render callback.
	httpReq := &codegen.HTTPRequest{
		Doc:       req.Doc,
		Package:   cfg.Package,
		Main:      cfg.Main,
		Framework: cfg.Framework,
		Routes:    routes,
		RenderHTML: func(routeIdx int) string {
			win := windows[routeIdx]
			return renderWindowHTML(req.Doc, win, analysis, req.Lang)
		},
	}

	src, err := hc.CompileHTTP(httpReq)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	resp := &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("routes.go", src),
		},
	}
	return resp, nil
}

// Config controls code generation options.
type Config struct {
	Package   string
	Main      bool
	Framework string
}

func staticPropString(props map[string]ast.Expr, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	if s, ok := v.Literal.(string); ok {
		return s
	}
	return ""
}

func exportName(s string) string {
	if s == "" {
		return "Index"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
