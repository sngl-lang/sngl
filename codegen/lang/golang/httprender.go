package golang

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// httprender.go emits the server-side rendering machinery for an html route
// whose handlers/bindings were placed on the backend: a per-route State struct,
// an in-process per-session store, a renderRoute(s) builder that fills the
// route's RouteRender holes by translating their IR against the session State,
// and the GET/POST (PRG) handlers. All Go-specific; the html platform stays
// language-agnostic and only ships the RouteRender / StateVars / actions across
// the seam.
//
// State idents resolve to `s.<ExportedField>` because the handler/render
// GoIRContext sets ExprCtx.StateReceiver to the route's state local (see
// newRouteGC); evalIdent and MutTargetIdent honor that for NameStateVar.

// routeStateType returns the Go type name of a route's per-session State struct.
func routeStateType(r codegen.HTTPRoute) string {
	return r.Name + "State"
}

// newRouteGC builds a GoIRContext whose state idents project onto the `s`
// receiver (the per-session State struct), used by both renderRoute and the
// POST action body so a state read/write renders `s.<Field>`.
func newRouteGC(req *codegen.HTTPRequest, r codegen.HTTPRoute, shared *GoIRContext, loc routeLocals) *GoIRContext {
	ctx := codegen.NewExprCtx(req.Pkg)
	ctx.ContextVar = loc.request + ".Context()"
	ctx.StateReceiver = loc.state
	ctx.StateFieldsExported = true
	// A harness root's state is surfaced to the route beside the package's,
	// matching routeStateVars, and StateReceiver projects it onto `s.<Field>`.
	if root := req.Pkg.RootDecl(); root != nil {
		ctx = ctx.ForComponent(root)
	}
	// And to the route's own window, which is where a root component's state is
	// by the time a backend sees it (#215). ForWindow keeps the component scope
	// rather than replacing it.
	if r.Window != nil {
		ctx = ctx.ForWindow(r.Window)
	}
	// The document's route parameters resolve through that window scope, and
	// they are bound per request rather than per session -- so left alone
	// they would project onto `s.<Field>` of a State struct that has no such
	// field. As a local the binding renders as the bare name
	// writeRouteParamBindings declares.
	if p := routeParamsVar(r); p != nil {
		ctx = ctx.WithLocal(p.Name)
	}
	gc := &GoIRContext{Ctx: ctx, imports: shared.imports}
	return gc
}

// routeParamsVar is the binding a route's path parameters arrive in: one
// struct value, named and typed by the window's scoped slot. Nil for a route
// whose window declared none.
func routeParamsVar(r codegen.HTTPRoute) *ir.Param {
	if r.Window == nil {
		return nil
	}
	return r.Window.Params
}

// snglSessionTTL is the lazy eviction window emitted into the session store
// (entries idle longer than this are dropped on access).
const snglSessionTTL = "30 * time.Minute"

