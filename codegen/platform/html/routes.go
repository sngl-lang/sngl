package html

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// generateRoutes runs the route-mode path: it produces static per-window HTML
// via the same compilation used for lang=none, then hands the routes and a
// RenderHTML callback to the language's HTTPCompiler. The language owns all
// framework specifics — mux syntax, server entry point, imports.
func (g *Generator) generateRoutes(req *codegen.Request, sink codegen.Sink, frontendNatives map[nativeFuncKey]bool) error {
	compiler, ok := req.Lang.(codegen.HTTPCompiler)
	if !ok {
		return fmt.Errorf("html: language %q does not implement HTTPCompiler", req.Lang.LanguageIdentifier())
	}

	c := &compilation{ctx: codegen.NewCodegenCtx(req, "html"), frontendNatives: frontendNatives}
	if _, err := c.BuildMutationModel(req, c.ctx.Analysis); err != nil {
		return err
	}
	perWindow := make([][]byte, len(c.windows))
	for i, w := range c.windows {
		perWindow[i] = w.bytes
	}

	windows := c.routeWindows
	targets := buildNativeFuncMap(req.Pkg, req.Lang.LanguageIdentifier())
	routes := make([]codegen.HTTPRoute, 0, len(windows))
	var served []servedRoute
	for i, win := range windows {
		// A window holding a nav.stack is a route per page, named for the
		// page and served at its href; one holding none is served at the
		// root, or at its id beside a first.
		page := c.routePages[i]
		routeName, label := windowRouteName(win), routeWindowLabel(win.Name, i)
		var hrefExpr, titleExpr ir.Expr
		if win.Surface != nil {
			titleExpr = win.Surface.Prop(windowTitle)
		}
		if page != nil {
			hrefExpr = pageHref(page)
			routeName, label = page.ID, fmt.Sprintf("page %q", page.ID)
		}
		path, err := hrefToRoutePath(hrefExpr)
		if err != nil {
			return fmt.Errorf("html: %s href: %v", label, err)
		}
		if path == "" {
			path = defaultRoutePath(routeName, i)
		}
		for _, prev := range served {
			if !routesConflict(prev.path, path) {
				continue
			}
			if routeSegsEqual(prev.path, path) {
				return fmt.Errorf("html: %s and %s both serve %s; give one of them an href", prev.label, label, path)
			}
			return fmt.Errorf("html: %s serves %s and %s serves %s, which match some of the same paths with neither more specific; change one so a request names one of them", prev.label, prev.path, label, path)
		}
		served = append(served, servedRoute{path, label})
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
			Name:      routeHandlerName(routeName, path),
			Path:      path,
			Title:     title,
			Params:    extractRouteParams(path),
			DocIdx:    i,
			Surface:   win.Surface,
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
	return nil
}

// backendHandlerWindow scans the package's event handlers (node handlers and
// var handlers) for a Backend placement, and names the window holding the
// first: its Window's `#id`, "" for one written outside any. False if every
// handler is client-side. Used by the static-mode (non-HTTPCompiler) guard: a
// build with no server cannot run a backend handler.
func backendHandlerWindow(pkg *ir.Package) (string, bool) {
	if pkg == nil {
		return "", false
	}
	backend := func(h *ir.EventHandler) bool {
		return h != nil && h.Func != nil && handlerPlacement(pkg, h.Func) == Backend
	}
	for _, v := range pkg.Vars {
		if slices.ContainsFunc(v.Handlers, backend) {
			return "", true
		}
	}
	name, found := "", false
	var walk func(stmts []ir.Stmt, win string)
	walk = func(stmts []ir.Stmt, win string) {
		for _, s := range stmts {
			if found {
				return
			}
			switch n := s.(type) {
			case *ir.NodeInst:
				in := win
				if n.Component != nil && ir.IsSurface(n.Component) {
					in = n.ID
				}
				for i := range n.Handlers {
					if backend(&n.Handlers[i]) {
						name, found = in, true
						return
					}
				}
				walk(n.Children, in)
				for _, name := range ir.SlotNames(n.Slots) {
					if sc := n.Slots[name]; sc != nil {
						walk(sc.Body, in)
					}
				}
			case *ir.If:
				walk(n.Body, win)
				walk(n.Else, win)
			case *ir.For:
				walk(n.Body, win)
				walk(n.Else, win)
			case *ir.ErrorBoundary:
				walk(n.Children, win)
			case *ir.ContextProvider:
				walk(n.Children, win)
			}
		}
	}
	walk(pkg.Body, "")
	for _, c := range pkg.Components {
		walk(c.Body, "")
	}
	return name, found
}

// windowRouteName is the `#id` a route is named for: its document's Window's.
// A harness root has none: the component it isolated is not a window's name,
// and using it made a handler's name depend on whether a harness ran.
func windowRouteName(win *codegen.ViewCtx) string {
	return surfaceName(win.Surface)
}

type servedRoute struct{ path, label string }

func routeSegsEqual(p, q string) bool {
	return slices.Equal(routeSegs(p), routeSegs(q))
}

type routeSeg struct {
	lit         string
	wild, multi bool
}

func routeSegs(path string) []routeSeg {
	var out []routeSeg
	rest := strings.TrimPrefix(path, "/")
	for {
		seg, more, found := strings.Cut(rest, "/")
		switch {
		case seg == "" && !found:
			return append(out, routeSeg{multi: true})
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}"):
			return append(out, routeSeg{multi: true})
		case seg == "{$}":
			return append(out, routeSeg{lit: "/"})
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}"):
			out = append(out, routeSeg{wild: true})
		default:
			out = append(out, routeSeg{lit: seg})
		}
		if !found {
			return out
		}
		rest = more
	}
}

