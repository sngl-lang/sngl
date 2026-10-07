package sngl_test

import (
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
)

// TestMarkdownDocumentMatchesScheme holds md.document to the tree the md:
// scheme writes. testdata/markdown_document_matches_scheme.txtar renders one
// document through each door, in a div of its own, and the html inside the
// two has to be the same: a golden shows both, and only a comparison says
// they agree.
func TestMarkdownDocumentMatchesScheme(t *testing.T) {
	ar, err := txtar.ParseFile("testdata/markdown_document_matches_scheme.txtar")
	if err != nil {
		t.Fatal(err)
	}
	var page string
	for _, f := range ar.Files {
		if f.Name == "out/none/html/index.html" {
			page = string(f.Data)
		}
	}
	if page == "" {
		t.Fatal("the archive holds no out/none/html/index.html; seed it with -update")
	}
	scheme := divBody(t, page, `class="via-scheme"`)
	document := divBody(t, page, `class="via-document"`)
	if scheme != document {
		t.Errorf("md.document and md: render one document differently\n--- md:\n%s\n--- md.document\n%s", scheme, document)
	}
}

// divBody is what the div carrying attr holds, with its own indentation taken
// off each line so that two divs at one depth compare by content alone.
func divBody(t *testing.T, page, attr string) string {
	t.Helper()
	at := strings.Index(page, attr)
	if at < 0 {
		t.Fatalf("no div with %s", attr)
	}
	open := at + strings.Index(page[at:], ">") + 1
	depth, i := 1, open
	for depth > 0 {
		next := strings.Index(page[i:], "<")
		if next < 0 {
			t.Fatalf("the div with %s does not close", attr)
		}
		i += next
		switch {
		case strings.HasPrefix(page[i:], "</div"):
			depth--
		case strings.HasPrefix(page[i:], "<div"):
			depth++
		}
		i++
	}
	var lines []string
	for l := range strings.SplitSeq(page[open:i-1], "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
