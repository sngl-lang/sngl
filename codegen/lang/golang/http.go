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
	needsErrHelper := anyNativeHasErr(req)
	fmt.Fprintln(&body, "import (")
	fmt.Fprintln(&body, `	"net/http"`)
	if needsErrHelper {
		fmt.Fprintln(&body, `	"log"`)
	}
	for _, imp := range extraImports {
		fmt.Fprintf(&body, "\t%q\n", imp)
	}
	fmt.Fprintln(&body, ")")
	fmt.Fprintln(&body)

	if needsErrHelper {
		fmt.Fprintln(&body, `// nativeMustOK adapts a native call of shape (T, error): it logs and`)
		fmt.Fprintln(&body, `// swallows the error, returning the value (zero T on failure).`)
		fmt.Fprintln(&body, `func nativeMustOK[T any](v T, err error) T {`)
		fmt.Fprintln(&body, `	if err != nil {`)
		body.WriteString("\t\tlog.Printf(\"native call failed: %v\", err)\n")
		fmt.Fprintln(&body, `	}`)
		fmt.Fprintln(&body, `	return v`)
		fmt.Fprintln(&body, `}`)
		fmt.Fprintln(&body)
	}

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
	scope := &codegen.ExprScope{ContextVar: "r.Context()"}
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

// anyNativeHasErr reports whether any native call reachable from an action
// has HasErrorReturn set; used to decide whether to emit the error-adapter
// helper and its "log" import.
func anyNativeHasErr(req *codegen.HTTPRequest) bool {
	found := false
	var visitStmt func(ir.Stmt)
	var visitExpr func(ir.Expr)
	visitExpr = func(e ir.Expr) {
		if found || e == nil {
			return
		}
		switch x := e.(type) {
		case *ir.Call:
			if x.Func != nil && x.Func.HasErrorReturn {
				found = true
				return
			}
			for _, a := range x.Args {
				visitExpr(a.Value)
			}
			visitExpr(x.Receiver)
			visitExpr(x.Callee)
		case *ir.Binary:
			visitExpr(x.Left)
			visitExpr(x.Right)
		case *ir.Unary:
			visitExpr(x.Operand)
		case *ir.Ternary:
			visitExpr(x.Cond)
			visitExpr(x.Then)
			visitExpr(x.Else)
		case *ir.Select:
			visitExpr(x.Operand)
		case *ir.Index:
			visitExpr(x.Operand)
			visitExpr(x.Idx)
		case *ir.Conversion:
			visitExpr(x.Operand)
		case *ir.StructLit:
			for _, f := range x.Fields {
				visitExpr(f.Value)
			}
		case *ir.ListLit:
			for _, el := range x.Elems {
				visitExpr(el)
			}
		case *ir.Spread:
			visitExpr(x.Operand)
		}
	}
	visitStmt = func(s ir.Stmt) {
		if found || s == nil {
			return
		}
		switch x := s.(type) {
		case *ir.Assign:
			visitExpr(x.Target)
			visitExpr(x.Value)
		case *ir.CallStmt:
			if x.Call != nil {
				visitExpr(x.Call)
			}
		case *ir.LocalVar:
			visitExpr(x.Init)
		case *ir.Return:
			visitExpr(x.Value)
		case *ir.If:
			visitExpr(x.Cond)
			for _, ss := range x.Body {
				visitStmt(ss)
			}
			for _, ss := range x.Else {
				visitStmt(ss)
			}
		case *ir.For:
			visitExpr(x.Iter)
			for _, ss := range x.Body {
				visitStmt(ss)
			}
		case *ir.PlatformFilter:
			for _, ss := range x.Body {
				visitStmt(ss)
			}
		case *ir.Emit:
			for _, a := range x.Args {
				visitExpr(a.Value)
			}
		}
	}
	for _, route := range req.Routes {
		for _, act := range route.Actions {
			for _, s := range act.Mutations {
				visitStmt(s)
			}
		}
	}
	return found
}

// collectGoImports walks every action's mutation tree for calls into go://
// imports and returns their native ImportPaths, sorted and deduped. Each
// *ir.Func carries its own NativePkg, so no cross-referencing against
// Pkg.Imports is required.
func collectGoImports(req *codegen.HTTPRequest) []string {
	seen := map[string]bool{}
	for _, route := range req.Routes {
		for _, act := range route.Actions {
			for _, s := range act.Mutations {
				collectFromStmt(s, seen)
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

func collectFromStmt(s ir.Stmt, seen map[string]bool) {
	switch x := s.(type) {
	case *ir.Assign:
		collectFromExpr(x.Target, seen)
		collectFromExpr(x.Value, seen)
	case *ir.CallStmt:
		if x.Call != nil {
			collectFromExpr(x.Call, seen)
		}
	case *ir.LocalVar:
		collectFromExpr(x.Init, seen)
	case *ir.Return:
		collectFromExpr(x.Value, seen)
	case *ir.If:
		collectFromExpr(x.Cond, seen)
		for _, ss := range x.Body {
			collectFromStmt(ss, seen)
		}
		for _, ss := range x.Else {
			collectFromStmt(ss, seen)
		}
	case *ir.For:
		collectFromExpr(x.Iter, seen)
		for _, ss := range x.Body {
			collectFromStmt(ss, seen)
		}
	case *ir.PlatformFilter:
		for _, ss := range x.Body {
			collectFromStmt(ss, seen)
		}
	case *ir.Emit:
		for _, a := range x.Args {
			collectFromExpr(a.Value, seen)
		}
	}
}

func collectFromExpr(e ir.Expr, seen map[string]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil && x.Func.NativePkg != "" {
			seen[x.Func.NativePkg] = true
		}
		for _, a := range x.Args {
			collectFromExpr(a.Value, seen)
		}
		collectFromExpr(x.Receiver, seen)
		collectFromExpr(x.Callee, seen)
	case *ir.Binary:
		collectFromExpr(x.Left, seen)
		collectFromExpr(x.Right, seen)
	case *ir.Unary:
		collectFromExpr(x.Operand, seen)
	case *ir.Ternary:
		collectFromExpr(x.Cond, seen)
		collectFromExpr(x.Then, seen)
		collectFromExpr(x.Else, seen)
	case *ir.Select:
		collectFromExpr(x.Operand, seen)
	case *ir.Index:
		collectFromExpr(x.Operand, seen)
		collectFromExpr(x.Idx, seen)
	case *ir.Conversion:
		collectFromExpr(x.Operand, seen)
	case *ir.StructLit:
		for _, f := range x.Fields {
			collectFromExpr(f.Value, seen)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			collectFromExpr(el, seen)
		}
	case *ir.Spread:
		collectFromExpr(x.Operand, seen)
	}
}
