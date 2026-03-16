package lspcore

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// Analyze parses and type-checks SNGL source, returning the document and diagnostics.
// Pass dir="" and resolve=nil when no filesystem is available (e.g. playground).
func Analyze(content, filename, dir string, resolve checker.ImportResolver) (*ast.Document, []Diagnostic) {
	doc, parseErr := snglparser.Parse(filename, strings.NewReader(content))

	var diags []Diagnostic

	if parseErr != nil {
		diags = append(diags, ParseErrorsToDiagnostics(filename, parseErr)...)
	}

	if doc != nil {
		_, checkDiags := checker.CheckDiagnostics(doc, dir, resolve)
		for _, d := range checkDiags {
			rng := Range{Start: Position{}, End: Position{}}
			if d.Pos.IsValid() {
				rng = AstPosToRange(d.Pos)
			}
			diags = append(diags, Diagnostic{
				Range:    rng,
				Severity: SeverityError,
				Source:   "sngl",
				Message:  d.Msg,
			})
		}
	}

	return doc, diags
}

// ParseErrorsToDiagnostics converts parse error strings into diagnostics.
func ParseErrorsToDiagnostics(filename string, err error) []Diagnostic {
	var diags []Diagnostic
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		d := ParseOneDiagnostic(filename, line)
		diags = append(diags, d)
	}
	return diags
}

// ParseOneDiagnostic parses a single "file:line:col: msg" error string.
func ParseOneDiagnostic(filename, s string) Diagnostic {
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
			Range:    AstPosToRange(pos),
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
