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
	a := AnalyzeChecked(content, filename, fsys, dir, resolve)
	return a.Doc, a.Diags
}

// Analysis is what checking one file yields: its parse, the package it was
// checked into -- held even where the check reported errors, since that is
// most of the time an editor asks -- and the file's diagnostics.
type Analysis struct {
	Doc   *ast.Document
	Pkg   *ir.Package
	Diags []Diagnostic
}

// AnalyzeChecked is Analyze keeping the checked package.
func AnalyzeChecked(content, filename string, fsys fs.FS, dir string, resolve checker.ImportResolver) Analysis {
	return AnalyzePackageChecked(content, filename, nil, &checker.Config{
		FS:       fsys,
		Dir:      dir,
		Resolver: resolve,
		IsMain:   true,
	})
}

// AnalyzePackage is Analyze for a file checked beside the other files of its
// package, under cfg. Only the file's own diagnostics are reported.
func AnalyzePackage(content, filename string, siblings []*ast.Document, cfg *checker.Config) (*ast.Document, []Diagnostic) {
	a := AnalyzePackageChecked(content, filename, siblings, cfg)
	return a.Doc, a.Diags
}

// AnalyzePackageChecked is AnalyzePackage keeping the checked package.
func AnalyzePackageChecked(content, filename string, siblings []*ast.Document, cfg *checker.Config) Analysis {
	doc, parseErr := parser.Parse(filename, []byte(content))
	var pkg *ir.Package

	var diags []Diagnostic

	if parseErr != nil {
		diags = append(diags, ParseErrorsToDiagnostics(filename, parseErr)...)
	}

	if doc != nil {
		var checkDiags []ir.Diagnostic
		pkg, checkDiags = checker.CheckPackage(append([]*ast.Document{doc}, siblings...), cfg)
		for _, d := range checkDiags {
			msg := d.Msg
			rng := Range{Start: Position{}, End: Position{}}
			var file string
			if d.Pos.File != "" && d.Pos.File != filename {
				// Another file's line numbers mean nothing in this one.
				msg = d.Pos.String() + ": " + msg
				file = d.Pos.File
			} else if d.Pos.IsValid() {
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
				Message:  msg,
				File:     file,
			})
		}
	}

	return Analysis{Doc: doc, Pkg: pkg, Diags: diags}
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
