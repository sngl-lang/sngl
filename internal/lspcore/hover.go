package lspcore

import (
	"fmt"
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

	for _, c := range doc.Computeds {
		if c.Name == word {
			return fmt.Sprintf("```sngl\ncomputed %s\n```", c.Name)
		}
	}

	for _, c := range doc.Consts {
		if c.Name == word {
			return fmt.Sprintf("```sngl\nconst %s\n```", c.Name)
		}
	}

	for _, c := range doc.Components {
		if c.Name == word {
			return formatComponentHover(c)
		}
	}

	registry, styleProps, _, err := checker.LoadStdlib()
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s\n```\n", c.Name)
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

func formatSchemaHover(name string, schema *checker.ComponentSchema) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s  // stdlib\n```\n", name)
	if len(schema.Props) > 0 {
		sb.WriteString("\n**Props:**\n")
		for pname, ps := range schema.Props {
			fmt.Fprintf(&sb, "- `%s` %s\n", pname, ps.Type)
			if len(ps.Enum) > 0 {
				fmt.Fprintf(&sb, "  Values: %s\n", strings.Join(ps.Enum, ", "))
			}
		}
	}
	if len(schema.Events) > 0 {
		sb.WriteString("\n**Events:**\n")
		for ename, etype := range schema.Events {
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
