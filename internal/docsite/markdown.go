package docsite

import (
	"bytes"
	"html/template"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	"git.duckfam.us/jonathan/sngl/internal/highlight"
)

// newMarkdown creates a goldmark instance configured for the doc site.
func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
					chromahtml.ClassPrefix("hl-"),
				),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}

// RenderMarkdown converts markdown bytes to HTML.
func RenderMarkdown(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := newMarkdown().Convert(src, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// HighlightSNGL returns syntax-highlighted HTML for SNGL source code.
// The output uses CSS classes (hl- prefix) matching the Chroma CSS generated
// by the doc site. Returns the raw source wrapped in <pre> on error.
func HighlightSNGL(source string) string {
	iterator, err := highlight.SNGLLexer.Tokenise(nil, source)
	if err != nil {
		return "<pre><code>" + template.HTMLEscapeString(source) + "</code></pre>"
	}
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true), chromahtml.ClassPrefix("hl-"))
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "<pre><code>" + template.HTMLEscapeString(source) + "</code></pre>"
	}
	return buf.String()
}
