package markdown

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing/fstest"

	snglast "git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// SiteComponent is what a directory import hands the program: one component
// whose slot is the layout, inserted once per page.
const SiteComponent = "site"

// generatedFile is the file the directory's own .sngl files sit beside.
const generatedFile = "generated.sngl"

// reserved are the names the generated package declares, which no page may
// take.
var reserved = []string{SiteComponent, "root", "Page", "Frontmatter"}

// page is one markdown file, placed in the site's tree.
type page struct {
	key      string // path without extension; "" is the root, guide/index.md is "guide"
	file     string // relative to the directory, for diagnostics
	name     string // the component holding its blocks
	front    []constDecl
	blocks   string
	children []*page
}

// href follows the file: guide/index.md is /guide/index.html and guide.md is
// /guide.html, though both are the page guide.
func (p *page) href() string {
	if base := path.Base(strings.TrimSuffix(p.file, path.Ext(p.file))); base == "index" {
		return "/" + path.Join(p.key, "index.html")
	}
	return "/" + p.key + ".html"
}

// convertDir turns a directory into one package: every markdown file under it
// a page, every .sngl file directly in it a file of the package. label is the
// directory as the import wrote it.
func convertDir(fsys fs.FS, label string) (fs.FS, error) {
	label = strings.TrimSuffix(path.Clean(label), "/")
	out := fstest.MapFS{}
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case isMarkdown(p):
			files = append(files, p)
		case path.Ext(p) == ".sngl" && path.Dir(p) == ".":
			if p == generatedFile {
				return fmt.Errorf("md: %s/%s: the name is the generated file's", label, p)
			}
			src, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			out[p] = &fstest.MapFile{Data: src}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	root, pages, err := pageTree(files, label)
	if err != nil {
		return nil, err
	}
	state := newDocState(label)
	for _, p := range pages {
		src, err := fs.ReadFile(fsys, p.file)
		if err != nil {
			return nil, fmt.Errorf("md: reading %s/%s: %w", label, p.file, err)
		}
		if p.front, p.blocks, err = state.page(src, label+"/"+p.file); err != nil {
			return nil, err
		}
	}

	fields, order, err := frontmatterFields(out, pages, label)
	if err != nil {
		return nil, err
	}
	if order != nil {
		if err := order.sortChildren(pages, label); err != nil {
			return nil, err
		}
		pages = root.walk()
	}
	out[generatedFile] = &fstest.MapFile{Data: []byte(site(state, label, root, pages, fields))}
	return out, nil
}

func isMarkdown(p string) bool {
	return strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".markdown")
}

// pageTree places each file in the tree and names its component. A page's
// children are the pages in the directory of its own name, so every directory
// holding a page has to be one: `guide.md` or `guide/index.md`, not both.
func pageTree(files []string, label string) (*page, []*page, error) {
	byKey := map[string]*page{}
	byName := map[string]*page{}
	var pages []*page
	for _, f := range files {
		key := strings.TrimSuffix(f, path.Ext(f))
		if key == "index" {
			key = ""
		} else if dir, base := path.Split(key); base == "index" {
			key = strings.TrimSuffix(dir, "/")
		}
		if other := byKey[key]; other != nil {
			return nil, nil, fmt.Errorf("md: %s/%s and %s/%s both hold the page %s", label, other.file, label, f, key)
		}
		p := &page{key: key, file: f, name: pageName(key)}
		if !isIdent(p.name) {
			return nil, nil, fmt.Errorf("md: %s/%s: %q is not a name a component can take", label, f, p.name)
		}
		if slices.Contains(reserved, p.name) {
			return nil, nil, fmt.Errorf("md: %s/%s: the name %q is reserved: the generated package declares it", label, f, p.name)
		}
		if other := byName[p.name]; other != nil {
			return nil, nil, fmt.Errorf("md: %s/%s and %s/%s are both the component %s", label, other.file, label, f, p.name)
		}
		byKey[key], byName[p.name] = p, p
		pages = append(pages, p)
	}
	root := byKey[""]
	if root == nil {
		return nil, nil, fmt.Errorf("md: %s has no index.md: the root page is its content", label)
	}
	slices.SortFunc(pages, func(a, b *page) int { return strings.Compare(a.key, b.key) })
	for _, p := range pages {
		if p == root {
			continue
		}
		parentKey := path.Dir(p.key)
		if parentKey == "." {
			parentKey = ""
		}
		parent := byKey[parentKey]
		if parent == nil {
			base := path.Base(parentKey)
			return nil, nil, fmt.Errorf("md: %s/%s has no page of its own to be the parent of %s: add %s.md or %s/index.md", label, parentKey, p.file, base, base)
		}
		parent.children = append(parent.children, p)
	}
	return root, root.walk(), nil
}

