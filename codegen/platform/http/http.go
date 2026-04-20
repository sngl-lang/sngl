package http

import (
	_ "embed"
	"errors"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// errNotPortedV2 is returned by any functionality not yet ported to the v2 AST.
var errNotPortedV2 = errors.New("http platform not yet ported to v2 AST")

//go:embed http.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, _ := parser.Parse("http.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for server-rendered HTTP output.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string             { return "http" }
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol    { return nil }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }

// Generate is not yet ported to the v2 AST.
// TODO: Port to v2 AST — requires Doc.Stmts iteration instead of Doc.App/Data/NativeImports.
func (g *Generator) Generate(_ *codegen.Request) (*codegen.Response, error) {
	return nil, errNotPortedV2
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
	if s, ok := codegen.ExprLiteralString(v); ok {
		return s
	}
	// InterpolationExpr: reconstruct path with {param} for ident parts.
	if interp, ok := v.(*ast.InterpolationExpr); ok {
		var path strings.Builder
		for _, part := range interp.Parts {
			switch p := part.(type) {
			case *ast.LiteralExpr:
				if s, ok := codegen.ExprLiteralString(p); ok {
					path.WriteString(s)
				}
			case *ast.IdentExpr:
				path.WriteString("{" + p.Name + "}")
			}
		}
		return path.String()
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
	if s, ok := codegen.ExprLiteralString(v); ok {
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
