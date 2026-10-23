package testutil

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var navRE = regexp.MustCompile(`//\s*(DEFINITION|REFERENCES)\(([^)]+)\)\s+"((?:[^"\\]|\\.)*)"`)
var navFileRE = regexp.MustCompile(`^([\w./-]+\.sngl): `)

// NavDirective is a parsed // DEFINITION(target) "expected" or
// // REFERENCES(target) "L:C L:C ..." comment. A target is found as a hover
// target is -- `@L:C`, or the first occurrence of its text -- and a `^` in it
// marks the cursor within the text, which is otherwise at its start.
//
// DEFINITION expects the text the declaration's name is written in, with a
// `^` at the name, and searched for the same way; a `file.sngl: ` prefix
// names the library file, relative to lib/, to search instead of the
// fixture. REFERENCES expects every position that names the same
// declaration, its declaration's included.
type NavDirective struct {
	Kind      string // DEFINITION or REFERENCES
	Target    string
	Line, Col int // the cursor, 1-based, Col in runes
	DirLine   int
	// DEFINITION: where the name is expected, in File ("" is the fixture).
	File              string
	WantLine, WantCol int
	// REFERENCES: line, col pairs.
	WantRefs [][2]int
}

// ParseNavDirectives reads the DEFINITION and REFERENCES directives of the
// fixture at path. lib reads a library file by its name relative to lib/.
func ParseNavDirectives(path string, lib func(string) (string, bool)) ([]NavDirective, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var dirs []NavDirective
	for i, l := range lines {
		m := navRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		dirLine := i + 1
		want, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %s: invalid string: %w", path, dirLine, m[1], err)
		}
		d := NavDirective{Kind: m[1], DirLine: dirLine, Target: m[2]}
		d.Line, d.Col, err = caretTarget(strings.TrimSpace(m[2]), lines, dirLine)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %s(%s): %w", path, dirLine, m[1], m[2], err)
		}
		switch d.Kind {
		case "DEFINITION":
			in := lines
			if f := navFileRE.FindStringSubmatch(want); f != nil {
				d.File = f[1]
				want = strings.TrimPrefix(want, f[0])
				text, ok := lib(d.File)
				if !ok {
					return nil, fmt.Errorf("%s:%d: no library file %s", path, dirLine, d.File)
				}
				in = strings.Split(text, "\n")
			}
			skip := dirLine
			if d.File != "" {
				skip = 0
			}
			d.WantLine, d.WantCol, err = caretTarget(want, in, skip)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: DEFINITION expectation: %w", path, dirLine, err)
			}
		case "REFERENCES":
			for f := range strings.FieldsSeq(want) {
				ln, col, ok := strings.Cut(f, ":")
				l, err1 := strconv.Atoi(ln)
				c, err2 := strconv.Atoi(col)
				if !ok || err1 != nil || err2 != nil {
					return nil, fmt.Errorf("%s:%d: REFERENCES expects L:C positions, got %q", path, dirLine, f)
				}
				d.WantRefs = append(d.WantRefs, [2]int{l, c})
			}
		}
		dirs = append(dirs, d)
	}
	return dirs, nil
}

// caretTarget resolves target as resolveTarget does, then moves to its `^`,
// counting columns in runes as the compiler does.
func caretTarget(target string, lines []string, dirLine int) (int, int, error) {
	text, after, hasCaret := strings.Cut(target, "^")
	whole := text + after
	line, col, err := resolveTarget(whole, lines, dirLine)
	if err != nil || hoverPosRE.MatchString(whole) {
		return line, col, err
	}
	l := lines[line-1]
	runeCol := utf8.RuneCountInString(l[:col-1]) + 1
	if hasCaret {
		runeCol += utf8.RuneCountInString(text)
	}
	return line, runeCol, nil
}
