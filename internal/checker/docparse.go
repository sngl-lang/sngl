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

// PrefixedExamples extracts example_* prefixed components from a parsed document.
// Components named example_<name> or example_<name>_<suffix> map to <name>.
// The first example per name wins when multiple suffixes exist.
func PrefixedExamples(doc *ast.Document) map[string]string {
	examples := make(map[string]string)
	for _, comp := range doc.Components {
		target, ok := strings.CutPrefix(comp.Name, "example_")
		if !ok {
			continue
		}
		if i := strings.Index(target, "_"); i >= 0 {
			target = target[:i]
		}
		if _, exists := examples[target]; exists {
			continue
		}
		display := *comp
		display.Name = target
		exDoc := &ast.Document{
			Components: []*ast.Component{&display},
			Decls:      []ast.Decl{&display},
		}
		examples[target] = strings.TrimSpace(parser.Format(exDoc))
	}
	return examples
}

// StdlibExamples finds example_ prefixed components in the stdlib and returns
// their formatted source keyed by the target component name. Components named
// example_<name> or example_<name>_<suffix> map to component <name>. Multiple
// examples per component are supported.
func StdlibExamples() (map[string][]string, error) {
	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, err
	}

	examples := map[string][]string{}
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
		for _, comp := range doc.Components {
			target, ok := strings.CutPrefix(comp.Name, "example_")
			if !ok {
				continue
			}
			// Strip optional suffix: example_button_click → button
			if i := strings.Index(target, "_"); i >= 0 {
				target = target[:i]
			}
			// Format the example component as standalone source.
			// Rename to strip the example_ prefix for display.
			display := *comp
			display.Name = target
			exDoc := &ast.Document{
				Components: []*ast.Component{&display},
				Decls:      []ast.Decl{&display},
			}
			src := strings.TrimSpace(parser.Format(exDoc))
			examples[target] = append(examples[target], src)
		}
	}

	return examples, nil
}
