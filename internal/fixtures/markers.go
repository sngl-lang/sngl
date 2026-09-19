package fixtures

// LSP marker parsing, moved from internal/lspcore's test package.

import (
	"regexp"
	"strings"
)

// marker represents a single //@ directive in a test file.
type marker struct {
	line int    // 1-based
	kind string // "diag", "hover", "complete"
	args []string
}

// parseMarkers extracts //@ directives from file content.
func parseMarkers(content string) []marker {
	var markers []marker
	for i, line := range strings.Split(content, "\n") {
		m := markerRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		markers = append(markers, marker{
			line: i + 1,
			kind: m[1],
			args: parseArgs(m[2]),
		})
	}
	return markers
}

// parseArgs splits a comma-separated list of quoted strings.

var markerRE = regexp.MustCompile(`//@\s+(\w+)\((.+)\)\s*$`)

func parseArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == ',' && !inQuote {
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}
