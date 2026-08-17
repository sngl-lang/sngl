package testutil

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

var directiveRE = regexp.MustCompile(`//\s*ERROR\((\w+)\)\s+("(?:[^"\\]|\\.)*")`)
var foldRE = regexp.MustCompile(`//\s*FOLD\s+(.+)`)

// ErrorDirective represents a // ERROR(phase) "substring" comment in a test fixture.
type ErrorDirective struct {
	Phase     string // "parse", "check", "compile"
	Substring string
	Line      int // 1-based line number where the directive appears
}

// FoldDirective represents a // FOLD value comment on a data or computed line.
// The value is written in SNGL literal syntax: "string", 42, 3.14, true, false.
type FoldDirective struct {
	Expected any    // typed value parsed from SNGL literal syntax
	Raw      string // original text for error messages
	Line     int    // 1-based line number
}

// ParseFoldDirectives scans a file for // FOLD value comments.
func ParseFoldDirectives(path string) ([]FoldDirective, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var dirs []FoldDirective
	s := bufio.NewScanner(f)
	lineNum := 0
	for s.Scan() {
		lineNum++
		if m := foldRE.FindStringSubmatch(s.Text()); m != nil {
			raw := strings.TrimSpace(m[1])
			val, err := parseSNGLLiteral(raw)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: FOLD directive: %w", path, lineNum, err)
			}
			dirs = append(dirs, FoldDirective{Expected: val, Raw: raw, Line: lineNum})
		}
	}
	return dirs, s.Err()
}

// parseSNGLLiteral parses a SNGL literal value: "string", 42, 3.14, true, false.
func parseSNGLLiteral(s string) (any, error) {
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return strconv.Unquote(s)
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("unrecognized SNGL literal: %s", s)
}

// ParseDirectives scans a file for // ERROR(phase) "substring" comments.
func ParseDirectives(path string) ([]ErrorDirective, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var dirs []ErrorDirective
	s := bufio.NewScanner(f)
	lineNum := 0
	for s.Scan() {
		lineNum++
		if m := directiveRE.FindStringSubmatch(s.Text()); m != nil {
			sub, err := strconv.Unquote(m[2])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: ERROR directive: %w", path, lineNum, err)
			}
			dirs = append(dirs, ErrorDirective{Phase: m[1], Substring: sub, Line: lineNum})
		}
	}
	return dirs, s.Err()
}

// Filter returns directives matching the given phase.
func Filter(dirs []ErrorDirective, phase string) []ErrorDirective {
	var out []ErrorDirective
	for _, d := range dirs {
		if d.Phase == phase {
			out = append(out, d)
		}
	}
	return out
}

// AssertDiagnostics matches directives against the diagnostic stream.
// Phase "lint" filters to warning-severity; all other phases filter to errors.
// Only directives matching the supplied phase are checked.
func AssertDiagnostics(t testing.TB, diags []ir.Diagnostic, dirs []ErrorDirective, phase string) {
	t.Helper()
	targetSeverity := ir.Error
	if phase == "lint" {
		targetSeverity = ir.Warning
	}
	relevant := Filter(dirs, phase)
	if len(relevant) == 0 {
		return
	}
	for _, exp := range relevant {
		found := false
		for _, d := range diags {
			if d.Severity != targetSeverity {
				continue
			}
			if d.Pos.Line == exp.Line && strings.Contains(d.Msg, exp.Substring) {
				found = true
				break
			}
		}
		if !found {
			var got strings.Builder
			for _, d := range diags {
				if d.Severity == targetSeverity {
					fmt.Fprintf(&got, "\n  %s", d.Error())
				}
			}
			t.Errorf("line %d: expected %s diagnostic containing %q, got:%s",
				exp.Line, phase, exp.Substring, got.String())
		}
	}
}

// ParseFile opens and parses a .sngl file.
func ParseFile(path string) (*ast.Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	return parser.Parse(name, src)
}
