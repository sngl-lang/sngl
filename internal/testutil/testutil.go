package testutil

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
	"git.duckfam.us/jonathan/sngl/snglparser"
)

var directiveRE = regexp.MustCompile(`//\s*ERROR\((\w+)\)\s+"([^"]+)"`)
var posLineRE = regexp.MustCompile(`^\d+:\d+:`)

// ErrorDirective represents a // ERROR(phase) "substring" comment in a test fixture.
type ErrorDirective struct {
	Phase     string // "parse", "check", "compile"
	Substring string
	Line      int // 1-based line number where the directive appears
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

// ParseFile opens and parses a .sngl.kdl or .sngl file using the appropriate parser.
func ParseFile(path string) (*ast.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(path)
	if strings.HasSuffix(strings.ToLower(path), ".sngl.kdl") {
		return parser.Parse(name, f)
	}
	return snglparser.Parse(name, f)
}

// RunFixtures globs dir for *.sngl.kdl files, creates a subtest per file,
// parses directives, and calls fn.
func RunFixtures(t *testing.T, dir string, fn func(t *testing.T, path string, dirs []ErrorDirective)) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl.kdl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *.sngl.kdl files found in %s", dir)
	}
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl.kdl")
		t.Run(name, func(t *testing.T) {
			dirs, err := ParseDirectives(path)
			if err != nil {
				t.Fatalf("parse directives: %v", err)
			}
			fn(t, path, dirs)
		})
	}
}
