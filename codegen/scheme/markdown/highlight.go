package markdown

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	_ "duckfam.us/sngl/internal/highlight" // registers the SNGL lexer with chroma
)

// tok is one run of a code sample and the `markup.Token` member it renders as.
type tok struct {
	kind string
	text string
}

// tokenize splits a code sample into token runs. A language chroma does not
// know, and a fence that named none, comes back as one plain run: `plain` is
// written rather than left out, so a reader of the tree is never asked to tell
// an unhighlighted sample from a missing span.
func tokenize(source, language string) []tok {
	if source == "" {
		return nil
	}
	lexer := lexers.Get(language)
	if lexer == nil {
		return []tok{{kind: "plain", text: source}}
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return []tok{{kind: "plain", text: source}}
	}
	var out []tok
	for t := iterator(); t != chroma.EOF; t = iterator() {
		if t.Value == "" {
			continue
		}
		kind := foldToken(t.Type)
		// Coalesce merges by chroma type, and several of those fold to one
		// member here -- two adjacent runs of the same kind would otherwise
		// reach the tree as two spans saying the same thing.
		if n := len(out); n > 0 && out[n-1].kind == kind {
			out[n-1].text += t.Value
			continue
		}
		out = append(out, tok{kind: kind, text: t.Value})
	}
	return out
}

// foldToken maps a chroma token type onto the eleven `markup.Token` members.
// A fold rather than a translation: the family's vocabulary is deliberately
// what every syntax theme distinguishes and no more, so a finer classification
// loses nothing a reader can see.
func foldToken(t chroma.TokenType) string {
	switch t.Category() {
	case chroma.Comment:
		return "comment"
	case chroma.Operator:
		return "operator"
	case chroma.Punctuation:
		return "punctuation"
	case chroma.Keyword:
		switch t {
		case chroma.KeywordType:
			return "type"
		case chroma.KeywordConstant:
			return "constant"
		}
		return "keyword"
	case chroma.Literal:
		switch t.SubCategory() {
		case chroma.LiteralString:
			return "string"
		case chroma.LiteralNumber:
			return "number"
		}
		return "constant"
	case chroma.Name:
		switch t.SubCategory() {
		case chroma.NameFunction:
			return "function"
		case chroma.NameConstant:
			return "constant"
		case chroma.NameClass, chroma.NameNamespace, chroma.NameException:
			return "type"
		case chroma.NameVariable, chroma.NameAttribute, chroma.NameProperty, chroma.NameLabel, chroma.NameTag:
			return "variable"
		}
		return "variable"
	}
	// Whitespace, generic output, an error the lexer could not place.
	return "plain"
}
