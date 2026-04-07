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
		if v := hrefToRoutePath(win.Props); v != "" {
			path = v
		} else if name != "" && name != "main" && name != "index" {
			path = "/" + name
		}
		handlerName := routeHandlerName(name, path)
		routes[i] = codegen.HTTPRoute{
			Name:      handlerName,
			Path:      path,
			Title:     staticPropString(win.Props, "title"),
			Params:    extractRouteParams(path),
			WindowIdx: i,
		}
	}

	// Analyze document for state partitioning and rendering.
	analysis := analyze(req.Doc)

	// Pre-render all routes to collect both HTML code and server actions.
	renderResults := make([]renderResult, len(routes))
	for i, route := range routes {
		win := windows[route.WindowIdx]
		renderResults[i] = renderWindowHTML(req.Doc, win, analysis, req.Lang, routes[i].Params)
		routes[i].Actions = renderResults[i].actions
	}

	// Build the HTTPRequest with a render callback.
	httpReq := &codegen.HTTPRequest{
		Doc:       req.Doc,
		Package:   cfg.Package,
		Main:      cfg.Main,
		Framework: cfg.Framework,
		Routes:    routes,
		RenderHTML: func(routeIdx int) string {
			return renderResults[routeIdx].code
		},
	}

	src, err := hc.CompileHTTP(httpReq)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("http", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
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

// hrefToRoutePath extracts a route path from the href prop.
// Handles both literal strings ("/" ) and interpolation expressions ("/{name}").
// Interpolated identifiers become {param} route parameters.
func hrefToRoutePath(props map[string]ast.Expr) string {
	v, ok := props["href"]
	if !ok {
		return ""
	}
	// Literal string: return as-is.
	if s, ok := v.Literal.(string); ok {
		return s
	}
	// InterpolationExpr: reconstruct path with {param} for ident parts.
	if v.SNGL != nil {
		if interp, ok := v.SNGL.(*ast.InterpolationExpr); ok {
			var path strings.Builder
			for _, part := range interp.Parts {
				switch p := part.(type) {
				case *ast.LiteralExpr:
					if s, ok := p.Value.(string); ok {
						path.WriteString(s)
					}
				case *ast.IdentExpr:
					path.WriteString("{" + p.Name + "}")
				}
			}
			return path.String()
		}
	}
	return ""
}

// extractRouteParams finds {param} placeholders in a route path.
func extractRouteParams(path string) []string {
	var params []string
	for seg := range strings.SplitSeq(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params = append(params, seg[1:len(seg)-1])
		}
	}
	return params
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

// routeHandlerName generates a valid Go function name for a route handler.
func routeHandlerName(windowName, path string) string {
	if windowName == "main" || windowName == "index" || (windowName == "" && path == "/") {
		return "handleIndex"
	}
	// Use window name if it's a clean identifier, otherwise derive from path.
	source := windowName
	if source == "" || strings.Contains(source, "/") || strings.Contains(source, "{") {
		source = path
	}
	clean := strings.TrimPrefix(source, "/")
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.' || r == '{' || r == '}'
	})
	var name strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		name.WriteString(exportName(p))
	}
	if name.String() == "" {
		return "handleIndex"
	}
	return "handle" + name.String()
}
