package markdown

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	snglast "git.duckfam.us/jonathan/sngl/ast"
)

// DocumentComponent is the name the generated component takes. One name
// whatever the file is called, so `import doc "md:./x.md"` is always
// `doc.document()`: deriving it from the filename would make the call site
// depend on a path the alias already stands for.
const DocumentComponent = "document"

// Convert turns markdown into the source of a SNGL package holding one
// component. name is the markdown file's own name and appears in the header.
func Convert(src []byte, name string) (string, error) {
	front, body, err := splitFrontmatter(src)
	if err != nil {
		return "", fmt.Errorf("md: %s: %w", name, err)
	}
	// The same goldmark the doc site parses with, GFM and all: two readings of
	// one document is a difference nobody would look for.
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(body))

	e := &emitter{src: body}
	e.linef("// Generated from %s by the md: import scheme; DO NOT EDIT.", name)
	e.line("")
	e.line(`import ui "sngl:ui"`)
	e.line(`import markup "sngl:ui/markup"`)
	if len(front) > 0 {
		e.line("")
		for _, c := range front {
			e.linef("const %s = %s", c.name, c.value)
		}
	}
	e.line("")
	e.wrap("component "+DocumentComponent+" ui.node", "", func(e *emitter) {
		e.wrap("ui.vbox", "style={gap=12}", func(e *emitter) {
			e.blocks(doc, false)
		})
	})
	if e.err != nil && *e.err != nil {
		return "", fmt.Errorf("md: %s: %w", name, *e.err)
	}
	return e.b.String(), nil
}

type emitter struct {
	b     strings.Builder
	src   []byte
	depth int
	err   *error // shared with every nested emitter, so the first failure wins
}

func (e *emitter) line(s string) {
	if s != "" {
		e.b.WriteString(strings.Repeat("    ", e.depth))
		e.b.WriteString(s)
	}
	e.b.WriteByte('\n')
}

func (e *emitter) linef(format string, args ...any) { e.line(fmt.Sprintf(format, args...)) }

func (e *emitter) errp() *error {
	if e.err == nil {
		var err error
		e.err = &err
	}
	return e.err
}

func (e *emitter) fail(format string, args ...any) {
	if p := e.errp(); *p == nil {
		*p = fmt.Errorf(format, args...)
	}
}

// wrap emits `head(args) { ... }`, or `head(args)` alone when body writes
// nothing -- a component with no children is written without a block, which
// is how `sngl fmt` writes one.
func (e *emitter) wrap(head, args string, body func(*emitter)) {
	if args != "" {
		head += "(" + args + ")"
	}
	inner := &emitter{src: e.src, depth: e.depth + 1, err: e.errp()}
	body(inner)
	if inner.b.Len() == 0 {
		e.line(head)
		return
	}
	e.line(head + " {")
	e.b.WriteString(inner.b.String())
	e.line("}")
}

// str spells s as a SNGL string literal.
func str(s string) string {
	return `"` + snglast.EscapeString(s, snglast.StyleDouble) + `"`
}

// --- Blocks ---

// blocks emits every block child of n. quoted says the blocks are inside a
// blockquote, where a paragraph is a `markup.quote` instead: the family has no
// block that *contains* prose, so the quotation is carried by each paragraph
// of the quote rather than by a box around them.
func (e *emitter) blocks(n gast.Node, quoted bool) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		e.block(c, quoted)
	}
}

func (e *emitter) block(n gast.Node, quoted bool) {
	switch n := n.(type) {
	case *gast.Heading:
		e.wrap(fmt.Sprintf("markup.heading%d", n.Level), "", func(e *emitter) { e.inlines(n) })
	case *gast.Paragraph:
		e.paragraph(n, quoted)
	case *gast.TextBlock:
		// A tight list item holds its prose in a TextBlock rather than a
		// Paragraph. It is still a paragraph of the document.
		e.paragraph(n, quoted)
	case *gast.Blockquote:
		e.blocks(n, true)
	case *gast.FencedCodeBlock:
		e.codeBlock(n, languageOf(n, e.src))
	case *gast.CodeBlock:
		e.codeBlock(n, "")
	case *gast.ThematicBreak:
		e.line("ui.divider")
	case *gast.List:
		e.list(n)
	case *gast.ListItem:
		e.listItem(n, "")
	case *east.Table:
		e.table(n)
	case *gast.HTMLBlock:
		// Raw html is one target's vocabulary and this package renders on six.
		// Dropped rather than shown as its source, which is a reading the
		// document that wrote it did not ask for either.
	default:
		e.fail("unsupported markdown block %T", n)
	}
}

func (e *emitter) paragraph(n gast.Node, quoted bool) {
	comp := "markup.paragraph"
	if quoted {
		comp = "markup.quote"
	}
	e.wrap(comp, "", func(e *emitter) { e.inlines(n) })
}

func (e *emitter) list(n *gast.List) {
	args := ""
	if n.IsOrdered() {
		args = fmt.Sprintf("ordered=true, start=%d", n.Start)
	}
	ordinal := n.Start
	e.wrap("markup.list", args, func(e *emitter) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			item, ok := c.(*gast.ListItem)
			if !ok {
				e.fail("unsupported markdown list child %T", c)
				continue
			}
			marker := ""
			if n.IsOrdered() {
				// The ordinal is the one thing only the importer knows: the
				// default body counts nothing, and a host that numbers a real
				// list ignores what is written here.
				marker = strconv.Itoa(ordinal) + "."
				ordinal++
			}
			e.listItem(item, marker)
		}
	})
}

