package html

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// generateRoutes runs the route-mode path: it produces static per-window HTML
// via the same compilation used for lang=none, then hands the routes and a
// RenderHTML callback to the language's HTTPCompiler. The language owns all
// framework specifics — mux syntax, server entry point, imports.
func (g *Generator) generateRoutes(req *codegen.Request) (*codegen.Response, error) {
	compiler, ok := req.Lang.(codegen.HTTPCompiler)
	if !ok {
		return nil, fmt.Errorf("html: language %q does not implement HTTPCompiler", req.Lang.LanguageIdentifier())
	}

	c := &compilation{}
	if _, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg)); err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	perWindow := make([][]byte, len(c.windows))
	for i, w := range c.windows {
		perWindow[i] = w.bytes
	}

	ctx := codegen.NewCodegenCtx(req, "html")
	windows := ctx.Windows()
	targets := buildNativeFuncMap(req.Pkg, req.Lang.LanguageIdentifier())
	routes := make([]codegen.HTTPRoute, 0, len(windows))
	for i, win := range windows {
		var hrefExpr, titleExpr ir.Expr
		if win.Window != nil {
			hrefExpr = win.Window.Href
			titleExpr = win.Window.Title
		}
		path, err := hrefToRoutePath(hrefExpr)
		if err != nil {
			return nil, fmt.Errorf("html: window %q href: %v", win.Name, err)
		}
		if path == "" {
			path = defaultRoutePath(win.Name, i)
		}
		title, _ := codegen.IRLiteralString(titleExpr)
		routes = append(routes, codegen.HTTPRoute{
			Name:      routeHandlerName(win.Name, path),
			Path:      path,
			Title:     title,
			Params:    extractRouteParams(path),
			WindowIdx: i,
			Actions:   collectActions(win, targets),
		})
	}

	var opts htmlConfig
	if err := codegen.ApplyOptions(&opts, req.Options); err != nil {
		return nil, fmt.Errorf("html: %w", err)
	}
	pkgName := opts.Package
	if pkgName == "" {
		if opts.Main {
			pkgName = "main"
		} else {
			pkgName = "ui"
		}
	}
	framework := opts.Framework
	if framework == "" {
		framework = "net/http"
	}

	httpReq := &codegen.HTTPRequest{
		Pkg:       req.Pkg,
		Package:   pkgName,
		Main:      opts.Main,
		Framework: framework,
		Routes:    routes,
		RenderHTML: func(idx int) string {
			if idx < 0 || idx >= len(perWindow) {
				return ""
			}
			return string(perWindow[idx])
		},
	}

	langFiles, err := compiler.CompileHTTP(httpReq)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	out := append([]*codegen.OutputFile{}, langFiles...)
	out = append(out, c.assetFiles...)
	return &codegen.Response{Files: out}, nil
}

func defaultRoutePath(winName string, idx int) string {
	if idx == 0 || winName == "" || winName == "main" || winName == "index" {
		return "/"
	}
	return "/" + winName
}

