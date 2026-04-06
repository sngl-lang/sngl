package golang

import (
	"fmt"
	"go/format"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// CompileHTTP implements codegen.HTTPCompiler for Go.
func (t *Translator) CompileHTTP(req *codegen.HTTPRequest) ([]byte, error) {
	switch req.Framework {
	case "net/http", "":
		return compileNetHTTP(req)
	default:
		return nil, fmt.Errorf("golang: unsupported HTTP framework %q", req.Framework)
	}
}

func compileNetHTTP(req *codegen.HTTPRequest) ([]byte, error) {
	// Pre-render all route bodies to know which packages are referenced.
	windows := req.Doc.App.EffectiveWindows()
	routeBodies := make([]string, len(req.Routes))
	for i := range req.Routes {
		routeBodies[i] = req.RenderHTML(i)
	}

	// Determine which go:// imports are actually referenced in the output.
	goImports := make(map[string]string) // import path → package alias
	for ns, decls := range req.Doc.NativeImports {
		if decls == nil || decls.ImportPath == "" {
			continue
		}
		// Check if any rendered body or action references this namespace.
		used := false
		for _, body := range routeBodies {
			if strings.Contains(body, ns+".") {
				used = true
				break
			}
		}
		if !used {
			// Check action handlers too.
			for _, route := range req.Routes {
				for _, action := range route.Actions {
					if action.Expr.SNGL != nil {
						scope := makeExternScope(req.Doc)
						t := &Translator{}
						stmts := t.TranslateMutation(action.Expr.SNGL, scope)
						for _, s := range stmts {
							if strings.Contains(s, ns+".") {
								used = true
								break
							}
						}
					}
					if used {
						break
					}
				}
				if used {
					break
				}
			}
		}
		if used {
			goImports[decls.ImportPath] = ns
		}
	}

	var b strings.Builder

	// Package declaration.
	fmt.Fprintf(&b, "package %s\n\n", req.Package)

	// Imports.
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"html\"\n")
	if req.Main {
		b.WriteString("\t\"log\"\n")
	}
	b.WriteString("\t\"net/http\"\n")
	if req.Main {
		b.WriteString("\t\"os\"\n")
	}
	if len(goImports) > 0 {
		b.WriteString("\n")
		sorted := make([]string, 0, len(goImports))
		for pkg := range goImports {
			sorted = append(sorted, pkg)
		}
		sort.Strings(sorted)
		for _, pkg := range sorted {
			fmt.Fprintf(&b, "\t%q\n", pkg)
		}
	}
	b.WriteString(")\n\n")

	// Suppress unused import warnings.
	b.WriteString("var _ = fmt.Sprint\n")
	b.WriteString("var _ = html.EscapeString\n\n")

	// Ternary helper (only emitted when used by conditional expressions).
	needsTernary := false
	for _, body := range routeBodies {
		if strings.Contains(body, "ternary(") {
			needsTernary = true
			break
		}
	}
	if needsTernary {
		b.WriteString("func ternary[T any](cond bool, a, b T) T {\n")
		b.WriteString("\tif cond {\n\t\treturn a\n\t}\n\treturn b\n}\n\n")
	}

	// Handler function.
	b.WriteString("// Handler returns an http.Handler that serves all routes.\n")
	b.WriteString("func Handler() http.Handler {\n")
	b.WriteString("\tmux := http.NewServeMux()\n")
	for _, route := range req.Routes {
		fmt.Fprintf(&b, "\tmux.HandleFunc(\"GET %s\", %s)\n", route.Path, route.Name)
		if len(route.Actions) > 0 {
			fmt.Fprintf(&b, "\tmux.HandleFunc(\"POST %s\", %sAction)\n", route.Path, route.Name)
		}
	}
	b.WriteString("\treturn mux\n")
	b.WriteString("}\n\n")

	// Route handlers.
	for i, route := range req.Routes {
		fmt.Fprintf(&b, "func %s(w http.ResponseWriter, r *http.Request) {\n", route.Name)
		b.WriteString("\tw.Header().Set(\"Content-Type\", \"text/html; charset=utf-8\")\n")

		// Route parameters from path (e.g., /{name} → name := r.PathValue("name"))
		for _, param := range route.Params {
			fmt.Fprintf(&b, "\t%s := r.PathValue(%q)\n", param, param)
			fmt.Fprintf(&b, "\t_ = %s\n", param)
		}

		win := windows[route.WindowIdx]
		emitLocalVars(&b, req.Doc.Data)
		emitLocalVars(&b, win.Data)

		title := route.Title
		if title == "" {
			title = route.Name
		}
		fmt.Fprintf(&b, "\tfmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title></head><body>`)\n", title)

		b.WriteString(routeBodies[i])

		b.WriteString("\tfmt.Fprint(w, `</body></html>`)\n")
		b.WriteString("}\n\n")
	}

	// POST action handlers.
	for _, route := range req.Routes {
		if len(route.Actions) == 0 {
			continue
		}
		fmt.Fprintf(&b, "func %sAction(w http.ResponseWriter, r *http.Request) {\n", route.Name)
		b.WriteString("\tswitch r.FormValue(\"action\") {\n")
		for _, action := range route.Actions {
			fmt.Fprintf(&b, "\tcase %q:\n", action.Name)
			scope := makeExternScope(req.Doc)
			if action.Expr.SNGL != nil {
				t := &Translator{}
				stmts := t.TranslateMutation(action.Expr.SNGL, scope)
				for _, s := range stmts {
					stmt := injectHiddenParamsInStmt(s, req.Doc)
					fmt.Fprintf(&b, "\t\t%s\n", stmt)
				}
			}
		}
		b.WriteString("\t}\n")
		fmt.Fprintf(&b, "\thttp.Redirect(w, r, %q, http.StatusSeeOther)\n", route.Path)
		b.WriteString("}\n\n")
	}

	// Main function.
	if req.Main {
		b.WriteString("func main() {\n")
		b.WriteString("\taddr := \":8080\"\n")
		b.WriteString("\tif port := os.Getenv(\"PORT\"); port != \"\" {\n")
		b.WriteString("\t\taddr = \":\" + port\n")
		b.WriteString("\t}\n")
		b.WriteString("\tfmt.Fprintf(os.Stderr, \"listening on %s\\n\", addr)\n")
		b.WriteString("\tlog.Fatal(http.ListenAndServe(addr, Handler()))\n")
		b.WriteString("}\n")
	}

	src := []byte(b.String())
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("generated code formatting error: %w\n%s", err, src)
	}
	return formatted, nil
}

