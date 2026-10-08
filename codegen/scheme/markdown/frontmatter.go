package markdown

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	snglast "duckfam.us/sngl/ast"
)

// constDecl is one frontmatter key as the const the generated package writes.
type constDecl struct {
	name  string
	value string // already spelled as SNGL source
	typ   string // the SNGL type the scalar spells
	raw   string // the scalar as YAML wrote it
}

// splitFrontmatter peels a leading `---` YAML block off src and returns its
// scalars as consts, in the order they were written, along with the number of
// lines it took -- a fence reports the line it was written on, and the body
// goldmark sees no longer holds them. goldmark does not parse frontmatter, so
// the block is removed before it is handed over -- left in, it reads as a
// thematic break followed by a heading.
func splitFrontmatter(src []byte) ([]constDecl, []byte, int, error) {
	rest, ok := bytes.CutPrefix(src, []byte("---\n"))
	if !ok {
		return nil, src, 0, nil
	}
	before, after, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return nil, src, 0, fmt.Errorf("frontmatter opened with --- and is never closed")
	}
	block := before
	body := after
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = nil
	}
	lines := bytes.Count(src[:len(src)-len(body)], []byte("\n"))

	// A yaml.Node rather than a map: the document's own key order is what the
	// consts are written in, and a map hands back Go's.
	var doc yaml.Node
	if err := yaml.Unmarshal(block, &doc); err != nil {
		return nil, nil, 0, fmt.Errorf("frontmatter: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, body, lines, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, 0, fmt.Errorf("frontmatter is not a mapping")
	}
	var out []constDecl
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		name := key.Value
		if !isIdent(name) {
			return nil, nil, 0, fmt.Errorf("frontmatter key %q is not a SNGL identifier", name)
		}
		lit, typ, ok := scalarLiteral(val)
		if !ok {
			// A list or a nested mapping has no const to be, and inventing a
			// type for one is the importer deciding what the document meant.
			return nil, nil, 0, fmt.Errorf("frontmatter key %q is not a scalar", name)
		}
		out = append(out, constDecl{name: name, value: lit, typ: typ, raw: val.Value})
	}
	return out, body, lines, nil
}

// scalarLiteral spells a YAML scalar as SNGL source. A bool and a number keep
// their type; everything else is the string it was written as.
func scalarLiteral(n *yaml.Node) (lit, typ string, ok bool) {
	if n.Kind != yaml.ScalarNode {
		return "", "", false
	}
	switch n.Tag {
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return "", "", false
		}
		if b {
			return "true", "bool", true
		}
		return "false", "bool", true
	case "!!int":
		return n.Value, "int", true
	case "!!float":
		return n.Value, "float", true
	}
	return `"` + snglast.EscapeString(n.Value, snglast.StyleDouble) + `"`, "string", true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return !strings.ContainsAny(s, " ")
}
