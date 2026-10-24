package lspcore

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// nodeKeywords open a block that is not a node's.
var nodeKeywords = map[string]bool{
	"func": true, "if": true, "else": true, "for": true, "component": true,
	"struct": true, "enum": true, "unit": true, "var": true, "const": true,
}

// enclosingNode is the node whose argument list or block the cursor is in: its
// name, and the 1-based line it is written on. It scans back from the cursor
// for the first `(` or `{` left open, and reads the name written before it --
// passing over a handler's block, a function's, an `if`'s, which are not
// nodes, to the node around them. Ok is false where none encloses the cursor.
//
// Text rather than the AST, because the cursor is usually somewhere the parse
// failed: an `@` with nothing after it yet.
func enclosingNode(content string, line, col int) (name string, nodeLine int, ok bool) {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return "", 0, false
	}
	// The scan walks back one byte at a time over code, comments dropped.
	cur := textPos{line - 1, min(col-1, len(lines[line-1]))}
	code := func(l int) string { return stripComment(lines[l]) }
	text := code(cur.l)
	if cur.c > len(text) {
		cur.c = len(text)
	}
	back := func(p textPos) (textPos, byte, bool) {
		for {
			if p.c > 0 {
				p.c--
				return p, code(p.l)[p.c], true
			}
			if p.l == 0 {
				return p, 0, false
			}
			p.l--
			p.c = len(code(p.l))
		}
	}
	depth := 0
	p := cur
	for {
		var ch byte
		var more bool
		p, ch, more = back(p)
		if !more {
			return "", 0, false
		}
		switch ch {
		case ')', '}':
			depth++
			continue
		case '(', '{':
			if depth > 0 {
				depth--
				continue
			}
		default:
			continue
		}
		// An opener left open: what is written before it?
		q := p
		if ch == '{' {
			q = skipSpaceBack(lines, q, code)
			if q.c > 0 && code(q.l)[q.c-1] == ')' {
				// `name(args) {`: back over the argument list.
				q, ok = matchParenBack(q, code)
				if !ok {
					return "", 0, false
				}
			}
		}
		n, nl := nameBefore(code, skipSpaceBack(lines, q, code))
		if n != "" && !nodeKeywords[n] && !strings.HasPrefix(n, "@") {
			return n, nl + 1, true
		}
		// Not a node: carry on outward from this opener.
	}
}

// stripComment drops a line comment, outside a string.
func stripComment(l string) string {
	inStr := byte(0)
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case inStr != 0:
			if c == '\\' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '`':
			inStr = c
		case c == '/' && i+1 < len(l) && l[i+1] == '/':
			return l[:i]
		}
	}
	return l
}

// textPos is a 0-based line and byte column.
type textPos struct{ l, c int }

func skipSpaceBack(lines []string, p textPos, code func(int) string) textPos {
	for {
		for p.c > 0 {
			ch := code(p.l)[p.c-1]
			if ch != ' ' && ch != '\t' {
				return p
			}
			p.c--
		}
		if p.l == 0 {
			return p
		}
		p.l--
		p.c = len(code(p.l))
	}
}

// matchParenBack moves from just after a `)` to its `(`.
func matchParenBack(p textPos, code func(int) string) (textPos, bool) {
	depth := 0
	for {
		for p.c > 0 {
			p.c--
			switch code(p.l)[p.c] {
			case ')':
				depth++
			case '(':
				depth--
				if depth == 0 {
					return p, true
				}
			}
		}
		if p.l == 0 {
			return p, false
		}
		p.l--
		p.c = len(code(p.l))
	}
}

// nameBefore reads the node name ending at p: a dotted identifier, past an
// `#id` written after it.
func nameBefore(code func(int) string, p textPos) (string, int) {
	l := code(p.l)[:p.c]
	word := func(s string) (string, string) {
		i := len(s)
		for i > 0 {
			ch := s[i-1]
			if ch == '_' || ch == '.' || ch == '#' || ch == '@' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
				i--
				continue
			}
			break
		}
		return s[i:], s[:i]
	}
	w, rest := word(l)
	if strings.HasPrefix(w, "#") {
		w, _ = word(strings.TrimRight(rest, " \t"))
	}
	return w, p.l
}

// nodeAt is the checked node the cursor is in, found by the name and line the
// text says it was written at in doc's file, or nil -- where no package was
// checked, or the node was written after it was.
func nodeAt(pkg *ir.Package, doc *ast.Document, content string, line, col int) *ir.NodeInst {
	if pkg == nil {
		return nil
	}
	name, nodeLine, ok := enclosingNode(content, line, col)
	if !ok {
		return nil
	}
	local := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		local = name[i+1:]
	}
	file := FileOf(doc)
	var found *ir.NodeInst
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, isNode := n.(*ir.NodeInst)
		if !isNode || found != nil || inst.AST == nil || inst.Site != nil {
			return nil
		}
		if pos := ir.StmtPos(inst); pos.Line == nodeLine && pos.File == file && inst.Name == local {
			found = inst
		}
		return nil
	})
	return found
}

// nodeEventItems is what n's call site may handle: each event its component
// declares, typed by what the call site bound, less those the checker would
// refuse on it.
func nodeEventItems(n *ir.NodeInst, used map[string]bool) []CompletionItem {
	var items []CompletionItem
	for _, e := range n.Events() {
		if used[e.Name] || n.EventRefusal(e.Name) != "" {
			continue
		}
		items = append(items, eventItem(e.Name, irEventSignature(e)))
	}
	return items
}

// irEventSignature spells an event's parameter list as it is declared.
func irEventSignature(e *ir.EventDecl) string {
	parts := make([]string, len(e.Params))
	for i, p := range e.Params {
		parts[i] = strings.TrimSpace(p.Name + " " + p.Type.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// FileOf is the file doc was parsed from, as its positions name it: a package
// checked with its siblings holds positions in every one of them.
func FileOf(doc *ast.Document) string {
	if doc != nil {
		for _, st := range doc.Stmts {
			if p := st.StmtPos(); p != nil && p.File != "" {
				return p.File
			}
		}
	}
	return ""
}
