package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// analyze parses and type-checks a file, returning LSP diagnostics.
func (s *Server) analyze(fs *fileState) []Diagnostic {
	uri := fs.URI
	filename := uriToPath(uri)
	content := fs.Content

	r := strings.NewReader(content)

	var doc *ast.Document
	var parseErr error
	doc, parseErr = snglparser.Parse(filename, r)

	var diags []Diagnostic

	if parseErr != nil {
		diags = append(diags, parseErrorsToDiagnostics(filename, parseErr)...)
	}

	if doc != nil {
		fs.Doc = doc
		dir := filepath.Dir(filename)
		_, checkDiags := checker.CheckDiagnostics(doc, dir, checker.DefaultResolver())
		for _, d := range checkDiags {
			rng := Range{Start: Position{}, End: Position{}}
			if d.Pos.IsValid() {
				rng = astPosToRange(d.Pos)
			}
			diags = append(diags, Diagnostic{
				Range:    rng,
				Severity: SeverityError,
				Source:   "sngl",
				Message:  d.Msg,
			})
		}
	}

	return diags
}

// parseErrorsToDiagnostics converts parse error strings into LSP diagnostics.
// Parse errors have the format "filename:line:col: msg" separated by newlines.
func parseErrorsToDiagnostics(filename string, err error) []Diagnostic {
	var diags []Diagnostic
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		d := parseOneDiagnostic(filename, line)
		diags = append(diags, d)
	}
	return diags
}

func parseOneDiagnostic(filename, s string) Diagnostic {
	// Strip optional filename prefix
	prefix := filename + ":"
	if strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}

	var line, col int
	n, _ := fmt.Sscanf(s, "%d:%d: ", &line, &col)
	if n == 2 {
		pfx := fmt.Sprintf("%d:%d: ", line, col)
		msg := s
		if len(s) > len(pfx) {
			msg = s[len(pfx):]
		}
		pos := ast.Pos{Line: line, Column: col}
		return Diagnostic{
			Range:    astPosToRange(pos),
			Severity: SeverityError,
			Source:   "sngl",
			Message:  msg,
		}
	}

	return Diagnostic{
		Range:    Range{Start: Position{}, End: Position{}},
		Severity: SeverityError,
		Source:   "sngl",
		Message:  s,
	}
}

func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		u, err := url.Parse(uri)
		if err == nil {
			return u.Path
		}
	}
	return uri
}

func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs
}
