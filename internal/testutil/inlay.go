package testutil

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var inlayRE = regexp.MustCompile(`//\s*INLAY(-NOT)?\(([^)]+)\)\s+"((?:[^"\\]|\\.)*)"`)
var inlayPosRE = regexp.MustCompile(`^@(\d+):(\d+)$`)

// InlayDirective is a parsed // INLAY(target) "label" comment.
type InlayDirective struct {
	Target    string // raw target text inside the parens
	Label     string // expected inlay hint label
	Negate    bool   // true for INLAY-NOT — asserts no hint at this position
	Line      int    // 1-based line of the target start
	Col       int    // 1-based column of the target start
	TargetLen int    // byte length of the resolved target text (0 for @line:col form)
	DirLine   int    // 1-based line of the directive comment (for error messages)
}

// ParseInlayDirectives scans a file for INLAY directives and resolves each
// target to a 1-based source position. Returns an error if any target cannot
// be located.
func ParseInlayDirectives(path string) ([]InlayDirective, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	var dirs []InlayDirective
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		m := inlayRE.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		negate := m[1] == "-NOT"
		target := strings.TrimSpace(m[2])
		label, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: INLAY directive: invalid string: %w", path, lineNum, err)
		}
		line, col, isExplicit, err := resolveInlayTarget(target, lines, lineNum)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: INLAY(%s): %w", path, lineNum, target, err)
		}
		tLen := len(target)
		if isExplicit {
			tLen = 0
		}
		dirs = append(dirs, InlayDirective{
			Target:    target,
			Label:     label,
			Negate:    negate,
			Line:      line,
			Col:       col,
			TargetLen: tLen,
			DirLine:   lineNum,
		})
	}
	return dirs, scanner.Err()
}

// resolveInlayTarget mirrors hover's resolveTarget but returns whether the
// target was an explicit @L:C.
func resolveInlayTarget(target string, lines []string, dirLine int) (line, col int, explicit bool, err error) {
	if m := inlayPosRE.FindStringSubmatch(target); m != nil {
		line, _ = strconv.Atoi(m[1])
		col, _ = strconv.Atoi(m[2])
		return line, col, true, nil
	}
	for i, l := range lines {
		ln := i + 1
		if ln == dirLine {
			continue
		}
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		idx := strings.Index(l, target)
		if idx < 0 {
			continue
		}
		return ln, idx + 1, false, nil
	}
	return 0, 0, false, fmt.Errorf("target %q not found in source", target)
}
