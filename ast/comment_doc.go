package ast

import "strings"

// CommentDoc is the documentation a parameter, prop, event or slot carries:
// the line comments written directly above it, or, with none, the comment
// after it on its line. Above wins because it is where a longer note goes and
// a declaration's own doc comment already lives; a trailing comment is the
// short form a one-line note takes in a column of props.
//
// A comment that opens with TODO is a note to the next author rather than to
// a reader, and documents nothing.
func CommentDoc(leading []*Comment, trailing *Comment) string {
	var lines []string
	for _, c := range leading {
		if c == nil || c.Block {
			continue
		}
		lines = append(lines, commentText(c))
	}
	doc := strings.TrimSpace(strings.Join(lines, " "))
	if doc == "" && trailing != nil && !trailing.Block {
		doc = commentText(trailing)
	}
	if strings.HasPrefix(doc, "TODO") {
		return ""
	}
	return doc
}

// Doc is the parameter's documentation (see CommentDoc).
func (p Param) Doc() string { return CommentDoc(p.Leading, p.Trailing) }

// Doc is the event's documentation (see CommentDoc).
func (e EventDecl) Doc() string { return CommentDoc(e.Leading, e.Trailing) }

func commentText(c *Comment) string {
	text := strings.TrimPrefix(c.Text, "//")
	return strings.TrimSpace(text)
}
