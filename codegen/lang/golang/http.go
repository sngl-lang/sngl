package golang

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CompileHTTP implements codegen.HTTPCompiler for Go. Produces a single
// server.go file containing: package clause, imports, Handler() returning an
// http.ServeMux, one GET (and optionally one POST) handler per route, and
// — when req.Main is set — a main() that calls http.ListenAndServe.
func (t *Translator) CompileHTTP(req *codegen.HTTPRequest) ([]*codegen.OutputFile, error) {
	if req.Framework != "" && req.Framework != "net/http" {
		return nil, fmt.Errorf("golang: framework %q not implemented", req.Framework)
	}

	var body bytes.Buffer
	pkgName := req.Package
	if pkgName == "" {
		if req.Main {
			pkgName = "main"
		} else {
			pkgName = "ui"
		}
	}
	fmt.Fprintf(&body, "package %s\n\n", pkgName)

	extraImports := collectGoImports(req)
	fmt.Fprintln(&body, "import (")
	fmt.Fprintln(&body, `	"net/http"`)
	for _, imp := range extraImports {
		fmt.Fprintf(&body, "\t%q\n", imp)
	}
	fmt.Fprintln(&body, ")")
	fmt.Fprintln(&body)

	writeHandler(&body, req)
	for _, r := range req.Routes {
		writeRouteHandler(&body, req, r, t)
	}
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

func writeRouteHandler(b *bytes.Buffer, req *codegen.HTTPRequest, r codegen.HTTPRoute, t *Translator) {
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
	scope := &codegen.ExprScope{}
	for _, act := range r.Actions {
		fmt.Fprintf(b, "\tcase %q:\n", act.Name)
		for _, s := range act.Mutations {
			for _, line := range t.TranslateIRMutation(s, scope) {
				fmt.Fprintf(b, "\t\t%s\n", line)
			}
		}
	}
	fmt.Fprintln(b, `	}`)
	fmt.Fprintf(b, "\thttp.Redirect(w, r, %q, http.StatusSeeOther)\n", r.Path)
	fmt.Fprintln(b, `}`)
	fmt.Fprintln(b)
}

// collectGoImports walks every action's mutation tree for calls into go://
// imports and returns their native ImportPaths, sorted and deduped.
func collectGoImports(req *codegen.HTTPRequest) []string {
	if req.Pkg == nil {
		return nil
	}
	funcToPath := map[*ir.Func]string{}
	for _, imp := range req.Pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, _ := codegen.SplitScheme(imp.AST.Path)
		if scheme != "go" {
			continue
		}
		for _, f := range imp.Native.Funcs {
			funcToPath[f] = imp.Native.ImportPath
		}
	}
	seen := map[string]bool{}
	for _, route := range req.Routes {
		for _, act := range route.Actions {
			for _, s := range act.Mutations {
				collectFromStmt(s, funcToPath, seen)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func collectFromStmt(s ir.Stmt, funcToPath map[*ir.Func]string, seen map[string]bool) {
	switch x := s.(type) {
	case *ir.Assign:
		collectFromExpr(x.Target, funcToPath, seen)
		collectFromExpr(x.Value, funcToPath, seen)
	case *ir.CallStmt:
		if x.Call != nil {
			collectFromExpr(x.Call, funcToPath, seen)
		}
	case *ir.LocalVar:
		collectFromExpr(x.Init, funcToPath, seen)
	case *ir.Return:
		collectFromExpr(x.Value, funcToPath, seen)
	case *ir.If:
		collectFromExpr(x.Cond, funcToPath, seen)
		for _, ss := range x.Body {
			collectFromStmt(ss, funcToPath, seen)
		}
		for _, ss := range x.Else {
			collectFromStmt(ss, funcToPath, seen)
		}
	case *ir.For:
		collectFromExpr(x.Iter, funcToPath, seen)
		for _, ss := range x.Body {
			collectFromStmt(ss, funcToPath, seen)
		}
	case *ir.PlatformFilter:
		for _, ss := range x.Body {
			collectFromStmt(ss, funcToPath, seen)
		}
	case *ir.Emit:
		for _, a := range x.Args {
			collectFromExpr(a.Value, funcToPath, seen)
		}
	}
}

func collectFromExpr(e ir.Expr, funcToPath map[*ir.Func]string, seen map[string]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil {
			if path, ok := funcToPath[x.Func]; ok {
				seen[path] = true
			}
		}
		for _, a := range x.Args {
			collectFromExpr(a.Value, funcToPath, seen)
		}
		collectFromExpr(x.Receiver, funcToPath, seen)
		collectFromExpr(x.Callee, funcToPath, seen)
	case *ir.Binary:
		collectFromExpr(x.Left, funcToPath, seen)
		collectFromExpr(x.Right, funcToPath, seen)
	case *ir.Unary:
		collectFromExpr(x.Operand, funcToPath, seen)
	case *ir.Ternary:
		collectFromExpr(x.Cond, funcToPath, seen)
		collectFromExpr(x.Then, funcToPath, seen)
		collectFromExpr(x.Else, funcToPath, seen)
	case *ir.Select:
		collectFromExpr(x.Operand, funcToPath, seen)
	case *ir.Index:
		collectFromExpr(x.Operand, funcToPath, seen)
		collectFromExpr(x.Idx, funcToPath, seen)
	case *ir.Conversion:
		collectFromExpr(x.Operand, funcToPath, seen)
	case *ir.StructLit:
		for _, f := range x.Fields {
			collectFromExpr(f.Value, funcToPath, seen)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			collectFromExpr(el, funcToPath, seen)
		}
	case *ir.Spread:
		collectFromExpr(x.Operand, funcToPath, seen)
	}
}