// hrefToRoutePath extracts an abstract route template from a window's href
// expression. Literal strings pass through verbatim; string interpolations
// (desugared by the checker into Binary concat chains) become "/{ident}"
// segments for identifier parts. Framework-specific mux syntax is the
// language's responsibility.
func hrefToRoutePath(e ir.Expr) (string, error) {
	if e == nil {
		return "", nil
	}
	if s, ok := codegen.IRLiteralString(e); ok {
		return s, nil
	}
	var b strings.Builder
	if err := walkHref(e, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func walkHref(e ir.Expr, b *strings.Builder) error {
	switch x := e.(type) {
	case *ir.Literal:
		s, ok := codegen.IRLiteralString(x)
		if !ok {
			return fmt.Errorf("non-string literal in href")
		}
		b.WriteString(s)
	case *ir.Ident:
		b.WriteByte('{')
		b.WriteString(x.Name)
		b.WriteByte('}')
	case *ir.Binary:
		if err := walkHref(x.Left, b); err != nil {
			return err
		}
		if err := walkHref(x.Right, b); err != nil {
			return err
		}
	case *ir.Select:
		b.WriteByte('{')
		b.WriteString(x.Field)
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported href expression %T", e)
	}
	return nil
}

// extractRouteParams finds {param} placeholders in a route path.
func extractRouteParams(path string) []string {
	var params []string
	for seg := range strings.SplitSeq(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && len(seg) > 2 {
			params = append(params, seg[1:len(seg)-1])
		}
	}
	return params
}

func routeExportName(s string) string {
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
		name.WriteString(routeExportName(p))
	}
	if name.String() == "" {
		return "handleIndex"
	}
	return "handle" + name.String()
}

// --- action classification ---

// buildNativeFuncMap returns the set of Func pointers sourced from imports
// whose scheme matches the target language identifier.
func buildNativeFuncMap(pkg *ir.Package, langID string) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	if pkg == nil || langID == "" {
		return out
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, _ := codegen.SplitScheme(imp.AST.Path)
		if scheme != langID {
			continue
		}
		for _, f := range imp.Native.Funcs {
			out[f] = true
		}
	}
	return out
}

// collectActions walks a window's visual tree for event handlers whose body
// transitively calls into the target language.
func collectActions(win *codegen.WindowCtx, targets map[*ir.Func]bool) []codegen.HTTPAction {
	if len(targets) == 0 {
		return nil
	}
	var actions []codegen.HTTPAction
	visited := map[*ir.Func]bool{}
	add := func(h *ir.EventHandler) {
		if h == nil || h.Func == nil {
			return
		}
		if !fnCallsTarget(h.Func, targets, visited) {
			return
		}
		actions = append(actions, codegen.HTTPAction{
			Name:      fmt.Sprintf("action%d", len(actions)),
			Mutations: h.Func.Block,
		})
	}
	for _, v := range win.Vars {
		for _, h := range v.Handlers {
			add(h)
		}
	}
	walkInstances(win.Body, func(n *ir.NodeInst) {
		for i := range n.Handlers {
			add(&n.Handlers[i])
		}
	})
	return actions
}

func walkInstances(stmts []ir.Stmt, fn func(*ir.NodeInst)) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			fn(n)
			walkInstances(n.Children, fn)
		case *ir.If:
			walkInstances(n.Body, fn)
			walkInstances(n.Else, fn)
		case *ir.For:
			walkInstances(n.Body, fn)
			walkInstances(n.Else, fn)
		case *ir.PlatformFilter:
			walkInstances(n.Body, fn)
		case *ir.SlotInst:
			walkInstances(n.Children, fn)
		case *ir.ErrorBoundary:
			walkInstances(n.Children, fn)
		case *ir.Window:
			walkInstances(n.Body, fn)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
			// Imperative stmts contain no NodeInst children.
		case *ir.ContextProvider:
			walkInstances(n.Children, fn)
		default:
			panic(fmt.Sprintf("html.walkInstances: unhandled ir.Stmt %T", n))
		}
	}
}

func fnCallsTarget(fn *ir.Func, targets, visited map[*ir.Func]bool) bool {
	if fn == nil || visited[fn] {
		return false
	}
	visited[fn] = true
	return stmtsCallTarget(fn.Block, targets, visited)
}

func stmtsCallTarget(stmts []ir.Stmt, targets, visited map[*ir.Func]bool) bool {
	for _, s := range stmts {
		if stmtCallsTarget(s, targets, visited) {
			return true
		}
	}
	return false
}

