package testutil

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

type SNGLBlock struct {
	Source     string // raw code block content (before wrapping)
	Line       int    // 1-based line of first source line in the markdown file
	Annotation string // "", "component", "expression", "nocheck"
	Prelude    string // SNGL prelude from HTML comment body
	File       string // markdown file path
}

func ExtractSNGLBlocks(t testing.TB, path string) []SNGLBlock {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var blocks []SNGLBlock
	scanner := bufio.NewScanner(f)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	inBlock := false
	var current strings.Builder
	blockStart := 0
	var annotation string
	var prelude string

	for lineNum := 0; lineNum < len(lines); lineNum++ {
		line := lines[lineNum]
		if strings.TrimSpace(line) == "```sngl" {
			inBlock = true
			blockStart = lineNum + 2 // 1-based, next line
			current.Reset()
			annotation, prelude = FindAnnotation(lines, lineNum)
			continue
		}
		if inBlock && strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, SNGLBlock{
				Source:     current.String(),
				Line:       blockStart,
				Annotation: annotation,
				Prelude:    prelude,
				File:       path,
			})
			continue
		}
		if inBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	return blocks
}

// Looks backward from fenceLine for a <!-- SNGL-... --> comment, returning the
// annotation type and any prelude source.
func FindAnnotation(lines []string, fenceLine int) (annotation, prelude string) {
	i := fenceLine - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 {
		return "", ""
	}

	// Single-line annotation: <!-- SNGL-component -->
	prev := strings.TrimSpace(lines[i])
	if strings.HasPrefix(prev, "<!-- SNGL-") && strings.HasSuffix(prev, "-->") {
		inner := strings.TrimPrefix(prev, "<!-- SNGL-")
		inner = strings.TrimSuffix(inner, "-->")
		inner = strings.TrimSpace(inner)
		return inner, ""
	}

	// Multi-line: the line before the fence should be "-->"
	if prev != "-->" {
		return "", ""
	}

	var preludeLines []string
	for i = i - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if after, ok := strings.CutPrefix(trimmed, "<!-- SNGL-"); ok {
			inner := strings.TrimSpace(after)
			if before, after, ok := strings.Cut(inner, " "); ok {
				annotation = before
				preludeLines = append([]string{after}, preludeLines...)
			} else if before, ok := strings.CutSuffix(inner, "-->"); ok {
				annotation = strings.TrimSpace(before)
			} else {
				annotation = inner
			}
			var b strings.Builder
			for _, pl := range preludeLines {
				b.WriteString(pl)
				b.WriteByte('\n')
			}
			return annotation, b.String()
		}
		preludeLines = append([]string{lines[i]}, preludeLines...)
	}
	return "", ""
}

// UnwrapComponent extracts the inner body from a formatted "component snippet { ... }"
// and un-indents by one level (4 spaces).
func UnwrapComponent(formatted, prelude string) string {
	src := formatted
	if prelude != "" {
		preDoc, err := parser.Parse("prelude", []byte(prelude))
		if err == nil {
			fmtPre := parser.Format(preDoc)
			src = strings.TrimPrefix(formatted, fmtPre)
		}
	}

	lines := strings.Split(src, "\n")
	start := -1
	end := -1
	depth := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if start == -1 && strings.HasPrefix(trimmed, "component snippet") && strings.HasSuffix(trimmed, "{") {
			start = i + 1
			depth = 1
			continue
		}
		if start != -1 {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if start == -1 || end == -1 {
		return src
	}

	var b strings.Builder
	for _, line := range lines[start:end] {
		if len(line) >= 4 && line[:4] == "    " {
			b.WriteString(line[4:])
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
