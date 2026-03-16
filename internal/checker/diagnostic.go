package checker

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Diagnostic is a structured error with position information.
type Diagnostic struct {
	Pos ast.Pos
	Msg string
}

// CheckDiagnostics type-checks a document and returns structured diagnostics
// instead of a joined error string.
func CheckDiagnostics(doc *ast.Document, dir string, resolve ImportResolver) (*ast.Document, []Diagnostic) {
	registry, styleProps, _, err := LoadStdlib()
	if err != nil {
		return doc, []Diagnostic{{Msg: fmt.Sprintf("loading stdlib: %v", err)}}
	}

	c := &checker{
		registry:   registry,
		styleProps: styleProps,
		scope:      NewScope(nil),
		dir:        dir,
		resolve:    resolve,
		visited:    map[string]bool{},
	}

	if doc.App == nil {
		c.errorAt(ast.Pos{}, "missing app node")
		return doc, toDiagnostics(c.errs)
	}

	c.pass1(doc)
	c.pass2(doc)
	return doc, toDiagnostics(c.errs)
}

func toDiagnostics(errs []error) []Diagnostic {
	var diags []Diagnostic
	for _, e := range errs {
		diags = append(diags, parseDiagnostic(e.Error()))
	}
	return diags
}

// parseDiagnostic extracts position from "line:col: msg" formatted errors.
func parseDiagnostic(s string) Diagnostic {
	var line, col int
	var msg string
	// Try parsing "line:col: msg"
	n, _ := fmt.Sscanf(s, "%d:%d: ", &line, &col)
	if n == 2 {
		// Find the position after "line:col: "
		prefix := fmt.Sprintf("%d:%d: ", line, col)
		if len(s) > len(prefix) {
			msg = s[len(prefix):]
		} else {
			msg = s
		}
		return Diagnostic{Pos: ast.Pos{Line: line, Column: col}, Msg: msg}
	}
	return Diagnostic{Msg: s}
}