func (e *emitter) listItem(n *gast.ListItem, marker string) {
	var args []string
	if marker != "" {
		args = append(args, "marker="+str(marker))
	}
	if task := takeTask(n); task != "" {
		args = append(args, "task=markup.Task."+task)
	}
	e.wrap("markup.listItem", strings.Join(args, ", "), func(e *emitter) {
		e.blocks(n, false)
	})
}

// takeTask reports a GFM task item's state and removes the checkbox from the
// tree, the `task` prop having taken it over. "" for an ordinary item.
func takeTask(n *gast.ListItem) string {
	first := n.FirstChild()
	if first == nil {
		return ""
	}
	box, ok := first.FirstChild().(*east.TaskCheckBox)
	if !ok {
		return ""
	}
	first.RemoveChild(first, box)
	if box.IsChecked {
		return "done"
	}
	return "todo"
}

func (e *emitter) codeBlock(n gast.Node, language string) {
	// The last line's newline belongs to the closing fence rather than to the
	// sample: kept, every code block ends in an empty plain token and a blank
	// line the author did not write.
	source := strings.TrimSuffix(string(linesOf(n, e.src)), "\n")
	e.wrap("markup.codeBlock", "", func(e *emitter) {
		for _, t := range tokenize(source, language) {
			e.wrap("markup.token", "kind=markup.Token."+t.kind, func(e *emitter) {
				e.line("markup.text(value=" + str(t.text) + ")")
			})
		}
	})
}

func (e *emitter) table(n *east.Table) {
	var columns, rows []string
	for r := n.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, str(plainText(c, e.src)))
		}
		if _, ok := r.(*east.TableHeader); ok {
			columns = cells
			continue
		}
		rows = append(rows, "["+strings.Join(cells, ", ")+"]")
	}
	// `ui.table`'s cells are plain strings, so a table's inline formatting
	// flattens to its words. A table of prose wants a layout of `richText`
	// runs, which is a document writing rows rather than an importer.
	args := "columns=[" + strings.Join(columns, ", ") + "]"
	if len(rows) > 0 {
		args += ", rows=[" + strings.Join(rows, ", ") + "]"
	}
	e.line("ui.table(" + args + ")")
}

// --- Inlines ---

// inlines emits the span children of a block, coalescing adjacent literal
// text so a sentence is one `markup.text` rather than one per word.
func (e *emitter) inlines(n gast.Node) {
	var pending strings.Builder
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		e.line("markup.text(value=" + str(pending.String()) + ")")
		pending.Reset()
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *gast.Text:
			pending.Write(c.Segment.Value(e.src))
			switch {
			case c.HardLineBreak():
				pending.WriteByte('\n')
			case c.SoftLineBreak():
				// A source line break is a space in the rendered document.
				// The family's text is literal, so the wrapping the markdown
				// author's editor chose must not reach the reader as one.
				pending.WriteByte(' ')
			}
		case *gast.String:
			pending.Write(c.Value)
		case *gast.Emphasis:
			flush()
			comp := "markup.italic"
			if c.Level >= 2 {
				comp = "markup.bold"
			}
			e.wrap(comp, "", func(e *emitter) { e.inlines(c) })
		case *east.Strikethrough:
			flush()
			e.wrap("markup.strike", "", func(e *emitter) { e.inlines(c) })
		case *gast.CodeSpan:
			flush()
			e.wrap("markup.monospace", "", func(e *emitter) {
				e.line("markup.text(value=" + str(plainText(c, e.src)) + ")")
			})
		case *gast.Link:
			flush()
			e.wrap("markup.link", "href="+str(string(c.Destination)), func(e *emitter) { e.inlines(c) })
		case *gast.AutoLink:
			flush()
			e.wrap("markup.link", "href="+str(string(c.URL(e.src))), func(e *emitter) {
				e.line("markup.text(value=" + str(string(c.Label(e.src))) + ")")
			})
		case *gast.Image:
			flush()
			// The description flattens to its words: `alt` is a string on the
			// declaration because a description is read aloud, not looked at.
			e.linef("markup.image(src=%s, alt=%s)", str(string(c.Destination)), str(plainText(c, e.src)))
		case *gast.RawHTML:
			// Dropped, for the reason an html block is.
		case *east.TaskCheckBox:
			// takeTask lifted it onto the item before this block was walked;
			// one written anywhere else is not a task list and has no box.
		default:
			e.fail("unsupported markdown inline %T", c)
		}
	}
	flush()
}

// --- Source helpers ---

// plainText flattens a node's inline content to its words. Used where the
// vocabulary on the other side is a plain string: an image's description and
// a table cell.
func plainText(n gast.Node, src []byte) string {
	var b strings.Builder
	var walk func(gast.Node)
	walk = func(n gast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *gast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *gast.String:
				b.Write(c.Value)
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// linesOf returns the raw source a block node covers.
func linesOf(n gast.Node, src []byte) []byte {
	var b []byte
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b = append(b, seg.Value(src)...)
	}
	return b
}

// languageOf returns a fence's info word, which is what chroma is asked for.
func languageOf(n *gast.FencedCodeBlock, src []byte) string {
	lang := n.Language(src)
	if lang == nil {
		return ""
	}
	return string(util.UnescapePunctuations(lang))
}
