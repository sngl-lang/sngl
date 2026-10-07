package markdown

import (
	"regexp"
	"slices"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/docsite"
)

// An in-page link is written against the page the doc site rendered, so a
// heading's anchor has to be the id goldmark gave it there -- repeats
// numbered as goldmark numbers them, punctuation dropped as goldmark drops it.
func TestHeadingAnchorsMatchGoldmark(t *testing.T) {
	const doc = "# Hello World\n\ntext\n\n## Hello World\n\n## Café & `code`!\n\n### Hello World\n\n#### `x`\n\n## 1. Numbered\n"
	src, err := Convert([]byte(doc), "doc.md")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`anchor="([^"]*)"`).FindAllStringSubmatch(src, -1) {
		got = append(got, m[1])
	}
	html, err := docsite.RenderMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, m := range regexp.MustCompile(`<h[1-6] id="([^"]*)"`).FindAllStringSubmatch(string(html), -1) {
		want = append(want, m[1])
	}
	if len(want) != 6 {
		t.Fatalf("goldmark rendered %d heading ids, want 6: %s", len(want), html)
	}
	if !slices.Equal(got, want) {
		t.Errorf("anchors = %q, goldmark's ids = %q\n%s", got, want, src)
	}
}