func makeExternScope(doc *ast.Document) *codegen.ExprScope {
	scope := &codegen.ExprScope{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		LocalVars:      make(map[string]bool),
		NeededHelpers:  make(map[string]bool),
	}
	for ns, decls := range doc.NativeImports {
		if decls == nil {
			continue
		}
		for _, d := range decls.Data {
			scope.ExternFuncs[ns+"."+d.Name] = true
		}
	}
	return scope
}

func injectHiddenParamsInStmt(stmt string, doc *ast.Document) string {
	for ns, decls := range doc.NativeImports {
		if decls == nil {
			continue
		}
		for _, d := range decls.Data {
			if d.HiddenParam == "" {
				continue
			}
			injectedArg := hiddenParamArgs(d.HiddenParam)
			call := ns + "." + d.Name + "("
			for {
				idx := strings.Index(stmt, call)
				if idx < 0 {
					break
				}
				afterCall := idx + len(call)
				if afterCall < len(stmt) && stmt[afterCall] == ')' {
					stmt = stmt[:afterCall] + injectedArg + stmt[afterCall:]
				} else {
					stmt = stmt[:afterCall] + injectedArg + ", " + stmt[afterCall:]
				}
			}
		}
	}
	return stmt
}

func hiddenParamArgs(hidden string) string {
	parts := strings.Split(hidden, ",")
	var args []string
	for _, p := range parts {
		switch p {
		case "*http.Request":
			args = append(args, "r")
		case "http.ResponseWriter":
			args = append(args, "w")
		case "context.Context":
			args = append(args, "r.Context()")
		}
	}
	return strings.Join(args, ", ")
}

func emitLocalVars(b *strings.Builder, data []*ast.Data) {
	for _, d := range data {
		if d.Extern || d.IsFunc {
			continue
		}
		initVal := LiteralToGo(d.Init)
		if initVal == "nil" {
			// Skip vars with non-literal init (e.g., function call results).
			// These will be accessed via their original expressions at use sites.
			continue
		}
		fmt.Fprintf(b, "\t%s := %s\n", d.Name, initVal)
		fmt.Fprintf(b, "\t_ = %s\n", d.Name)
	}
}
