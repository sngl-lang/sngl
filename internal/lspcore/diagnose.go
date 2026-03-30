package lspcore

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// Analyze parses and type-checks SNGL source, returning the document and diagnostics.
// Pass dir="" and resolve=nil when no filesystem is available (e.g. playground).
func Analyze(content, filename, dir string, resolve checker.ImportResolver, schemeResolve ...checker.SchemeResolver) (*ast.Document, []Diagnostic) {
	doc, parseErr := snglparser.Parse(filename, strings.NewReader(content))

	var diags []Diagnostic

	if parseErr != nil {
		diags = append(diags, ParseErrorsToDiagnostics(filename, parseErr)...)
	}

	var sr checker.SchemeResolver
	if len(schemeResolve) > 0 {
		sr = schemeResolve[0]
	}

	if doc != nil {
		_, checkDiags := checker.CheckDiagnostics(doc, dir, resolve, sr, buildLSPAPIConfig(doc))
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

// buildLSPAPIConfig builds an APIConfig from the document's output declarations
// by looking up registered lang/platform providers.
func buildLSPAPIConfig(doc *ast.Document) *checker.APIConfig {
	if len(doc.Outputs) == 0 {
		return nil
	}
	cfg := &checker.APIConfig{Namespaces: map[string]*ast.Document{}}
	seen := map[string]bool{}
	for _, out := range doc.Outputs {
		if !seen[out.Lang] {
			seen[out.Lang] = true
			if lang := codegen.LookupLang(out.Lang); lang != nil {
				if ap, ok := lang.(codegen.APIProvider); ok {
					cfg.Namespaces[out.Lang] = ap.API()
				}
			}
		}
		if !seen[out.Platform] {
			seen[out.Platform] = true
			if plat := codegen.LookupPlatform(out.Platform); plat != nil {
				if ap, ok := plat.(codegen.APIProvider); ok {
					cfg.Namespaces[out.Platform] = ap.API()
				}
			}
		}
	}
	if len(cfg.Namespaces) == 0 {
		return nil
	}
	return cfg
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