func stmtCallsTarget(s ir.Stmt, targets, visited map[*ir.Func]bool) bool {
	switch x := s.(type) {
	case *ir.Assign:
		return exprCallsTarget(x.Target, targets, visited) || exprCallsTarget(x.Value, targets, visited)
	case *ir.CallStmt:
		if x.Call != nil {
			return exprCallsTarget(x.Call, targets, visited)
		}
	case *ir.LocalVar:
		return exprCallsTarget(x.Init, targets, visited)
	case *ir.Return:
		return exprCallsTarget(x.Value, targets, visited)
	case *ir.If:
		return exprCallsTarget(x.Cond, targets, visited) ||
			stmtsCallTarget(x.Body, targets, visited) ||
			stmtsCallTarget(x.Else, targets, visited)
	case *ir.For:
		return exprCallsTarget(x.Iter, targets, visited) ||
			stmtsCallTarget(x.Body, targets, visited) ||
			stmtsCallTarget(x.Else, targets, visited)
	case *ir.PlatformFilter:
		return stmtsCallTarget(x.Body, targets, visited)
	case *ir.Emit:
		for _, a := range x.Args {
			if exprCallsTarget(a.Value, targets, visited) {
				return true
			}
		}
	case *ir.Toggle:
		return exprCallsTarget(x.Target, targets, visited)
	case *ir.NodeInst:
		for _, p := range x.Props {
			if exprCallsTarget(p.Value, targets, visited) {
				return true
			}
		}
		for _, h := range x.Handlers {
			if fnCallsTarget(h.Func, targets, visited) {
				return true
			}
		}
		return stmtsCallTarget(x.Children, targets, visited)
	case *ir.SlotInst:
		return stmtsCallTarget(x.Children, targets, visited)
	case *ir.ErrorBoundary:
		return stmtsCallTarget(x.Children, targets, visited)
	case *ir.Window:
		return stmtsCallTarget(x.Body, targets, visited)
	case *ir.ContextProvider:
		return stmtsCallTarget(x.Children, targets, visited)
	default:
		panic(fmt.Sprintf("html.stmtCallsTarget: unhandled ir.Stmt %T", s))
	}
	return false
}

func exprCallsTarget(e ir.Expr, targets, visited map[*ir.Func]bool) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil {
			if targets[x.Func] {
				return true
			}
			if fnCallsTarget(x.Func, targets, visited) {
				return true
			}
		}
		for _, a := range x.Args {
			if exprCallsTarget(a.Value, targets, visited) {
				return true
			}
		}
		return exprCallsTarget(x.Receiver, targets, visited) || exprCallsTarget(x.Callee, targets, visited)
	case *ir.Binary:
		return exprCallsTarget(x.Left, targets, visited) || exprCallsTarget(x.Right, targets, visited)
	case *ir.Unary:
		return exprCallsTarget(x.Operand, targets, visited)
	case *ir.Ternary:
		return exprCallsTarget(x.Cond, targets, visited) ||
			exprCallsTarget(x.Then, targets, visited) ||
			exprCallsTarget(x.Else, targets, visited)
	case *ir.Select:
		return exprCallsTarget(x.Operand, targets, visited)
	case *ir.Index:
		return exprCallsTarget(x.Operand, targets, visited) || exprCallsTarget(x.Idx, targets, visited)
	case *ir.Conversion:
		return exprCallsTarget(x.Operand, targets, visited)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if exprCallsTarget(f.Value, targets, visited) {
				return true
			}
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			if exprCallsTarget(el, targets, visited) {
				return true
			}
		}
	case *ir.Spread:
		return exprCallsTarget(x.Operand, targets, visited)
	case *ir.Lambda:
		if x.Func != nil {
			return fnCallsTarget(x.Func, targets, visited)
		}
	case *ir.Closure:
		if x.Func != nil {
			return fnCallsTarget(x.Func, targets, visited)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			if exprCallsTarget(kv.Key, targets, visited) || exprCallsTarget(kv.Value, targets, visited) {
				return true
			}
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Leaf — no nested call.
	default:
		panic(fmt.Sprintf("html.exprCallsTarget: unhandled ir.Expr %T", e))
	}
	return false
}
