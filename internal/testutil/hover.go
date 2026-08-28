package testutil

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var hoverRE = regexp.MustCompile(`//\s*HOVER(-NOT)?\(([^)]+)\)\s+"((?:[^"\\]|\\.)*)"`)
var hoverPosRE = regexp.MustCompile(`^@(\d+):(\d+)$`)

// HoverDirective is a parsed // HOVER(target) "substring" comment.
type HoverDirective struct {
	Target    string // raw target text inside the parens
	Substring string // expected substring of hover output (or required-absent if Negate)
	Negate    bool   // true for HOVER-NOT
	Line      int    // 1-based line of the hover position
	Col       int    // 1-based column of the hover position
	DirLine   int    // 1-based line of the directive comment itself (for error messages)
}

// Errors if any directive target cannot be located in the source.
func ParseHoverDirectives(path string) ([]HoverDirective, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	var dirs []HoverDirective
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		m := hoverRE.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		negate := m[1] == "-NOT"
		target := strings.TrimSpace(m[2])
		substring, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: HOVER directive: invalid string: %w", path, lineNum, err)
		}
		line, col, err := resolveTarget(target, lines, lineNum)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: HOVER(%s): %w", path, lineNum, target, err)
		}
		dirs = append(dirs, HoverDirective{
			Target:    target,
			Substring: substring,
			Negate:    negate,
			Line:      line,
			Col:       col,
			DirLine:   lineNum,
		})
	}
	return dirs, scanner.Err()
}

// resolveTarget finds the 1-based source position for a directive target.
// @L:C → explicit. Otherwise: first occurrence of target text in source,
// skipping comment lines (to avoid the directive line matching itself).
func resolveTarget(target string, lines []string, dirLine int) (int, int, error) {
	if m := hoverPosRE.FindStringSubmatch(target); m != nil {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return line, col, nil
	}
	for i, l := range lines {
		lineNum := i + 1
		if lineNum == dirLine {
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
		return lineNum, idx + 1, nil
	}
	return 0, 0, fmt.Errorf("target %q not found in source", target)
}
