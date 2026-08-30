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

var directiveRE = regexp.MustCompile(`//\s*ERROR\((\w+)(?::(\d+)(?::(\d+))?)?\)\s+("(?:[^"\\]|\\.)*")`)
var foldRE = regexp.MustCompile(`//\s*FOLD\s+(.+)`)

// ErrorDirective represents a // ERROR(phase) "substring" comment in a test
// fixture. The diagnostic is expected on the line the comment sits on, which
// is the usual case and needs saying nowhere.
//
// `// ERROR(phase:LINE) "..."` and `// ERROR(phase:LINE:COL) "..."` name the
// position instead, for a diagnostic that cannot be reported where a comment
// can be written: an unterminated string swallows everything after it, so a
// comment on its line is inside the string rather than after it.
type ErrorDirective struct {
	Phase     string // "parse", "check", "compile"
	Substring string
	Line      int // 1-based line the directive comment appears on
	AtLine    int // expected diagnostic line; 0 means "the line above"
	AtCol     int // expected diagnostic column; 0 means "anywhere on the line"
}

// Pos is the line the diagnostic is expected on: the directive's own line
// unless it named another.
func (d ErrorDirective) Pos() int {
	if d.AtLine != 0 {
		return d.AtLine
	}
	return d.Line
}

// FoldDirective represents a // FOLD value comment on a data or computed line.
// The value is written in SNGL literal syntax: "string", 42, 3.14, true, false.
type FoldDirective struct {
	Expected any    // typed value parsed from SNGL literal syntax
	Raw      string // original text for error messages
	Line     int    // 1-based line number
}

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
			sub, err := strconv.Unquote(m[4])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: ERROR directive: %w", path, lineNum, err)
			}
			atLine, _ := strconv.Atoi(m[2])
			atCol, _ := strconv.Atoi(m[3])
			dirs = append(dirs, ErrorDirective{
				Phase: m[1], Substring: sub, Line: lineNum,
				AtLine: atLine, AtCol: atCol,
			})
		}
	}
	return dirs, s.Err()
}

// nofmtRE matches a `// NOFMT "reason"` directive, which exempts a fixture
// from the check that it is written the way `sngl fmt` writes it.
var nofmtRE = regexp.MustCompile(`//\s*NOFMT\b\s*(".*")`)

// ParseNoFmt reports whether a fixture carries a NOFMT directive, and the
// reason written with it. A fixture that says something the formatter would
// rewrite — an odd layout a test is about, or output the formatter cannot
// reproduce yet — opts out here rather than by weakening the check.
func ParseNoFmt(path string) (bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		m := nofmtRE.FindStringSubmatch(s.Text())
		if m == nil {
			continue
		}
		if m[1] == "" {
			return true, "", nil
		}
		reason, err := strconv.Unquote(m[1])
		if err != nil {
			return false, "", fmt.Errorf("%s: NOFMT directive: %w", path, err)
		}
		return true, reason, nil
	}
	return false, "", s.Err()
}

func Filter(dirs []ErrorDirective, phase string) []ErrorDirective {
	var out []ErrorDirective
	for _, d := range dirs {
		if d.Phase == phase {
			out = append(out, d)
		}
	}
	return out
}

// Phase "lint" filters to warning-severity; every other phase filters to
// errors. Only directives matching the supplied phase are checked.
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

func ParseFile(path string) (*ast.Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	return parser.Parse(name, src)
}