type routeRel int

const (
	relEqual routeRel = iota
	relDisjoint
	relOverlap
	relSpecific
	relGeneral
)

func combineRel(a, b routeRel) routeRel {
	switch a {
	case relEqual:
		return b
	case relDisjoint:
		return relDisjoint
	case relOverlap:
		if b == relDisjoint {
			return relDisjoint
		}
		return relOverlap
	}
	switch {
	case b == relEqual:
		return a
	case a == relSpecific && b == relGeneral, a == relGeneral && b == relSpecific:
		return relOverlap
	}
	return b
}

func compareRouteSeg(a, b routeSeg) routeRel {
	switch {
	case a.multi && b.multi, a.wild && b.wild:
		return relEqual
	case a.multi:
		return relGeneral
	case b.multi:
		return relSpecific
	case a.wild:
		if b.lit == "/" {
			return relDisjoint
		}
		return relGeneral
	case b.wild:
		if a.lit == "/" {
			return relDisjoint
		}
		return relSpecific
	case a.lit == b.lit:
		return relEqual
	}
	return relDisjoint
}

// routesConflict is net/http's rule for two patterns of one method: they
// conflict when some path matches both and neither is more specific, which
// ServeMux reports by panicking when the second is registered.
func routesConflict(p, q string) bool {
	a, b := routeSegs(p), routeSegs(q)
	lastMulti := func(s []routeSeg) bool { return len(s) > 0 && s[len(s)-1].multi }
	if len(a) != len(b) && !lastMulti(a) && !lastMulti(b) {
		return false
	}
	rel := relEqual
	for ; len(a) > 0 && len(b) > 0; a, b = a[1:], b[1:] {
		if rel = combineRel(rel, compareRouteSeg(a[0], b[0])); rel == relDisjoint {
			return false
		}
	}
	switch {
	case len(a) == 0 && len(b) == 0:
	case len(a) == 0 && lastMulti(routeSegs(p)):
		rel = combineRel(rel, relGeneral)
	case len(b) == 0 && lastMulti(routeSegs(q)):
		rel = combineRel(rel, relSpecific)
	default:
		return false
	}
	return rel == relEqual || rel == relOverlap
}

func routeWindowLabel(winName string, idx int) string {
	if winName == "" {
		return fmt.Sprintf("window %d", idx+1)
	}
	return fmt.Sprintf("window %q", winName)
}

func defaultRoutePath(winName string, idx int) string {
	if idx == 0 || winName == "" || winName == "index" {
		return "/"
	}
	return "/" + winName
}

// hrefToRoutePath extracts an abstract route template from a page's href
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
func routeParamField(sd *ir.StructDef, name string) *ir.StructField {
	if sd == nil {
		return nil
	}
	for _, f := range sd.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

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
	if windowName == "index" || (windowName == "" && path == "/") {
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
func collectActions(pkg *ir.Package, win *codegen.ViewCtx, targets map[*ir.Func]bool) ([]codegen.HTTPAction, map[*ir.EventHandler]int) {
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
	// Vars first, and the order is the contract: an action's index is the
	// POST switch case that runs it, so moving a handler in this walk moves
	// the case a form's hidden `_action` has to name.
	for _, v := range routeVars(pkg, win) {
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
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Imperative stmts contain no NodeInst children.
		default:
			panic(fmt.Sprintf("html.walkInstances: unhandled ir.Stmt %T", n))
		}
	}
}

// routeParamsJS is the script that reads a route's params out of the path the
// browser asked for: the struct's zero, with each field a `{name}` segment of
// path names parsed from that segment. Empty for a path that names none, which
// leaves the cell its zero.
//
// The server binds the same fields from the same path when it renders the
// route, but a client-only route ships one document for every request, so the
// script has to read the request again for itself.
func routeParamsJS(path string, t *ir.Type, zero string) string {
	holes := extractRouteParams(path)
	if len(holes) == 0 || t == nil {
		return ""
	}
	sd, _ := t.Decl.(*ir.StructDef)
	var re strings.Builder
	re.WriteString("^")
	group := map[string]int{}
	for seg := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
		re.WriteString("\\/")
		if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			group[seg[1:len(seg)-1]] = len(group) + 1
			re.WriteString("([^/]*)")
			continue
		}
		re.WriteString(regexp.QuoteMeta(seg))
	}
	re.WriteString("$")
	var fields []string
	for _, name := range holes {
		f := routeParamField(sd, name)
		if f == nil {
			continue
		}
		seg := fmt.Sprintf("decodeURIComponent(__m[%d])", group[name])
		switch f.Type.Kind {
		case ir.TypeInt:
			seg = "parseInt(" + seg + ", 10)"
		case ir.TypeFloat:
			seg = "parseFloat(" + seg + ")"
		case ir.TypeBool:
			seg = "(" + seg + " === \"true\")"
		}
		fields = append(fields, name+": "+seg)
	}
	return fmt.Sprintf("((__m) => __m ? { ...%s, %s } : %s)(location.pathname.match(/%s/))", zero, strings.Join(fields, ", "), zero, re.String())
}
