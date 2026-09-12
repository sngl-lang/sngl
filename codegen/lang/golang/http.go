package golang

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strconv"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CompileHTTP implements codegen.HTTPCompiler for Go. Produces a single
// server.go file containing: package clause, imports, Handler() returning an
// http.ServeMux, and — per route — a per-session State struct, a session
// loader, a renderRoute builder (filling the route's RouteRender holes from
// State), a GET handler (load session → render), and (when the route has
// backend actions) a POST handler implementing PRG (load session → run the
// action's logical mutations → save → 303 redirect). When req.Main is set it
// also emits a main() calling http.ListenAndServe.
//
// All server-side state/render/handler emission is driven through GoIRContext
// (httprender.go): the route GoIRContext sets ExprCtx.StateReceiver = "s" so a
// state read/write renders `s.<Field>` against the per-session State struct.
func (t *Translator) CompileHTTP(req *codegen.HTTPRequest) ([]*codegen.OutputFile, error) {
	if req.Framework != "" && req.Framework != "net/http" {
		return nil, fmt.Errorf("golang: framework %q not implemented", req.Framework)
	}

	pkgName := req.Package
	if pkgName == "" {
		if req.Main {
			pkgName = "main"
		} else {
			pkgName = "ui"
		}
	}

	// A single GoIRContext accumulates the native imports every route body
	// references via RequireImport; we read them back through gc.Imports() to
	// build the import block. Per-route contexts (newRouteGC) share this
	// import set so their writes propagate here.
	ctx := codegen.NewExprCtx(req.Pkg)
	ctx.ContextVar = "r.Context()"
	gc := NewIRContext(ctx)

	// Render the route bodies first so the GoIRContext accumulates its imports
	// before we emit the import block.
	hasActions := false
	for _, r := range req.Routes {
		if len(r.Actions) > 0 {
			hasActions = true
		}
	}

	var routeBody bytes.Buffer
	writeHandler(&routeBody, req)
	if hasActions {
		emitSessionStore(&routeBody, gc)
	}
	for _, r := range req.Routes {
		writeRouteHandler(&routeBody, req, r, gc)
	}

	var body bytes.Buffer
	fmt.Fprintf(&body, "package %s\n\n", pkgName)

	extraImports := gc.Imports()
	sort.Strings(extraImports)
	fmt.Fprintln(&body, "import (")
	fmt.Fprintln(&body, `	"net/http"`)
	for _, imp := range extraImports {
		if imp == "net/http" {
			continue
		}
		fmt.Fprintf(&body, "\t%q\n", imp)
	}
	fmt.Fprintln(&body, ")")
	fmt.Fprintln(&body)

	body.Write(routeBody.Bytes())
	if req.Main {
		fmt.Fprintln(&body)
		fmt.Fprintln(&body, `func main() {`)
		fmt.Fprintln(&body, `	http.ListenAndServe(":8080", Handler())`)
		fmt.Fprintln(&body, `}`)
	}

	// The route path assembles bytes by hand rather than through fileEmitter,
	// and each handler writes a trailing blank gofmt would otherwise strip.
	formatted, err := format.Source(body.Bytes())
	if err != nil {
		return nil, fmt.Errorf("golang: format server.go: %w\n%s", err, body.Bytes())
	}

	return []*codegen.OutputFile{codegen.BytesFile("server.go", formatted)}, nil
}

// writeRouteFuncs emits the user functions a route's markup or actions call,
// as methods on that route's State.
//
// Route mode has no Model — state is the per-request State struct — so a
// component-scoped func has nowhere else to live, and the call site rendered
// `m.keep(x)` against a receiver this file has no binding for and a function
// it never wrote. As State methods they dispatch through the same `s` the
// state fields do, and one named as a value (`xs.filter(keep)`) is a Go method
// value, which already carries its receiver and matches the callback type.
func writeRouteFuncs(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, shared *GoIRContext) {
	fns := routeEmittableFuncs(req.Pkg)
	if len(fns) == 0 {
		return
	}
	gc := newRouteGC(req, shared)
	gc.MethodRecvType = routeStateType(r)
	for _, fn := range fns {
		fnCopy := *fn
		fnCopy.Name = ExportName(fn.Name)
		fmt.Fprintln(b)
		for _, line := range gc.EmitFuncDef(&fnCopy) {
			fmt.Fprintln(b, line)
		}
	}
	fmt.Fprintln(b)
}

// routeEmittableFuncs is the component- and window-scoped funcs a route file
// carries, computeds included: route mode emits neither anywhere else, and a
// computed is reached by the same `s.Name()` call a plain func is.
func routeEmittableFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(fns []*ir.Func) {
		for _, fn := range fns {
			if fn == nil || seen[fn] || len(fn.Block) == 0 {
				continue
			}
			if fn.IsTest {
				continue
			}
			seen[fn] = true
			out = append(out, fn)
		}
	}
	if main := mainComponent(pkg); main != nil {
		add(main.Funcs)
	}
	for _, w := range pkg.Windows {
		if w != nil {
			add(w.Funcs)
		}
	}
	return out
}

