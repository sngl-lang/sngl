package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
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

// DocText joins doc block lines into prose text, stopping before the Example section.
func DocText(docLines []string) string {
	var out []string
	for _, line := range docLines {
		if strings.TrimSpace(line) == "Example:" {
			break
		}
		out = append(out, line)
	}
	// Trim trailing blank lines
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}

// DeclDoc extracts the doc comment text for any declaration at the given line.
func DeclDoc(comments []ast.Comment, line int) string {
	return DocText(DocBlock(comments, line))
}

// PackageDocs extracts documentation for all declarations in a document.
type PackageDocs struct {
	Components []DeclInfo
	Structs    []DeclInfo
	Enums      []DeclInfo
	Data       []DeclInfo
	Functions  []DeclInfo
	Consts     []DeclInfo
}

// DeclInfo holds a declaration name with its doc comment.
type DeclInfo struct {
	Name string
	Doc  string
	Decl any // the underlying AST node
}

// ExtractPackageDocs extracts docs for all declarations in a document.
func ExtractPackageDocs(doc *ast.Document) *PackageDocs {
	pd := &PackageDocs{}

	for _, comp := range doc.Components {
		pd.Components = append(pd.Components, DeclInfo{
			Name: comp.Name,
			Doc:  DeclDoc(doc.Comments, comp.Pos.Line),
			Decl: comp,
		})
	}
	for _, s := range doc.Structs {
		pd.Structs = append(pd.Structs, DeclInfo{
			Name: s.Name,
			Doc:  DeclDoc(doc.Comments, s.Pos.Line),
			Decl: s,
		})
	}
	for _, e := range doc.Enums {
		pd.Enums = append(pd.Enums, DeclInfo{
			Name: e.Name,
			Doc:  DeclDoc(doc.Comments, e.Pos.Line),
			Decl: e,
		})
	}
	for _, d := range doc.Data {
		pd.Data = append(pd.Data, DeclInfo{
			Name: d.Name,
			Doc:  DeclDoc(doc.Comments, d.Pos.Line),
			Decl: d,
		})
	}
	for _, fn := range doc.Functions {
		if fn.IsStdlib || fn.IsTest() {
			continue
		}
		pd.Functions = append(pd.Functions, DeclInfo{
			Name: fn.Name,
			Doc:  DeclDoc(doc.Comments, fn.Pos.Line),
			Decl: fn,
		})
	}
	for _, c := range doc.Consts {
		pd.Consts = append(pd.Consts, DeclInfo{
			Name: c.Name,
			Doc:  DeclDoc(doc.Comments, c.Pos.Line),
			Decl: c,
		})
	}

	return pd
}

// FindDecl searches for a named declaration across all types.
func (pd *PackageDocs) FindDecl(name string) *DeclInfo {
	for i := range pd.Components {
		if pd.Components[i].Name == name {
			return &pd.Components[i]
		}
	}
	for i := range pd.Structs {
		if pd.Structs[i].Name == name {
			return &pd.Structs[i]
		}
	}
	for i := range pd.Enums {
		if pd.Enums[i].Name == name {
			return &pd.Enums[i]
		}
	}
	for i := range pd.Data {
		if pd.Data[i].Name == name {
			return &pd.Data[i]
		}
	}
	for i := range pd.Functions {
		if pd.Functions[i].Name == name {
			return &pd.Functions[i]
		}
	}
	for i := range pd.Consts {
		if pd.Consts[i].Name == name {
			return &pd.Consts[i]
		}
	}
	return nil
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

// StdlibExamples reads example .sngl files from stdlib/examples/.
// Each file is named after the component (e.g., button.sngl) and contains
// a component main with a working example that the checker can validate.
func StdlibExamples() (map[string]string, error) {
	entries, err := stdlibFS.ReadDir("stdlib/examples")
	if err != nil {
		return nil, err
	}

	examples := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sngl") {
			continue
		}
		data, err := stdlibFS.ReadFile("stdlib/examples/" + entry.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".sngl")
		examples[name] = strings.TrimSpace(string(data))
	}

	return examples, nil
}
