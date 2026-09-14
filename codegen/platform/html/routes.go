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
func (g *Generator) generateRoutes(req *codegen.Request, sink codegen.Sink) error {
	compiler, ok := req.Lang.(codegen.HTTPCompiler)
	if !ok {
		return fmt.Errorf("html: language %q does not implement HTTPCompiler", req.Lang.LanguageIdentifier())
	}

	c := &compilation{}
	if _, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg)); err != nil {
		return err
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
			hrefExpr = win.Window.Prop(ir.WindowHref)
			titleExpr = win.Window.Prop(ir.WindowTitle)
		}
		path, err := hrefToRoutePath(hrefExpr)
		if err != nil {
			return fmt.Errorf("html: window %q href: %v", win.Name, err)
		}
		if path == "" {
			path = defaultRoutePath(win.Name, i)
		}
		title, _ := codegen.IRLiteralString(titleExpr)
		// Single source of truth for action indexing: collectActions enumerates
		// every backend handler in a stable order and returns both the action
		// list (whose slice index is the POST switch case) and a map from each
		// handler to that same index. buildRenderModel uses the map so a form's
		// hidden _action value is guaranteed to match the switch case that runs
		// the handler's mutations — the two no longer count independently.
		// Per route rather than once: a route's State carries the vars its own
		// window owns, which is where a root component's state now lives.
		stateVars := routeStateVars(req.Pkg, win)
		actions, actionIdx := collectActions(req.Pkg, win, targets)
		if len(actions) > 0 {
			if fn := reactiveSlotFunc(req.Pkg); fn != nil {
				return fmt.Errorf("html: route %s has a server-side action, so window %q is rendered per request — but a conditional or loop in it re-renders on the client (%s), and route mode cannot emit both for one window; make the condition depend on the request (a route parameter) rather than on mutable state", path, win.Name, fn.Name)
			}
		}
		render, err := buildRenderModel(req.Pkg, win, actionIdx)
		if err != nil {
			return fmt.Errorf("html: route %s: %w", path, err)
		}
		routes = append(routes, codegen.HTTPRoute{
			Name:      routeHandlerName(win.Name, path),
			Path:      path,
			Title:     title,
			Params:    extractRouteParams(path),
			WindowIdx: i,
			Window:    win.Window,
			Actions:   actions,
			Render:    render,
			StateVars: stateVars,
		})
	}

	var opts htmlConfig
	if err := codegen.ApplyOptions(&opts, req.Options); err != nil {
		return fmt.Errorf("html: %w", err)
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
		return err
	}
	for _, f := range langFiles {
		w, werr := sink.Create(f.Name)
		if werr != nil {
			return werr
		}
		if _, werr = f.WriteTo(w); werr != nil {
			w.Close()
			return werr
		}
		if werr = w.Close(); werr != nil {
			return werr
		}
	}
	for _, f := range c.assetFiles {
		if err := writeSinkFile(sink, f.name, f.bytes); err != nil {
			return err
		}
	}
	return nil
}

// reactiveSlotFunc returns the first __renderSlotN the reactivity lowering
// synthesized anywhere in the package, or nil when it synthesized none.
//
// Package-wide because that is the set a route emits its funcs from: one such
// func anywhere reaches the language translator carrying `lower.CreateNode`.
func reactiveSlotFunc(pkg *ir.Package) *ir.Func {
	if pkg == nil {
		return nil
	}
	for _, fn := range pkg.Funcs {
		if fn != nil && fn.SlotRender {
			return fn
		}
	}
	for _, c := range pkg.Components {
		for _, fn := range c.Funcs {
			if fn != nil && fn.SlotRender {
				return fn
			}
		}
	}
	for _, w := range pkg.Windows {
		for _, fn := range w.Funcs {
			if fn != nil && fn.SlotRender {
				return fn
			}
		}
	}
	return nil
}

// backendHandlerWindow scans every window's event handlers (node handlers and
// var handlers) for a Backend placement. It returns the name of the first
// window carrying a server-side handler, or false if all handlers are
// client-side. Used by the static-mode (non-HTTPCompiler) guard: a build with
// no server cannot run a backend handler.
func backendHandlerWindow(pkg *ir.Package, windows []*codegen.WindowCtx) (string, bool) {
	if pkg == nil {
		return "", false
	}
	for _, win := range windows {
		backend := false
		check := func(h *ir.EventHandler) {
			if h != nil && h.Func != nil && handlerPlacement(pkg, h.Func) == Backend {
				backend = true
			}
		}
		for _, v := range win.Vars {
			for _, h := range v.Handlers {
				check(h)
			}
		}
		walkInstances(win.Body, func(n *ir.NodeInst) {
			for i := range n.Handlers {
				check(&n.Handlers[i])
			}
		})
		if backend {
			return win.Name, true
		}
	}
	return "", false
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

// collectActions walks a window's visual tree for event handlers placed on the
// backend (per handlerPlacement: a transitive non-js: import). Each such
// handler becomes a server-state form action.
//
// Mutations carries the full handler block unchanged; LogicalMutations carries
// the same body with visual/DOM-patch statements removed, which is what the
// golang consumer reads.
//
// It also returns an index map keyed by each backend *ir.EventHandler to its
// position in the returned slice. This is the SINGLE source of truth for action
// indexing: the slice index is both the action's identity and the POST switch
// case, and buildRenderModel consumes the same map to mint a form's hidden
// _action value — so a form and the switch case that handles it can never
// disagree (even with multiple backend handlers per node, or backend var
// handlers that emit no form).
func collectActions(pkg *ir.Package, win *codegen.WindowCtx, targets map[*ir.Func]bool) ([]codegen.HTTPAction, map[*ir.EventHandler]int) {
	if len(targets) == 0 {
		return nil, nil
	}
	var actions []codegen.HTTPAction
	idx := map[*ir.EventHandler]int{}
	add := func(h *ir.EventHandler) {
		if h == nil || h.Func == nil {
			return
		}
		if handlerPlacement(pkg, h.Func) != Backend {
			return
		}
		idx[h] = len(actions)
		actions = append(actions, codegen.HTTPAction{
			Name:             fmt.Sprintf("action%d", len(actions)),
			Mutations:        h.Func.Block,
			LogicalMutations: logicalMutations(h.Func.Block),
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
	return actions, idx
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
		case *ir.SlotInst:
			walkInstances(n.Children, fn)
		case *ir.ErrorBoundary:
			walkInstances(n.Children, fn)
		case *ir.Window:
			walkInstances(n.Body, fn)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Imperative stmts contain no NodeInst children.
		case *ir.ContextProvider:
			walkInstances(n.Children, fn)
		default:
			panic(fmt.Sprintf("html.walkInstances: unhandled ir.Stmt %T", n))
		}
	}
}
