package lspcore

import (
	"fmt"
	"io/fs"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// Analyze parses and type-checks SNGL source, returning the document and diagnostics.
// Pass dir="" and resolve=nil when no filesystem is available.
func Analyze(content, filename string, fsys fs.FS, dir string, resolve checker.ImportResolver) (*ast.Document, []Diagnostic) {
	return AnalyzePackage(content, filename, nil, &checker.Config{
		FS:       fsys,
		Dir:      dir,
		Resolver: resolve,
		IsMain:   true,
	})
}

// AnalyzePackage is Analyze for a file checked beside the other files of its
// package, under cfg. Only the file's own diagnostics are reported.
func AnalyzePackage(content, filename string, siblings []*ast.Document, cfg *checker.Config) (*ast.Document, []Diagnostic) {
	doc, parseErr := parser.Parse(filename, []byte(content))

	var diags []Diagnostic

	if parseErr != nil {
		diags = append(diags, ParseErrorsToDiagnostics(filename, parseErr)...)
	}

	if doc != nil {
		_, checkDiags := checker.CheckPackage(append([]*ast.Document{doc}, siblings...), cfg)
		for _, d := range checkDiags {
			if len(siblings) > 0 && d.Pos.File != "" && d.Pos.File != filename {
				continue
			}
			rng := Range{Start: Position{}, End: Position{}}
			if d.Pos.IsValid() {
				rng = AstPosToRange(d.Pos)
			}
			sev := SeverityError
			if d.Severity == ir.Warning {
				sev = SeverityWarning
			}
			diags = append(diags, Diagnostic{
				Range:    rng,
				Severity: sev,
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
	for line := range strings.SplitSeq(err.Error(), "\n") {
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
