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
// It generates a Go source file with HTTP handlers using the specified framework.
func (t *Translator) CompileHTTP(req *codegen.HTTPRequest) ([]byte, error) {
	switch req.Framework {
	case "net/http", "":
		return compileNetHTTP(req)
	default:
		return nil, fmt.Errorf("golang: unsupported HTTP framework %q", req.Framework)
	}
}

func compileNetHTTP(req *codegen.HTTPRequest) ([]byte, error) {
	var b strings.Builder

	// Collect go:// imports needed.
	goImports := make(map[string]bool)
	for _, decls := range req.Doc.NativeImports {
		if decls != nil && decls.ImportPath != "" {
			goImports[decls.ImportPath] = true
		}
	}

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
	// Add go:// imports.
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

	// Handler function.
	b.WriteString("// Handler returns an http.Handler that serves all routes.\n")
	b.WriteString("func Handler() http.Handler {\n")
	b.WriteString("\tmux := http.NewServeMux()\n")
	for _, route := range req.Routes {
		fmt.Fprintf(&b, "\tmux.HandleFunc(\"GET %s\", %s)\n", route.Path, route.Name)
	}
	b.WriteString("\treturn mux\n")
	b.WriteString("}\n\n")

	// Route handlers.
	windows := req.Doc.App.EffectiveWindows()
	for i, route := range req.Routes {
		fmt.Fprintf(&b, "func %s(w http.ResponseWriter, r *http.Request) {\n", route.Name)
		b.WriteString("\tw.Header().Set(\"Content-Type\", \"text/html; charset=utf-8\")\n")

		// Emit local variable declarations for state.
		// Merge document-level and window-level data.
		win := windows[route.WindowIdx]
		emitLocalVars(&b, req.Doc.Data)
		emitLocalVars(&b, win.Data)

		// Write HTML document wrapper.
		title := route.Title
		if title == "" {
			title = route.Name
		}
		fmt.Fprintf(&b, "\tfmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title></head><body>`)\n", title)

		// Render body via platform callback.
		bodyCode := req.RenderHTML(i)
		b.WriteString(bodyCode)

		b.WriteString("\tfmt.Fprint(w, `</body></html>`)\n")
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

// emitLocalVars writes local variable declarations for data fields.
func emitLocalVars(b *strings.Builder, data []*ast.Data) {
	for _, d := range data {
		if d.Extern || d.IsFunc {
			continue
		}
		initVal := LiteralToGo(d.Init)
		fmt.Fprintf(b, "\t%s := %s\n", d.Name, initVal)
		fmt.Fprintf(b, "\t_ = %s\n", d.Name)
	}
}
