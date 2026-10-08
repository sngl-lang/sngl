package markdown

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/parser"
)

const sample = `---
title: Guide
count: 2
draft: false
---

# Guide

A paragraph with **bold**, *italic*, ~~struck~~ words, ` + "`code`" + ` and a
[link](https://example.com).

> Quoted.

- one
  - nested
- [x] done
- [ ] open

1. first
2. second

` + "```sngl" + `
const n = 1
` + "```" + `

| Name | Role |
| ---- | ---- |
| a    | b    |

---

![a duck](duck.png)
`

// The generated package is SNGL source, so the first thing it owes anyone is
// to parse. Every construct the importer emits is in the sample above, which
// is what makes this the cheap check that a new mapping did not write
// something no file can hold.
func TestConvertParses(t *testing.T) {
	out, err := Convert([]byte(sample), "guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse("document.sngl", []byte(out)); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, out)
	}
}

// One document is one package, whichever import read it: a map iterated in Go's
// order, or a counter kept across calls, would make two imports of one file two
// different packages -- and the second would differ from the golden nobody
// regenerated.
func TestConvertIsDeterministic(t *testing.T) {
	first, err := Convert([]byte(sample), "guide.md")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		again, err := Convert([]byte(sample), "guide.md")
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("conversion %d differs from the first", i+1)
		}
	}
}

// Frontmatter keys become consts in the order the document wrote them, and a
// scalar keeps its type: a count is a number a program can add to, not the
// digits it was spelled with.
func TestFrontmatterBecomesConsts(t *testing.T) {
	out, err := Convert([]byte(sample), "guide.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "const title = \"Guide\"\nconst count = 2\nconst draft = false\n"
	if !strings.Contains(out, want) {
		t.Fatalf("frontmatter consts missing or reordered:\n%s", out)
	}
}

func TestFrontmatterRefusals(t *testing.T) {
	for _, tt := range []struct {
		name, src, want string
	}{
		{"unclosed", "---\ntitle: x\n", "never closed"},
		{"not an identifier", "---\nmy title: x\n---\n", "not a SNGL identifier"},
		{"not a scalar", "---\ntags: [a, b]\n---\n", "not a scalar"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.src), "doc.md")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

// The document is a file of the project being built, so it is read through the
// filesystem that project is checked against -- which is the only path an
// in-memory package (a golden archive, the playground) has.
func TestResolveProjectFS(t *testing.T) {
	fsys := fstest.MapFS{"guide.md": &fstest.MapFile{Data: []byte("# Hi\n")}}
	out, err := (&Importer{}).ResolveProjectFS("./guide.md", fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(out, "document.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "markup.heading1") {
		t.Fatalf("heading not converted:\n%s", got)
	}
}

func TestResolveProjectFSRefusals(t *testing.T) {
	fsys := fstest.MapFS{"guide.md": &fstest.MapFile{Data: []byte("# Hi\n")}}
	for _, tt := range []struct{ uri, want string }{
		{"", "needs a path"},
		{"./guide.sngl", "not a markdown file"},
		{"../outside.md", "escapes project directory"},
		{"./missing.md", "reading"},
	} {
		if _, err := (&Importer{}).ResolveProjectFS(tt.uri, fsys, "."); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: got %v, want an error mentioning %q", tt.uri, err, tt.want)
		}
	}
}

// A name starting with `_` in a directory import is the directory's own
// material -- a template, a tutorial's source -- and no page, as Go skips one:
// neither a `_` file nor anything under a `_` directory is rendered.
func TestDirectorySkipsUnderscoreNames(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/index.md":           &fstest.MapFile{Data: []byte("# Home\n")},
		"docs/_tour.md":           &fstest.MapFile{Data: []byte("# Tour\n")},
		"docs/_templates/page.md": &fstest.MapFile{Data: []byte("# Template\n")},
		"docs/guide.md":           &fstest.MapFile{Data: []byte("# Guide\n")},
		"docs/guide/_draft.md":    &fstest.MapFile{Data: []byte("# Draft\n")},
	}
	out, err := (&Importer{}).ResolveProjectFS("./docs/", fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(out, "generated.sngl")
	if err != nil {
		t.Fatal(err)
	}
	src := string(got)
	for _, page := range []string{"component index ", "component guide "} {
		if !strings.Contains(src, page) {
			t.Errorf("no %q in the site:\n%s", page, src)
		}
	}
	for _, skipped := range []string{"Tour", "Template", "Draft", "_tour", "_draft"} {
		if strings.Contains(src, skipped) {
			t.Errorf("%q reached the site, which skips a `_` name:\n%s", skipped, src)
		}
	}
	if !strings.Contains(src, "nav.page(href=") {
		t.Errorf("site renders no nav.page:\n%s", src)
	}
}