// emitSessionStore writes a tiny per-session store: a cookie-keyed,
// mutex-guarded map of session id → *snglSession. Inlined into the generated
// server so the emitted package is self-contained (no external runtime
// dependency). Emitted once per server, parameterized over the union of route
// State types via a generic-free `any` field plus per-route accessors.
//
// Each session carries its OWN mutex; the map mutex guards only the map, while
// the per-session mutex is held by the handler across the render/mutation so a
// concurrent GET (reading state) and POST (mutating state) on the same session
// cannot race. A lazy TTL evicts idle sessions on access to bound map growth.
func emitSessionStore(b *bytes.Buffer, gc *GoIRContext) {
	gc.RequireImport("crypto/rand")
	gc.RequireImport("encoding/hex")
	gc.RequireImport("sync")
	gc.RequireImport("time")
	fmt.Fprintln(b, `// snglSession is one cookie's server state, guarded by its own mutex so a`)
	fmt.Fprintln(b, `// handler can hold the lock across the whole render/mutation.`)
	fmt.Fprintln(b, `type snglSession struct {`)
	fmt.Fprintln(b, "\tmu         sync.Mutex")
	fmt.Fprintln(b, "\tstate      any")
	fmt.Fprintln(b, "\tlastAccess time.Time")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
	fmt.Fprintln(b, `// snglSessionStore is a minimal cookie-keyed in-memory session store. Each`)
	fmt.Fprintln(b, `// distinct cookie maps to its own per-route state value. Sessions idle`)
	fmt.Fprintf(b, "// longer than snglSessionTTL (%s) are evicted lazily on access to bound\n", snglSessionTTL)
	fmt.Fprintln(b, `// the map's growth.`)
	fmt.Fprintln(b, `type snglSessionStore struct {`)
	fmt.Fprintln(b, "\tmu   sync.Mutex")
	fmt.Fprintln(b, "\tdata map[string]*snglSession")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
	fmt.Fprintf(b, "const snglSessionTTL = %s\n", snglSessionTTL)
	fmt.Fprintln(b)
	fmt.Fprintln(b, `var snglSessions = &snglSessionStore{data: map[string]*snglSession{}}`)
	fmt.Fprintln(b)
	// session returns (and creates) the *snglSession for a key, evicting idle
	// entries first. The caller locks the returned session's mutex.
	fmt.Fprintln(b, `// snglSession returns the *snglSession for key, creating it if absent. Idle`)
	fmt.Fprintln(b, `// sessions are evicted first. The caller must lock the returned session.`)
	fmt.Fprintln(b, `func (st *snglSessionStore) session(key string) *snglSession {`)
	fmt.Fprintln(b, "\tst.mu.Lock()")
	fmt.Fprintln(b, "\tdefer st.mu.Unlock()")
	fmt.Fprintln(b, "\tnow := time.Now()")
	fmt.Fprintln(b, "\tfor k, sess := range st.data {")
	fmt.Fprintln(b, "\t\tif now.Sub(sess.lastAccess) > snglSessionTTL {")
	fmt.Fprintln(b, "\t\t\tdelete(st.data, k)")
	fmt.Fprintln(b, "\t\t}")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tsess := st.data[key]")
	fmt.Fprintln(b, "\tif sess == nil {")
	fmt.Fprintln(b, "\t\tsess = &snglSession{}")
	fmt.Fprintln(b, "\t\tst.data[key] = sess")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tsess.lastAccess = now")
	fmt.Fprintln(b, "\treturn sess")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
	fmt.Fprintln(b, `// snglSessionID returns the caller's session id, minting and setting a`)
	fmt.Fprintln(b, `// hardened cookie when absent. ok is false (and a 500 already written) if`)
	fmt.Fprintln(b, `// a fresh id could not be generated.`)
	fmt.Fprintln(b, `func snglSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {`)
	fmt.Fprintln(b, "\tif c, err := r.Cookie(\"sngl_session\"); err == nil && c.Value != \"\" {")
	fmt.Fprintln(b, "\t\treturn c.Value, true")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tvar buf [16]byte")
	fmt.Fprintln(b, "\tif _, err := rand.Read(buf[:]); err != nil {")
	fmt.Fprintln(b, "\t\thttp.Error(w, \"internal server error\", http.StatusInternalServerError)")
	fmt.Fprintln(b, "\t\treturn \"\", false")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tid := hex.EncodeToString(buf[:])")
	fmt.Fprintln(b, "\thttp.SetCookie(w, &http.Cookie{")
	fmt.Fprintln(b, "\t\tName:     \"sngl_session\",")
	fmt.Fprintln(b, "\t\tValue:    id,")
	fmt.Fprintln(b, "\t\tPath:     \"/\",")
	fmt.Fprintln(b, "\t\tHttpOnly: true,")
	fmt.Fprintln(b, "\t\tSameSite: http.SameSiteLaxMode,")
	fmt.Fprintln(b, "\t})")
	fmt.Fprintln(b, "\treturn id, true")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// emitState writes the per-route State struct (one exported field per StateVar)
// plus a session loader that returns the request's *snglSession (whose mutex the
// caller locks) and a *State view onto its stored value.
func emitState(b *bytes.Buffer, r codegen.HTTPRoute) {
	stateType := routeStateType(r)
	fmt.Fprintf(b, "// %s is the per-session server state for route %q.\n", stateType, r.Path)
	fmt.Fprintf(b, "type %s struct {\n", stateType)
	for _, sv := range r.StateVars {
		fmt.Fprintf(b, "\t%s %s\n", ExportName(sv.Name), IRTypeToGo(sv.Type))
	}
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)

	// Loader keyed on the route's State type. Distinct routes use distinct
	// store keys (handler name) so their states don't collide. Returns the
	// owning *snglSession (the caller locks sess.mu across read/mutation) and
	// the typed *State view of its stored value.
	loader := "load" + ExportName(r.Name) + "State"
	fmt.Fprintf(b, "func %s(id string) (*snglSession, *%s) {\n", loader, stateType)
	fmt.Fprintf(b, "\tkey := %q + id\n", r.Name+":")
	fmt.Fprintln(b, "\tsess := snglSessions.session(key)")
	fmt.Fprintln(b, "\tsess.mu.Lock()")
	fmt.Fprintf(b, "\ts, ok := sess.state.(*%s)\n", stateType)
	fmt.Fprintln(b, "\tif !ok {")
	fmt.Fprintf(b, "\t\ts = &%s{}\n", stateType)
	fmt.Fprintln(b, "\t\tsess.state = s")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\treturn sess, s")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// emitRenderRoute writes `func renderXState(s *XState) string` that concatenates
// the RouteRender chunks and fills each hole by translating its IR expr against
// the session State via gc. Returns the function name.
func emitRenderRoute(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, gc *GoIRContext, loc routeLocals) string {
	stateType := routeStateType(r)
	fnName := "render" + ExportName(r.Name)
	gc.RequireImport("strings")

	var sig strings.Builder
	sig.WriteString(loc.state + " *" + stateType)
	if p := routeParamsVar(r); p != nil {
		sig.WriteString(", " + p.Name + " " + IRTypeToGo(p.Type))
	}
	fmt.Fprintf(b, "func %s(%s) string {\n", fnName, sig.String())
	if p := routeParamsVar(r); p != nil {
		fmt.Fprintf(b, "\t_ = %s\n", p.Name)
	}
	fmt.Fprintf(b, "\tvar %s strings.Builder\n", loc.build)
	if r.Render != nil {
		writeRenderBody(b, "\t", loc.build, r.Render, gc)
	}
	fmt.Fprintf(b, "\treturn %s.String()\n", loc.build)
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
	return fnName
}

// writeRenderBody emits, into builder var `bv`, the chunks/holes of rr at the
// given indent. HoleText string-coerces; HoleAttr writes the attribute value;
// HoleIf/HoleFor recurse.
func writeRenderBody(b *bytes.Buffer, indent, bv string, rr *codegen.RouteRender, gc *GoIRContext) {
	for i, chunk := range rr.Chunks {
		if chunk != "" {
			fmt.Fprintf(b, "%s%s.WriteString(%s)\n", indent, bv, strconv.Quote(chunk))
		}
		if i >= len(rr.Holes) {
			continue
		}
		h := rr.Holes[i]
		switch h.Kind {
		case codegen.HoleText, codegen.HoleAttr:
			fmt.Fprintf(b, "%s%s.WriteString(%s)\n", indent, bv, holeStringExpr(h, gc))
		case codegen.HoleIf:
			fmt.Fprintf(b, "%sif %s {\n", indent, gc.EvalExpr(h.Expr))
			if h.Then != nil {
				writeRenderBody(b, indent+"\t", bv, h.Then, gc)
			}
			if h.Else != nil {
				fmt.Fprintf(b, "%s} else {\n", indent)
				writeRenderBody(b, indent+"\t", bv, h.Else, gc)
			}
			fmt.Fprintf(b, "%s}\n", indent)
		case codegen.HoleFor:
			key := h.Key
			if key == "" {
				key = "_item"
			}
			fmt.Fprintf(b, "%sfor _, %s := range %s {\n", indent, key, gc.EvalExpr(h.Expr))
			// A loop body that never names the element is legal SNGL and
			// "declared and not used" in Go.
			fmt.Fprintf(b, "%s\t_ = %s\n", indent, key)
			if h.Then != nil {
				writeRenderBody(b, indent+"\t", bv, h.Then, gc.WithLocal(key))
			}
			fmt.Fprintf(b, "%s}\n", indent)
		}
	}
}

// holeStringExpr renders a text/attr hole's expression coerced to a Go string
// and HTML-escaped. String-typed expressions pass through the string coercion;
// everything else routes through fmt.Sprint (matching the client renderer's
// String() coercion). The result is wrapped in html.EscapeString so state
// echoed into a text node or a (double-quoted) attribute can't inject markup —
// matching the client DOM path, which escapes via textContent/setAttribute.
// html.EscapeString covers &, <, >, ' and ", so it is safe for both text and
// double-quoted attribute contexts (the only attribute quoting the render model
// emits).
func holeStringExpr(h codegen.RouteHole, gc *GoIRContext) string {
	expr := gc.EvalExpr(h.Expr)
	if t := h.Expr.ExprType(); t == nil || IRTypeToGo(t) != "string" {
		gc.RequireImport("fmt")
		expr = "fmt.Sprint(" + expr + ")"
	}
	gc.RequireImport("html")
	return "html.EscapeString(" + expr + ")"
}
