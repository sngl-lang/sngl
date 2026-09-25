package gtk4rt_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglcolor"
)

// The five characters are the ones g_markup_escape_text answers for: the three
// that open and close an element or an entity, and the two quotes, which
// matter because a run's words may end up inside an attribute.
//
// The emitter escapes a literal run at build time and calls this for every
// other, so the two have to agree character for character -- which is why
// `escapePango` in codegen/platform/gtk4/markup.go lists the same five and
// nothing else does.
func TestEscapeIsEveryCharacterMarkupReads(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain words", "plain words"},
		{"<em>not markup</em>", "&lt;em&gt;not markup&lt;/em&gt;"},
		{"a & b", "a &amp; b"},
		{`say "hi"`, "say &quot;hi&quot;"},
		{"it's", "it&apos;s"},
		{"&<>\"'", "&amp;&lt;&gt;&quot;&apos;"},
		// Two spaces and a newline are the family's literal-whitespace rule,
		// and none of them is markup: escaping must leave them alone.
		{"two  spaces\nand a newline", "two  spaces\nand a newline"},
	} {
		if got := gtk4rt.Escape(tc.in); got != tc.want {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A color the build can read is written by the emitter's hexColor, and one it
// cannot is written here, so the two spell it alike.
func TestForegroundMatchesTheBuildTimeSpelling(t *testing.T) {
	if got := gtk4rt.Foreground(snglcolor.Color{R: 0xCF, G: 0x22, B: 0x2E, A: 255}); got != ` foreground="#CF222E"` {
		t.Errorf("Foreground = %q", got)
	}
	if got := gtk4rt.Foreground(snglcolor.Color{}); got != "" {
		t.Errorf("Foreground of the unset color = %q, want nothing", got)
	}
}
