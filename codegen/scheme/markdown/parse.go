package markdown

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// sngl:ui/markup/md's `document` parses its source at build time through
// this: the same goldmark and the same mapping the scheme's Convert writes
// source with, handed back as the data the declaration's components render.
func init() {
	buildhost.RegisterIntrinsic("md.parse", parseIntrinsic)
}

// maxListDepth is how deep a list nests in what Parse hands back: the
// components rendering it cannot call themselves, so there is one per level.
// A list below the last is laid out in its item's place.
const maxListDepth = 3

// Block is one block of a parsed document, the Go side of `md._Block`.
type Block struct {
	Kind    string // a member of md._Kind
	Runs    []Run
	Ordered bool
	Start   int
	Items   []Item
	Columns []string
	Rows    [][]string
}

// Item is one item of a list, the Go side of `md._Item`.
type Item struct {
	Marker string
	Task   string // a member of markup.Task
	Blocks []Block
}

// Run is a run of words and what is true of them, the Go side of `md._Run`.
type Run struct {
	Text                       string
	Bold, Italic, Strike, Mono bool
	Href, Image                string
	Token                      string // a member of markup.Token
	Parts                      []Run
}

// Parse reads markdown into blocks. A fence's `mode=` is not read: every
// fence is a sample here, since a live example has no program to join.
func Parse(src []byte) ([]Block, error) {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(src))
	p := &mdParser{src: src}
	blocks := p.blocks(doc, false, 0)
	return blocks, p.err
}

type mdParser struct {
	src []byte
	err error
}

func (p *mdParser) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
}

// blocks reads every block child of n. depth is how many lists enclose them.
func (p *mdParser) blocks(n gast.Node, quoted bool, depth int) []Block {
	var out []Block
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, p.block(c, quoted, depth)...)
	}
	return out
}

func (p *mdParser) block(n gast.Node, quoted bool, depth int) []Block {
	switch n := n.(type) {
	case *gast.Heading:
		return []Block{{Kind: fmt.Sprintf("heading%d", n.Level), Runs: p.inlines(n)}}
	case *gast.Paragraph, *gast.TextBlock:
		kind := "paragraph"
		if quoted {
			kind = "quote"
		}
		return []Block{{Kind: kind, Runs: p.inlines(n)}}
	case *gast.Blockquote:
		return p.blocks(n, true, depth)
	case *gast.FencedCodeBlock:
		return []Block{codeBlock(trimNewline(linesOf(n, p.src)), languageOf(n, p.src))}
	case *gast.CodeBlock:
		return []Block{codeBlock(trimNewline(linesOf(n, p.src)), "")}
	case *gast.ThematicBreak:
		return []Block{{Kind: "rule"}}
	case *gast.List:
		return p.list(n, depth)
	case *gast.ListItem:
		return p.item(n, "", depth).Blocks
	case *east.Table:
		return []Block{p.table(n)}
	case *gast.HTMLBlock:
		return nil
	}
	p.fail("unsupported markdown block %T", n)
	return nil
}

// list reads a list one level below depth, or splices its items' blocks into
// the item holding it when that level is past the last.
func (p *mdParser) list(n *gast.List, depth int) []Block {
	b := Block{Kind: "list", Ordered: n.IsOrdered(), Start: 1}
	if n.IsOrdered() {
		b.Start = n.Start
	}
	ordinal := b.Start
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		item, ok := c.(*gast.ListItem)
		if !ok {
			p.fail("unsupported markdown list child %T", c)
			continue
		}
		marker := ""
		if n.IsOrdered() {
			marker = strconv.Itoa(ordinal) + "."
			ordinal++
		}
		b.Items = append(b.Items, p.item(item, marker, depth+1))
	}
	if depth >= maxListDepth {
		var flat []Block
		for _, it := range b.Items {
			flat = append(flat, it.Blocks...)
		}
		return flat
	}
	return []Block{b}
}

func (p *mdParser) item(n *gast.ListItem, marker string, depth int) Item {
	task := takeTask(n)
	if task == "" {
		task = "none"
	}
	return Item{Marker: marker, Task: task, Blocks: p.blocks(n, false, depth)}
}

func codeBlock(source, language string) Block {
	b := Block{Kind: "code"}
	for _, t := range tokenize(source, language) {
		b.Runs = append(b.Runs, Run{Text: t.text, Token: t.kind})
	}
	return b
}

func (p *mdParser) table(n *east.Table) Block {
	b := Block{Kind: "table"}
	for r := n.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, plainText(c, p.src))
		}
		if _, ok := r.(*east.TableHeader); ok {
			b.Columns = cells
			continue
		}
		b.Rows = append(b.Rows, cells)
	}
	return b
}

// style is what the spans around a run said about it.
type style struct{ bold, italic, strike bool }

// inlines reads the spans of a block as a flat list of runs, each carrying
// the style of every span around it -- which is what the span family's
// cascade makes of the nesting the scheme writes out. Adjacent words in one
// style are one run.
func (p *mdParser) inlines(n gast.Node) []Run {
	var out []Run
	p.spans(n, style{}, &out)
	return out
}

