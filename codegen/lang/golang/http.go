package golang

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// CompileHTTP implements codegen.HTTPCompiler for Go. Produces a single
// server.go file containing: package clause, imports, Handler() returning an
// http.ServeMux, one GET (and optionally one POST) handler per route, and
// — when req.Main is set — a main() that calls http.ListenAndServe.
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

	// A single GoIRContext renders every action handler body. It tracks the
	// native imports those bodies reference via RequireImport; we read them
	// back through gc.Imports() to build the import block, replacing the
	// legacy manual collectGoImports walk. Error-returning natives are
	// inline-wrapped by GoIRContext.maybeWrapErrorReturn, so the old
	// nativeMustOK helper + "log" import are gone.
	ctx := codegen.NewExprCtx(req.Pkg)
	ctx.ContextVar = "r.Context()"
	gc := NewIRContext(ctx)

	// Render the route bodies first so the GoIRContext accumulates its
	// imports before we emit the import block.
	var routeBody bytes.Buffer
	writeHandler(&routeBody, req)
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

	return []*codegen.OutputFile{codegen.BytesFile("server.go", body.Bytes())}, nil
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

func writeRouteHandler(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, gc *GoIRContext) {
	body := req.RenderHTML(r.WindowIdx)
	fmt.Fprintf(b, "func %s(w http.ResponseWriter, r *http.Request) {\n", r.Name)
	fmt.Fprintln(b, `	w.Header().Set("Content-Type", "text/html; charset=utf-8")`)
	fmt.Fprintf(b, "\tw.Write([]byte(%s))\n", strconv.Quote(body))
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)

	if len(r.Actions) == 0 {
		return
	}
	fmt.Fprintf(b, "func %sAction(w http.ResponseWriter, r *http.Request) {\n", r.Name)
	fmt.Fprintln(b, `	switch r.FormValue("action") {`)
	for _, act := range r.Actions {
		fmt.Fprintf(b, "\tcase %q:\n", act.Name)
		for _, s := range act.Mutations {
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
