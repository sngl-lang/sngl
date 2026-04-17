package highlight

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func init() {
	lexers.Register(SNGLLexer)
	lexers.Register(SNGLGrammarLexer)
}

// SNGLLexer is a Chroma lexer for SNGL source files.
var SNGLLexer = chroma.MustNewLexer(&chroma.Config{
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
			{Pattern: `\b(import|output|struct|enum|unit|style|component|const|var|if|for|func)\b`, Type: chroma.Keyword, Mutator: nil},
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

// SNGLGrammarLexer is a Chroma lexer for the EBNF-like grammar notation
// used in the language specification.
var SNGLGrammarLexer = chroma.MustNewLexer(&chroma.Config{
	Name:    "SNGL Grammar",
	Aliases: []string{"sngl-grammar"},
}, func() chroma.Rules {
	return chroma.Rules{
		"root": {
			// Comments
			{Pattern: `//[^\n]*`, Type: chroma.CommentSingle, Mutator: nil},
			// Quoted string literals
			{Pattern: `"[^"]*"`, Type: chroma.LiteralString, Mutator: nil},
			// Helper functions
			{Pattern: `\b(field|commaSep|commaSep1|sepBy1|optCommaSep|prec)\b`, Type: chroma.NameBuiltin, Mutator: nil},
			// ALL_CAPS terminals
			{Pattern: `\b[A-Z][A-Z0-9_]{1,}\b`, Type: chroma.KeywordType, Mutator: nil},
			// CamelCase rule references
			{Pattern: `\b[A-Z][a-zA-Z0-9]+\b`, Type: chroma.NameClass, Mutator: nil},
			// Numbers
			{Pattern: `\b\d+\b`, Type: chroma.LiteralNumberInteger, Mutator: nil},
			// Rule names (lowercase with underscores at start of line)
			{Pattern: `^[a-z_]\w*`, Type: chroma.NameFunction, Mutator: nil},
			// Operators
			{Pattern: `=|\.\.\.`, Type: chroma.Operator, Mutator: nil},
			// Punctuation
			{Pattern: `[(){}\[\],]`, Type: chroma.Punctuation, Mutator: nil},
			// Whitespace
			{Pattern: `\s+`, Type: chroma.TextWhitespace, Mutator: nil},
		},
	}
})