func (p *mdParser) spans(n gast.Node, st style, out *[]Run) {
	add := func(r Run) {
		r.Bold, r.Italic, r.Strike = r.Bold || st.bold, r.Italic || st.italic, r.Strike || st.strike
		if r.Token == "" {
			r.Token = "plain"
		}
		if k := len(*out) - 1; k >= 0 && r.Href == "" && r.Image == "" && sameStyle((*out)[k], r) {
			(*out)[k].Text += r.Text
			return
		}
		*out = append(*out, r)
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *gast.Text:
			s := string(c.Segment.Value(p.src))
			switch {
			case c.HardLineBreak():
				s += "\n"
			case c.SoftLineBreak():
				s += " "
			}
			add(Run{Text: s})
		case *gast.String:
			add(Run{Text: string(c.Value)})
		case *gast.Emphasis:
			inner := st
			if c.Level >= 2 {
				inner.bold = true
			} else {
				inner.italic = true
			}
			p.spans(c, inner, out)
		case *east.Strikethrough:
			inner := st
			inner.strike = true
			p.spans(c, inner, out)
		case *gast.CodeSpan:
			add(Run{Text: plainText(c, p.src), Mono: true})
		case *gast.Link:
			var parts []Run
			p.spans(c, st, &parts)
			*out = append(*out, Run{Href: string(c.Destination), Token: "plain", Parts: parts})
		case *gast.AutoLink:
			*out = append(*out, Run{Href: string(c.URL(p.src)), Token: "plain", Parts: []Run{{Text: string(c.Label(p.src)), Token: "plain"}}})
		case *gast.Image:
			*out = append(*out, Run{Text: plainText(c, p.src), Image: string(c.Destination), Token: "plain"})
		case *gast.RawHTML, *east.TaskCheckBox:
		default:
			p.fail("unsupported markdown inline %T", c)
		}
	}
}

func sameStyle(a, b Run) bool {
	return a.Href == "" && a.Image == "" && a.Bold == b.Bold && a.Italic == b.Italic &&
		a.Strike == b.Strike && a.Mono == b.Mono && a.Token == b.Token
}

func trimNewline(b []byte) string {
	s := string(b)
	if n := len(s); n > 0 && s[n-1] == '\n' {
		return s[:n-1]
	}
	return s
}

// parseIntrinsic answers md.parse: the source parsed and built into values of
// the declaration's return type. It reads nothing but its argument, so it
// records nothing.
func parseIntrinsic(_ *buildhost.Recorder, call *ir.Call, args []any) (any, error) {
	src, _ := args[0].(string)
	if call == nil || call.Func == nil || call.Func.Return == nil || len(call.Func.Return.Elems) != 1 {
		return nil, errors.New("md.parse: no declared return type")
	}
	blocks, err := Parse([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("md.parse: %w", err)
	}
	tb := newTypes(call.Func.Return.Elems[0])
	if tb == nil {
		return nil, errors.New("md.parse: the declaration's types are not md._Block, md._Item and md._Run")
	}
	return tb.blocks(blocks), nil
}

// types are the three declarations a parse is built into, read off the
// intrinsic's return type rather than looked up by name.
type types struct {
	block, item, run *ir.Type
}

func newTypes(block *ir.Type) *types {
	items := fieldType(block, "items")
	runs := fieldType(block, "runs")
	if items == nil || runs == nil || len(items.Elems) != 1 || len(runs.Elems) != 1 {
		return nil
	}
	return &types{block: block, item: items.Elems[0], run: runs.Elems[0]}
}

func fieldType(t *ir.Type, name string) *ir.Type {
	def, _ := t.Decl.(*ir.StructDef)
	if def == nil {
		return nil
	}
	for _, f := range def.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

func newStruct(t *ir.Type) *interp.Struct {
	def, _ := t.Decl.(*ir.StructDef)
	return interp.NewStruct(def, t)
}

func (tb *types) blocks(bs []Block) []any {
	out := make([]any, len(bs))
	for i, b := range bs {
		s := newStruct(tb.block)
		s.Set("kind", b.Kind)
		s.Set("runs", tb.runs(b.Runs))
		s.Set("ordered", b.Ordered)
		s.Set("start", b.Start)
		items := make([]any, len(b.Items))
		for j, it := range b.Items {
			is := newStruct(tb.item)
			is.Set("marker", it.Marker)
			is.Set("task", it.Task)
			is.Set("blocks", tb.blocks(it.Blocks))
			items[j] = is
		}
		s.Set("items", items)
		s.Set("columns", stringList(b.Columns))
		rows := make([]any, len(b.Rows))
		for j, r := range b.Rows {
			rows[j] = stringList(r)
		}
		s.Set("rows", rows)
		out[i] = s
	}
	return out
}

func (tb *types) runs(rs []Run) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		s := newStruct(tb.run)
		s.Set("text", r.Text)
		s.Set("bold", r.Bold)
		s.Set("italic", r.Italic)
		s.Set("strike", r.Strike)
		s.Set("mono", r.Mono)
		s.Set("href", r.Href)
		s.Set("image", r.Image)
		s.Set("token", r.Token)
		s.Set("parts", tb.runs(r.Parts))
		out[i] = s
	}
	return out
}

func stringList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
