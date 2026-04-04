package lspcore

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// Hover returns markdown hover info for the word at the given 1-based position.
func Hover(content string, doc *ast.Document, line, col int) string {
	if doc == nil {
		return ""
	}
	word := WordAtPosition(content, line, col)
	if word == "" {
		return ""
	}
	return HoverInfo(doc, word)
}

func WordAtPosition(content string, line, col int) string {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	l := lines[line-1]
	runes := []rune(l)
	if col < 1 || col > len(runes)+1 {
		return ""
	}

	idx := col - 1
	start := idx
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}
	end := idx
	for end < len(runes) && isIdentRune(runes[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return string(runes[start:end])
}

func isIdentRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func HoverInfo(doc *ast.Document, word string) string {
	for _, d := range doc.Data {
		if d.Name == word {
			hint := d.Init.TypeHint
			if hint == "" {
				hint = "dyn"
			}
			if d.Extern {
				return fmt.Sprintf("```sngl\nvar %s %s extern\n```", d.Name, hint)
			}
			return fmt.Sprintf("```sngl\nvar %s %s\n```", d.Name, hint)
		}
	}

	for _, fn := range doc.Functions {
		if fn.Name == word && fn.Block == nil && len(fn.Params) == 0 && !fn.IsStdlib {
			return fmt.Sprintf("```sngl\nfunc %s()\n```", fn.Name)
		}
	}

	for _, c := range doc.Consts {
		if c.Name == word {
			return fmt.Sprintf("```sngl\nconst %s\n```", c.Name)
		}
	}

	for _, c := range doc.Components {
		if c.Name == word {
			return formatComponentHoverWithDoc(c, doc)
		}
	}

	for _, c := range doc.ImportedComponents {
		if c.Name == word {
			return formatComponentHoverWithDoc(c, doc)
		}
	}

	registry, styleProps, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return ""
	}

	if schema, ok := registry[word]; ok {
		return formatSchemaHover(word, schema)
	}

	if sp, ok := styleProps[word]; ok {
		info := fmt.Sprintf("**style property** `%s`\n\nType: `%s`", word, sp.Type)
		if len(sp.Enum) > 0 {
			info += fmt.Sprintf("\n\nValues: %s", strings.Join(sp.Enum, ", "))
		}
		return info
	}

	for _, s := range doc.Structs {
		if s.Name == word {
			return formatStructHover(s)
		}
	}

	for _, e := range doc.Enums {
		if e.Name == word {
			return fmt.Sprintf("```sngl\nenum %s { %s }\n```", e.Name, strings.Join(e.Values, ", "))
		}
	}

	return ""
}

func formatComponentHover(c *ast.Component) string {
	return formatComponentHoverWithDoc(c, nil)
}

func formatComponentHoverWithDoc(c *ast.Component, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s\n```\n", c.Name)
	if doc != nil {
		if d := docForPos(doc, c.Pos); d != "" {
			fmt.Fprintf(&sb, "\n%s\n", d)
		}
	}
	if len(c.Params) > 0 {
		sb.WriteString("\n**Params:**\n")
		for _, p := range c.Params {
			hint := p.Default.TypeHint
			if hint == "" {
				hint = "dyn"
			}
			if p.Required {
				fmt.Fprintf(&sb, "- `%s` %s (required)\n", p.Name, hint)
			} else {
				fmt.Fprintf(&sb, "- `%s` %s\n", p.Name, hint)
			}
		}
	}
	return sb.String()
}

// docForPos extracts a doc comment immediately preceding the given position.
// It looks for contiguous // comment lines ending on the line before pos.Line.
func docForPos(doc *ast.Document, pos ast.Pos) string {
	if doc == nil || len(doc.Comments) == 0 {
		return ""
	}
	targetLine := pos.Line
	var lines []string
	// Gather contiguous comment lines ending at targetLine-1
	for i := len(doc.Comments) - 1; i >= 0; i-- {
		c := doc.Comments[i]
		if c.Block {
			continue
		}
		if c.Pos.Line == targetLine-1-len(lines) {
			text := strings.TrimPrefix(c.Text, "//")
			text = strings.TrimPrefix(text, " ")
			lines = append(lines, text)
		} else if len(lines) > 0 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	// Reverse since we collected bottom-up
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

func formatSchemaHover(name string, schema *checker.ComponentSchema) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s  // stdlib\n```\n", name)
	if schema.Doc != "" {
		fmt.Fprintf(&sb, "\n%s\n", schema.Doc)
	}
	if len(schema.Props) > 0 {
		sb.WriteString("\n**Props:**\n")
		propNames := make([]string, 0, len(schema.Props))
		for pname := range schema.Props {
			propNames = append(propNames, pname)
		}
		sort.Strings(propNames)
		for _, pname := range propNames {
			ps := schema.Props[pname]
			if ps.Doc != "" {
				fmt.Fprintf(&sb, "- `%s` %s — %s\n", pname, ps.Type, ps.Doc)
			} else {
				fmt.Fprintf(&sb, "- `%s` %s\n", pname, ps.Type)
			}
			if len(ps.Enum) > 0 {
				fmt.Fprintf(&sb, "  Values: %s\n", strings.Join(ps.Enum, ", "))
			}
		}
	}
	if len(schema.Events) > 0 {
		sb.WriteString("\n**Events:**\n")
		eventNames := make([]string, 0, len(schema.Events))
		for ename := range schema.Events {
			eventNames = append(eventNames, ename)
		}
		sort.Strings(eventNames)
		for _, ename := range eventNames {
			etype := schema.Events[ename]
			fmt.Fprintf(&sb, "- `@%s` (%s)\n", ename, etype)
		}
	}
	return sb.String()
}

func formatStructHover(s *ast.StructDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nstruct %s {\n", s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&sb, "    %s %s\n", f.Name, f.Type)
	}
	sb.WriteString("}\n```")
	return sb.String()
}
