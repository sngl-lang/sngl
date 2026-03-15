package docsite

import (
	"bytes"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
)

func init() {
	lexers.Register(snglLexer)
}

// snglLexer is a Chroma lexer for SNGL source files.
var snglLexer = chroma.MustNewLexer(&chroma.Config{
	Name:      "SNGL",
	Aliases:   []string{"sngl"},
	Filenames: []string{"*.sngl"},
	MimeTypes: []string{"text/x-sngl"},
}, func() chroma.Rules {
	return chroma.Rules{
		"root": {
			// Line comments
			{Pattern: `//[^\n]*`, Type: chroma.CommentSingle, Mutator: nil},
			// Block comments
			{Pattern: `/\*[\s\S]*?\*/`, Type: chroma.CommentMultiline, Mutator: nil},
			// Keywords
			{Pattern: `\b(import|output|struct|enum|unit|style|styles|component|param|prop|event|children|const|var|computed|if|for|in|func|extern|trigger)\b`, Type: chroma.Keyword, Mutator: nil},
			// Builtin constants
			{Pattern: `\b(true|false|null)\b`, Type: chroma.KeywordConstant, Mutator: nil},
			// Builtin types
			{Pattern: `\b(bool|int|float|string|dyn|color|list|map|date|time|duration|measurement|url|email|uuid)\b`, Type: chroma.KeywordType, Mutator: nil},
			// Builtin components
			{Pattern: `\b(vbox|hbox|stack|text|button|input|image|scroll|spacer|checkbox)\b`, Type: chroma.NameTag, Mutator: nil},
			// Color literals
			{Pattern: `#[0-9a-fA-F]{3,8}\b`, Type: chroma.LiteralStringOther, Mutator: nil},
			// Unit literals (number + suffix)
			{Pattern: `\b\d+(\.\d+)?(px|em|rem|vw|vh|pct|ms|s|m|h)\b`, Type: chroma.LiteralNumber, Mutator: nil},
			// Float literals
			{Pattern: `\b\d+\.\d+\b`, Type: chroma.LiteralNumberFloat, Mutator: nil},
			// Integer literals
			{Pattern: `\b\d+\b`, Type: chroma.LiteralNumberInteger, Mutator: nil},
			// Strings
			{Pattern: `"`, Type: chroma.LiteralString, Mutator: chroma.Push("string")},
			// Event handler @name
			{Pattern: `@\w+`, Type: chroma.NameDecorator, Mutator: nil},
			// Operators
			{Pattern: `[!=<>]=|&&|\|\||!!|\->|[+\-*/%=<>!?:|]`, Type: chroma.Operator, Mutator: nil},
			// Punctuation
			{Pattern: `[(){}\[\],;.]`, Type: chroma.Punctuation, Mutator: nil},
			// Identifiers (capitalized = type)
			{Pattern: `\b[A-Z]\w*\b`, Type: chroma.NameClass, Mutator: nil},
			{Pattern: `\b[a-z_]\w*\b`, Type: chroma.Name, Mutator: nil},
			// Whitespace
			{Pattern: `\s+`, Type: chroma.TextWhitespace, Mutator: nil},
		},
		"string": {
			{Pattern: `\{`, Type: chroma.LiteralStringInterpol, Mutator: chroma.Push("interp")},
			{Pattern: `\\[\\"{nrt]`, Type: chroma.LiteralStringEscape, Mutator: nil},
			{Pattern: `"`, Type: chroma.LiteralString, Mutator: chroma.Pop(1)},
			{Pattern: `[^"\\{]+`, Type: chroma.LiteralString, Mutator: nil},
		},
		"interp": {
			{Pattern: `\}`, Type: chroma.LiteralStringInterpol, Mutator: chroma.Pop(1)},
			chroma.Include("root"),
		},
	}
})

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
