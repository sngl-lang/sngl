package golang

import (
	"bytes"
	"fmt"
	"strconv"

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
// GoIRContext sets ExprCtx.StateReceiver = "s" (see newRouteCtx); evalIdent and
// MutTargetIdent honor that for NameStateVar.

const routeStateReceiver = "s"

// routeStateType returns the Go type name of a route's per-session State struct.
func routeStateType(r codegen.HTTPRoute) string {
	return r.Name + "State"
}

// newRouteGC builds a GoIRContext whose state idents project onto the `s`
// receiver (the per-session State struct), used by both renderRoute and the
// POST action body so a state read/write renders `s.<Field>`.
func newRouteGC(req *codegen.HTTPRequest, shared *GoIRContext) *GoIRContext {
	ctx := codegen.NewExprCtx(req.Pkg)
	ctx.ContextVar = "r.Context()"
	ctx.StateReceiver = routeStateReceiver
	// Scope to the main component so its state vars resolve (StateReceiver
	// then projects them onto `s.<Field>`). State surfaced to the route lives
	// on the main component (and/or package-level), matching routeStateVars.
	if main := mainComponent(req.Pkg); main != nil {
		ctx = ctx.ForComponent(main)
	}
	gc := &GoIRContext{Ctx: ctx, imports: shared.imports}
	return gc
}

// mainComponent returns the "main" component of pkg, or nil.
func mainComponent(pkg *ir.Package) *ir.Component {
	if pkg == nil {
		return nil
	}
	for _, c := range pkg.Components {
		if c != nil && c.Name == "main" {
			return c
		}
	}
	return nil
}

// emitSessionStore writes a tiny per-session store: a cookie-keyed,
// mutex-guarded map of session id → *State. Inlined into the generated server
// so the emitted package is self-contained (no external runtime dependency).
// Emitted once per server, parameterized over the union of route State types via
// a generic-free `any` map plus per-route accessors.
func emitSessionStore(b *bytes.Buffer, gc *GoIRContext) {
	gc.RequireImport("crypto/rand")
	gc.RequireImport("encoding/hex")
	gc.RequireImport("sync")
	fmt.Fprintln(b, `// snglSessionStore is a minimal cookie-keyed in-memory session store. Each`)
	fmt.Fprintln(b, `// distinct cookie maps to its own per-route State value.`)
	fmt.Fprintln(b, `type snglSessionStore struct {`)
	fmt.Fprintln(b, "\tmu   sync.Mutex")
	fmt.Fprintln(b, "\tdata map[string]any")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
	fmt.Fprintln(b, `var snglSessions = &snglSessionStore{data: map[string]any{}}`)
	fmt.Fprintln(b)
	fmt.Fprintln(b, `// snglSessionID returns the caller's session id, minting and setting a`)
	fmt.Fprintln(b, `// cookie when absent.`)
	fmt.Fprintln(b, `func snglSessionID(w http.ResponseWriter, r *http.Request) string {`)
	fmt.Fprintln(b, "\tif c, err := r.Cookie(\"sngl_session\"); err == nil && c.Value != \"\" {")
	fmt.Fprintln(b, "\t\treturn c.Value")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tvar buf [16]byte")
	fmt.Fprintln(b, "\trand.Read(buf[:])")
	fmt.Fprintln(b, "\tid := hex.EncodeToString(buf[:])")
	fmt.Fprintln(b, "\thttp.SetCookie(w, &http.Cookie{Name: \"sngl_session\", Value: id, Path: \"/\"})")
	fmt.Fprintln(b, "\treturn id")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// emitState writes the per-route State struct (one exported field per StateVar)
// plus a session loader that returns a stable *State for the request's cookie.
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
	// store keys (handler name) so their states don't collide.
	loader := "load" + ExportName(r.Name) + "State"
	fmt.Fprintf(b, "func %s(id string) *%s {\n", loader, stateType)
	fmt.Fprintln(b, "\tsnglSessions.mu.Lock()")
	fmt.Fprintln(b, "\tdefer snglSessions.mu.Unlock()")
	fmt.Fprintf(b, "\tkey := %q + id\n", r.Name+":")
	fmt.Fprintf(b, "\ts, ok := snglSessions.data[key].(*%s)\n", stateType)
	fmt.Fprintln(b, "\tif !ok {")
	fmt.Fprintf(b, "\t\ts = &%s{}\n", stateType)
	fmt.Fprintln(b, "\t\tsnglSessions.data[key] = s")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\treturn s")
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// emitRenderRoute writes `func renderXState(s *XState) string` that concatenates
// the RouteRender chunks and fills each hole by translating its IR expr against
// the session State via gc. Returns the function name.
func emitRenderRoute(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, gc *GoIRContext) string {
	stateType := routeStateType(r)
	fnName := "render" + ExportName(r.Name)
	gc.RequireImport("strings")

	fmt.Fprintf(b, "func %s(%s *%s) string {\n", fnName, routeStateReceiver, stateType)
	fmt.Fprintln(b, "\tvar __b strings.Builder")
	if r.Render != nil {
		writeRenderBody(b, "\t", "__b", r.Render, gc)
	}
	fmt.Fprintln(b, "\treturn __b.String()")
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
			fmt.Fprintf(b, "%s\t_ = %s\n", indent, key)
			if h.Then != nil {
				writeRenderBody(b, indent+"\t", bv, h.Then, gc.WithLocal(key))
			}
			fmt.Fprintf(b, "%s}\n", indent)
		}
	}
}

// holeStringExpr renders a text/attr hole's expression coerced to a Go string.
// String-typed expressions pass through; everything else routes through
// fmt.Sprint (matching the client renderer's String() coercion).
func holeStringExpr(h codegen.RouteHole, gc *GoIRContext) string {
	expr := gc.EvalExpr(h.Expr)
	if t := h.Expr.ExprType(); t != nil && IRTypeToGo(t) == "string" {
		return expr
	}
	gc.RequireImport("fmt")
	return "fmt.Sprint(" + expr + ")"
}
