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
)

var directiveRE = regexp.MustCompile(`//\s*ERROR\((\w+)\)\s+"([^"]+)"`)
var foldRE = regexp.MustCompile(`//\s*FOLD\s+(.+)`)
var posLineRE = regexp.MustCompile(`^\d+:\d+:`)

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

// AssertFolds checks that data and computed fields on directive lines were folded
// to the expected literal values after optimization. Both value and type must match.
func AssertFolds(t *testing.T, doc *ast.Document, folds []FoldDirective) {
	t.Helper()
	for _, fd := range folds {
		expr, name := findExprAtLine(doc, fd.Line)
		if expr == nil {
			t.Errorf("line %d: no data or computed field found for FOLD directive", fd.Line)
			continue
		}
		if expr.SNGL != nil {
			t.Errorf("line %d (%s): expression was not folded (SNGL still set)", fd.Line, name)
			continue
		}
		if expr.Literal != fd.Expected {
			t.Errorf("line %d (%s): expected %v (%T), got %v (%T)",
				fd.Line, name, fd.Expected, fd.Expected, expr.Literal, expr.Literal)
		}
	}
}

// findExprAtLine returns the Expr pointer and name for the data/computed/const
// declaration at the given line, searching both top-level and inside components.
func findExprAtLine(doc *ast.Document, line int) (*ast.Expr, string) {
	if e, name := searchScope(doc.Data, doc.Computeds, doc.Consts, line); e != nil {
		return e, name
	}
	for _, comp := range doc.Components {
		if e, name := searchScope(comp.Data, comp.Computeds, comp.Consts, line); e != nil {
			return e, name
		}
	}
	return nil, ""
}

func searchScope(data []*ast.Data, computeds []*ast.Computed, consts []*ast.Const, line int) (*ast.Expr, string) {
	for _, d := range data {
		if d.Pos.Line == line {
			return &d.Init, d.Name
		}
	}
	for _, c := range computeds {
		if c.Pos.Line == line {
			return &c.Expr, c.Name
		}
	}
	for _, c := range consts {
		if c.Pos.Line == line {
			return &c.Init, c.Name
		}
	}
	return nil, ""
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
			dirs = append(dirs, ErrorDirective{Phase: m[1], Substring: m[2], Line: lineNum})
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

// AssertErrors checks: if expected is empty, err must be nil;
// if expected is non-empty, err must be non-nil and each directive's substring
// must appear in an error line that starts with the directive's line number.
// For errors without position prefixes (e.g. "missing app node"), the directive
// matches if any error line contains the substring.
func AssertErrors(t *testing.T, err error, expected []ErrorDirective) {
	t.Helper()
	if len(expected) == 0 {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error containing %v, got nil", expected)
	}
	msg := err.Error()
	errLines := strings.Split(msg, "\n")
	for _, exp := range expected {
		found := false
		linePrefix := fmt.Sprintf("%d:", exp.Line)
		for _, line := range errLines {
			if strings.HasPrefix(line, linePrefix) && strings.Contains(line, exp.Substring) {
				found = true
				break
			}
		}
		// Fall back: match non-positional error lines (those not starting with "N:")
		// by substring only. This handles errors like "missing app node" that have
		// no source position.
		if !found {
			for _, line := range errLines {
				if !posLineRE.MatchString(line) && strings.Contains(line, exp.Substring) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("expected error at line %d containing %q, got:\n%s", exp.Line, exp.Substring, msg)
		}
	}
}

// ParseFile opens and parses a .sngl file.
func ParseFile(path string) (*ast.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(path)
	return parser.Parse(name, f)
}

// RunFixtures globs dir for *.sngl files, creates a subtest per file,
// parses directives, and calls fn.
func RunFixtures(t *testing.T, dir string, fn func(t *testing.T, path string, dirs []ErrorDirective)) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *.sngl files found in %s", dir)
	}
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			dirs, err := ParseDirectives(path)
			if err != nil {
				t.Fatalf("parse directives: %v", err)
			}
			fn(t, path, dirs)
		})
	}
}