func writeHandler(b *bytes.Buffer, req *codegen.HTTPRequest) {
	fmt.Fprintln(b, `// Handler returns an http.Handler wired with every route.`)
	fmt.Fprintln(b, `func Handler() http.Handler {`)
	fmt.Fprintln(b, `	mux := http.NewServeMux()`)
	for _, r := range req.Routes {
		fmt.Fprintf(b, "\tmux.HandleFunc(%q, %s)\n", "GET "+r.Path, r.Name)
		if len(r.Actions) > 0 {
			fmt.Fprintf(b, "\tmux.HandleFunc(%q, %sAction)\n", "POST "+r.Path, r.Name)
		}
	}
	fmt.Fprintln(b, `	return mux`)
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// writeRouteHandler emits the handlers for one route.
//
// A CLIENT-ONLY route (no backend actions) keeps the legacy baked static-page
// GET handler — byte-identical to pre-Phase-4 output — so no-backend pages are
// unchanged: the full client page (DOCTYPE/script) is shipped via RenderHTML.
//
// A BACKEND route (has server actions) gets the server-side render machinery:
// the per-session State struct + loader, a renderRoute builder, a GET handler
// (load session → render), and the POST/PRG action handler. All state idents
// resolve to `s.<Field>` via the route GoIRContext.
func writeRouteHandler(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, shared *GoIRContext) {
	if len(r.Actions) == 0 {
		writeClientRouteHandler(b, req, r)
		return
	}

	gc := newRouteGC(req, shared)

	emitState(b, r)
	writeRouteFuncs(b, req, r, shared)
	renderFn := emitRenderRoute(b, req, r, gc)

	loader := "load" + ExportName(r.Name) + "State"

	// GET handler: load session (locking it across the render) → write page.
	fmt.Fprintf(b, "func %s(w http.ResponseWriter, r *http.Request) {\n", r.Name)
	fmt.Fprintln(b, `	w.Header().Set("Content-Type", "text/html; charset=utf-8")`)
	fmt.Fprintln(b, "\tid, ok := snglSessionID(w, r)")
	fmt.Fprintln(b, "\tif !ok {")
	fmt.Fprintln(b, "\t\treturn")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintf(b, "\tsess, %s := %s(id)\n", routeStateReceiver, loader)
	fmt.Fprintln(b, "\tdefer sess.mu.Unlock()")
	fmt.Fprintf(b, "\tw.Write([]byte(%s(%s)))\n", renderFn, routeStateReceiver)
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)

	// POST handler (PRG): load session (locking it across the mutation) →
	// dispatch on _action → run the action's logical mutations against s →
	// 303 redirect.
	fmt.Fprintf(b, "func %sAction(w http.ResponseWriter, r *http.Request) {\n", r.Name)
	fmt.Fprintln(b, "\tid, ok := snglSessionID(w, r)")
	fmt.Fprintln(b, "\tif !ok {")
	fmt.Fprintln(b, "\t\treturn")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintf(b, "\tsess, %s := %s(id)\n", routeStateReceiver, loader)
	fmt.Fprintln(b, "\tdefer sess.mu.Unlock()")
	fmt.Fprintln(b, `	switch r.FormValue("_action") {`)
	for i, act := range r.Actions {
		fmt.Fprintf(b, "\tcase %q:\n", fmt.Sprintf("%d", i))
		for _, s := range act.LogicalMutations {
			for _, line := range gc.EvalStmt(s) {
				fmt.Fprintf(b, "\t\t%s\n", line)
			}
		}
	}
	fmt.Fprintln(b, `	}`)
	fmt.Fprintf(b, "\thttp.Redirect(w, r, %q, http.StatusSeeOther)\n", r.Path)
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// writeClientRouteHandler emits the legacy baked static-page GET handler for a
// client-only route (no backend actions). The full client page (the same string
// the static-site path produces) is written verbatim, preserving byte-identical
// output for pages with no server-side placement.
func writeClientRouteHandler(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute) {
	var page string
	if req.RenderHTML != nil {
		page = req.RenderHTML(r.WindowIdx)
	}
	fmt.Fprintf(b, "func %s(w http.ResponseWriter, r *http.Request) {\n", r.Name)
	fmt.Fprintln(b, `	w.Header().Set("Content-Type", "text/html; charset=utf-8")`)
	fmt.Fprintf(b, "\tw.Write([]byte(%s))\n", strconv.Quote(page))
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}
