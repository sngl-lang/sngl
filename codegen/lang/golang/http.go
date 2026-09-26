package golang

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/names"
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
	ctx.ContextVar = routeRequestVar + ".Context()"
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
	writeRouteParamTypes(&routeBody, req)
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

// writeRouteParamTypes declares the structs a route's path parameters arrive
// in. Route mode emits no user type declarations of its own -- a Go platform's
// generator is what writes those, and there is no platform here -- so the one
// type the handlers themselves name has to be written where they are.
//
// Deduplicated by declaration: two routes may take the same params struct, and
// two structs of one name are two declarations the checker has already
// renamed apart.
func writeRouteParamTypes(b *bytes.Buffer, req *codegen.HTTPRequest) {
	seen := map[*ir.StructDef]bool{}
	for _, r := range req.Routes {
		pv := routeParamsVar(r)
		if pv == nil {
			continue
		}
		sd, _ := pv.Type.Decl.(*ir.StructDef)
		if sd == nil || seen[sd] {
			continue
		}
		seen[sd] = true
		fmt.Fprintf(b, "// %s is what route %q takes from the request.\n", IRTypeToGo(pv.Type), r.Path)
		fmt.Fprintf(b, "type %s struct {\n", IRTypeToGo(pv.Type))
		for _, f := range sd.Fields {
			fmt.Fprintf(b, "\t%s %s\n", ExportName(f.Name), IRTypeToGo(f.Type))
		}
		fmt.Fprintln(b, "}")
		fmt.Fprintln(b)
	}
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
func writeRouteFuncs(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, shared *GoIRContext, loc routeLocals) {
	fns := routeEmittableFuncs(req.Pkg)
	if len(fns) == 0 {
		return
	}
	gc := newRouteGC(req, r, shared, loc)
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

// routeEmittableFuncs is every user func a route file carries, computeds
// included: route mode emits funcs nowhere else, and a computed is reached by
// the same `s.Name()` call a plain func is.
//
// Every one of them, because that is what the call site already assumes:
// evalIdent's NameFunc arm spells any user func as `s.Name(…)` in route mode,
// there being one namespace here and it is the State's. Asking the main
// component alone was a proxy for "component- and window-scoped", and it
// emptied out when a root component's declarations went to the package
// (#215's hoist) -- `keep` and `biggest` were called as `s.Keep`/`s.Biggest`
// against a file that declared neither.
//
// A host identifier is not a user func and is never emitted; a method on a
// user type is not one either, and route mode has no emitter for those.
func routeEmittableFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(fns []*ir.Func) {
		for _, fn := range fns {
			if fn == nil || seen[fn] || len(fn.Block) == 0 {
				continue
			}
			if fn.IsTest || fn.Receiver != "" {
				continue
			}
			if fn.Foreign.Name != "" && !fn.Foreign.Marked {
				continue
			}
			seen[fn] = true
			out = append(out, fn)
		}
	}
	add(pkg.Funcs)
	if root := pkg.RootDecl(); root != nil {
		add(root.Funcs)
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

	loc := newRouteLocals(r)
	gc := newRouteGC(req, r, shared, loc)

	emitState(b, r)
	writeRouteFuncs(b, req, r, shared, loc)
	renderFn := emitRenderRoute(b, req, r, gc, loc)

	loader := "load" + ExportName(r.Name) + "State"

	// GET handler: load session (locking it across the render), write page.
	fmt.Fprintf(b, "func %s(%s http.ResponseWriter, %s *http.Request) {\n", r.Name, loc.writer, loc.request)
	fmt.Fprintf(b, "\t%s.Header().Set(\"Content-Type\", \"text/html; charset=utf-8\")\n", loc.writer)
	fmt.Fprintf(b, "\t%s, %s := snglSessionID(%s, %s)\n", loc.sessionID, loc.sessionOK, loc.writer, loc.request)
	fmt.Fprintf(b, "\tif !%s {\n", loc.sessionOK)
	fmt.Fprintln(b, "\t\treturn")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintf(b, "\t%s, %s := %s(%s)\n", loc.session, loc.state, loader, loc.sessionID)
	fmt.Fprintf(b, "\tdefer %s.mu.Unlock()\n", loc.session)
	writeRouteParamBindings(b, r, gc, loc)
	fmt.Fprintf(b, "\t%s.Write([]byte(%s(%s)))\n", loc.writer, renderFn,
		strings.Join(append([]string{loc.state}, routeRenderArgs(r)...), ", "))
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)

	// POST handler (PRG): load session (locking it across the mutation), then
	// dispatch on _action, run the action's logical mutations against s, and
	// 303 redirect.
	fmt.Fprintf(b, "func %sAction(%s http.ResponseWriter, %s *http.Request) {\n", r.Name, loc.writer, loc.request)
	fmt.Fprintf(b, "\t%s, %s := snglSessionID(%s, %s)\n", loc.sessionID, loc.sessionOK, loc.writer, loc.request)
	fmt.Fprintf(b, "\tif !%s {\n", loc.sessionOK)
	fmt.Fprintln(b, "\t\treturn")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintf(b, "\t%s, %s := %s(%s)\n", loc.session, loc.state, loader, loc.sessionID)
	fmt.Fprintf(b, "\tdefer %s.mu.Unlock()\n", loc.session)
	writeRouteParamBindings(b, r, gc, loc)
	fmt.Fprintf(b, "\tswitch %s.FormValue(\"_action\") {\n", loc.request)
	for i, act := range r.Actions {
		fmt.Fprintf(b, "\tcase %q:\n", fmt.Sprintf("%d", i))
		for _, s := range act.LogicalMutations {
			for _, line := range gc.EvalStmt(s) {
				fmt.Fprintf(b, "\t\t%s\n", line)
			}
		}
	}
	fmt.Fprintln(b, `	}`)
	fmt.Fprintf(b, "\thttp.Redirect(%s, %s, %s, http.StatusSeeOther)\n",
		loc.writer, loc.request, routePathExpr(r, gc))
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// routeRenderArgs is what the render function takes beyond the state
// receiver: the parameter binding, where the route has one.
func routeRenderArgs(r codegen.HTTPRoute) []string {
	if p := routeParamsVar(r); p != nil {
		return []string{p.Name}
	}
	return nil
}

// The spellings the handler and render emitters prefer; routeLocals decides
// what they actually get.
const (
	routeWriterVar    = "__w"
	routeRequestVar   = "__r"
	routeSessionIDVar = "id"
	routeSessionOKVar = "ok"
	routeSessionVar   = "sess"
	routeStateVar     = "s"
	routeBuilderVar   = "__b"
)

// routeLocals is what one route's GET handler, POST handler and render
// function call the bindings they declare for themselves.
//
// A route's parameter binding reaches the generated code verbatim -- the IR
// that reads it renders the bare name the window's slot declared, with no
// substitution to rename it through -- so the program's name is the fixed one
// here and every name below bends around it.
type routeLocals struct {
	writer, request       string
	sessionID, sessionOK  string
	session, state, build string
}

func newRouteLocals(r codegen.HTTPRoute) routeLocals {
	var taken []string
	if p := routeParamsVar(r); p != nil {
		taken = append(taken, p.Name)
	}
	reg := names.New(taken...)
	return routeLocals{
		writer:    reg.Unique(routeWriterVar),
		request:   reg.Unique(routeRequestVar),
		sessionID: reg.Unique(routeSessionIDVar),
		sessionOK: reg.Unique(routeSessionOKVar),
		session:   reg.Unique(routeSessionVar),
		state:     reg.Unique(routeStateVar),
		build:     reg.Unique(routeBuilderVar),
	}
}

// writeRouteParamBindings binds the route's path parameters from the request,
// as the one struct value the window's slot hands its body. Per-request input
// rather than per-session state, which is why it is a local here and not a
// field on State.
//
// Field by field rather than as a composite literal: a path segment is text,
// and a field the struct typed as something else needs a conversion statement
// that no literal has room for. A field the path does not name keeps the
// struct's zero, which is what says the path is one source of a request's
// values and not the only one.
func writeRouteParamBindings(b *bytes.Buffer, r codegen.HTTPRoute, gc *GoIRContext, loc routeLocals) {
	pv := routeParamsVar(r)
	if pv == nil {
		return
	}
	fmt.Fprintf(b, "\tvar %s %s\n", pv.Name, IRTypeToGo(pv.Type))
	sd, _ := pv.Type.Decl.(*ir.StructDef)
	for _, name := range r.Params {
		field := routeParamField(sd, name)
		if field == nil {
			continue
		}
		read := fmt.Sprintf("%s.PathValue(%q)", loc.request, name)
		lhs := pv.Name + "." + ExportName(name)
		switch IRTypeToGo(field.Type) {
		case "string":
			fmt.Fprintf(b, "\t%s = %s\n", lhs, read)
		case "int":
			gc.RequireImport("strconv")
			fmt.Fprintf(b, "\tif __v, __err := strconv.Atoi(%s); __err == nil {\n\t\t%s = __v\n\t}\n", read, lhs)
		case "float64":
			gc.RequireImport("strconv")
			fmt.Fprintf(b, "\tif __v, __err := strconv.ParseFloat(%s, 64); __err == nil {\n\t\t%s = __v\n\t}\n", read, lhs)
		case "bool":
			gc.RequireImport("strconv")
			fmt.Fprintf(b, "\tif __v, __err := strconv.ParseBool(%s); __err == nil {\n\t\t%s = __v\n\t}\n", read, lhs)
		}
	}
	fmt.Fprintf(b, "\t_ = %s\n", pv.Name)
}

// routeParamField is the struct field a path placeholder fills. The checker
// has already refused a placeholder naming none, so a miss here is a route
// whose href the platform read differently from the checker.
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

// routePathExpr is the route's path with each parameter's value substituted in.
// The mux pattern spells `{pkg}`, and a redirect has to name the page the
// browser should ask for next rather than the pattern that matched it.
func routePathExpr(r codegen.HTTPRoute, gc *GoIRContext) string {
	pv := routeParamsVar(r)
	if len(r.Params) == 0 || pv == nil {
		return strconv.Quote(r.Path)
	}
	gc.RequireImport("net/url")
	var parts []string
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, strconv.Quote(lit.String()))
			lit.Reset()
		}
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(r.Path, "/"), "/") {
		lit.WriteByte('/')
		if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			// A placeholder the params struct has no field for is written
			// through as the pattern wrote it. The checker refuses one where
			// the href is a literal, which is where the rule can be stated;
			// a path the platform assembled from an expression is not, and
			// naming a field nobody declared would not compile.
			if read := routeParamString(pv, seg[1:len(seg)-1], gc); read != "" {
				flush()
				parts = append(parts, fmt.Sprintf("url.PathEscape(%s)", read))
				continue
			}
		}
		lit.WriteString(seg)
	}
	flush()
	return strings.Join(parts, " + ")
}

// routeParamString is one path parameter read back out of the binding, as a
// string: the redirect writes the page the browser should ask for next, and a
// field the struct typed as a number has to be spelled back the way the path
// spelled it. Empty where the struct has no such field.
func routeParamString(pv *ir.Param, name string, gc *GoIRContext) string {
	sd, _ := pv.Type.Decl.(*ir.StructDef)
	field := routeParamField(sd, name)
	if field == nil {
		return ""
	}
	read := pv.Name + "." + ExportName(name)
	switch IRTypeToGo(field.Type) {
	case "string":
		return read
	case "int":
		gc.RequireImport("strconv")
		return "strconv.Itoa(" + read + ")"
	}
	gc.RequireImport("fmt")
	return "fmt.Sprint(" + read + ")"
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
	fmt.Fprintf(b, "func %s(%s http.ResponseWriter, _ *http.Request) {\n", r.Name, routeWriterVar)
	fmt.Fprintf(b, "\t%s.Header().Set(\"Content-Type\", \"text/html; charset=utf-8\")\n", routeWriterVar)
	fmt.Fprintf(b, "\t%s.Write([]byte(%s))\n", routeWriterVar, strconv.Quote(page))
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}
