package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// DocBlock extracts the full doc comment for a declaration at the given line,
// preserving line structure. Returns the doc text with leading "// " stripped
// from each line.
func DocBlock(comments []ast.Comment, line int) []string {
	var lines []string
	target := line - 1
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Pos.Line == target {
			text := c.Text
			text = strings.TrimPrefix(text, "// ")
			text = strings.TrimPrefix(text, "//")
			if strings.HasPrefix(strings.TrimSpace(text), "---") {
				break
			}
			lines = append([]string{text}, lines...)
			target--
		} else if c.Pos.Line < target {
			break
		}
	}
	return lines
}

// ExtractExample extracts an example SNGL app from a doc comment block.
// The example starts with a line containing just "Example:" and is followed
// by indented SNGL code (lines starting with at least 2 spaces). The example
// ends at the first non-indented, non-empty line or end of doc block.
//
// Returns the SNGL source with indentation stripped, or "" if no example found.
func ExtractExample(docLines []string) string {
	inExample := false
	var exampleLines []string

	for _, line := range docLines {
		trimmed := strings.TrimSpace(line)
		if !inExample {
			if trimmed == "Example:" {
				inExample = true
				continue
			}
			continue
		}

		// In example section
		if trimmed == "" {
			// Blank line in example — keep it if we already have content
			if len(exampleLines) > 0 {
				exampleLines = append(exampleLines, "")
			}
			continue
		}

		// Check for indentation (at least 2 spaces)
		if strings.HasPrefix(line, "  ") {
			// Strip the common indent (find minimum)
			exampleLines = append(exampleLines, line)
		} else {
			// Non-indented line ends the example
			break
		}
	}

	if len(exampleLines) == 0 {
		return ""
	}

	// Strip common leading whitespace
	minIndent := len(exampleLines[0])
	for _, l := range exampleLines {
		if l == "" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if indent < minIndent {
			minIndent = indent
		}
	}
	for i, l := range exampleLines {
		if len(l) > minIndent {
			exampleLines[i] = l[minIndent:]
		}
	}

	// Trim trailing blank lines
	for len(exampleLines) > 0 && exampleLines[len(exampleLines)-1] == "" {
		exampleLines = exampleLines[:len(exampleLines)-1]
	}

	return strings.Join(exampleLines, "\n")
}

// ComponentExamples extracts all component name → example SNGL source pairs
// from a parsed document's comments.
func ComponentExamples(doc *ast.Document) map[string]string {
	examples := make(map[string]string)

	for _, comp := range doc.Components {
		lines := DocBlock(doc.Comments, comp.Pos.Line)
		if ex := ExtractExample(lines); ex != "" {
			examples[comp.Name] = ex
		}
	}

	return examples
}

// StdlibExamples parses the embedded stdlib and extracts component examples.
func StdlibExamples() (map[string]string, error) {
	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, err
	}

	examples := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sngl") {
			continue
		}
		data, err := stdlibFS.ReadFile("stdlib/" + entry.Name())
		if err != nil {
			continue
		}
		doc, err := parser.Parse(entry.Name(), strings.NewReader(string(data)))
		if err != nil {
			continue
		}
		for name, ex := range ComponentExamples(doc) {
			examples[name] = ex
		}
	}

	return examples, nil
}