// walk is the tree in document order: a page, then each child's subtree.
func (p *page) walk() []*page {
	out := []*page{p}
	for _, c := range p.children {
		out = append(out, c.walk()...)
	}
	return out
}

// pageName is a page's path as an identifier: segments joined by `_`, and
// anything an identifier cannot hold spelled `_` too.
func pageName(key string) string {
	if key == "" {
		return "index"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, key)
}

// field is one field of a synthesized Frontmatter.
type field struct {
	name, typ, file string
}

// frontmatterFields is the struct the importer declares when the directory
// declares none: the union of the keys the pages wrote, in the order first
// seen, each typed by the first scalar written for it. A directory that does
// declare one gets nil, and the checker holds each literal to it; its
// #[md.order] field, if it marks one, is the key children are sorted by.
func frontmatterFields(pkg fstest.MapFS, pages []*page, label string) ([]field, *orderKey, error) {
	for name, f := range pkg {
		doc, err := parser.Parse(name, f.Data)
		if err != nil {
			continue // the checker reports it against the file
		}
		for _, s := range doc.Stmts {
			if sd, ok := s.(*snglast.StructDef); ok && sd.Name == "Frontmatter" {
				key, err := findOrderKey(doc, sd, name, label)
				return nil, key, err
			}
		}
	}
	fields := []field{}
	seen := map[string]field{}
	for _, p := range pages {
		for _, c := range p.front {
			if prev, ok := seen[c.name]; ok {
				if prev.typ != c.typ {
					return nil, nil, fmt.Errorf("md: %s/%s: frontmatter key %q is %s here and %s in %s; declare struct Frontmatter to say which", label, p.file, c.name, c.typ, prev.typ, prev.file)
				}
				continue
			}
			f := field{name: c.name, typ: c.typ, file: p.file}
			seen[c.name] = f
			fields = append(fields, f)
		}
	}
	return fields, nil, nil
}

var zeros = map[string]string{"string": `""`, "int": "0", "float": "0.0", "bool": "false"}

// site writes the generated file: the page vocabulary, the tree as data, a
// component per page, and the site that inserts the layout once per page.
func site(state *docState, label string, root *page, pages []*page, fields []field) string {
	e := &emitter{doc: state}
	e.linef("// Generated from %s by the md: import scheme; DO NOT EDIT.", label)
	e.line("")
	e.header()
	if fields != nil {
		e.line("")
		e.line("struct Frontmatter {")
		for _, f := range fields {
			e.linef("    %s %s = %s", f.name, f.typ, zeros[f.typ])
		}
		e.line("}")
	}
	e.line("")
	e.line("struct Page {")
	e.line("    href string")
	e.line("    frontmatter Frontmatter")
	e.line("    children list<Page>")
	e.line("}")
	e.line("")
	e.line("const root = " + pageLit(root))
	e.decls()
	for _, p := range pages {
		e.line("")
		e.component(p.name, p.blocks)
	}
	e.line("")
	e.line("component " + SiteComponent + "<T>(layout component(page Page, content component ui.node) T) T {")
	for _, p := range pages {
		e.line("    layout(" + pageRef(root, p) + ") {")
		e.line("        component content {")
		e.line("            " + p.name)
		e.line("        }")
		e.line("    }")
	}
	e.line("}")
	return e.b.String()
}

func pageLit(p *page) string {
	fm := make([]string, len(p.front))
	for i, c := range p.front {
		fm[i] = c.name + "=" + c.value
	}
	kids := make([]string, len(p.children))
	for i, c := range p.children {
		kids[i] = pageLit(c)
	}
	return fmt.Sprintf("Page{href=%s, frontmatter=Frontmatter{%s}, children=[%s]}", str(p.href()), strings.Join(fm, ", "), strings.Join(kids, ", "))
}

// pageRef reads p out of the root const, so the tree is written once.
func pageRef(root, p *page) string {
	ref := "root"
	var find func(at *page) bool
	find = func(at *page) bool {
		if at == p {
			return true
		}
		for i, c := range at.children {
			prev := ref
			ref += fmt.Sprintf(".children[%d]", i)
			if find(c) {
				return true
			}
			ref = prev
		}
		return false
	}
	find(root)
	return ref
}
