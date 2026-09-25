// Package docs embeds the SNGL documentation markdown files and provides
// pure functions for compile-time page generation.
package docs

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/docs/lookup"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/lib"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

//go:embed *.md learn reference
var content embed.FS

type Page struct {
	Title       string
	Slug        string // URL slug, e.g. "learn/installation"
	Href        string // URL path, e.g. "/learn/installation.html"
	Description string
	Order       int
	Section     string // parent directory, e.g. "learn", "reference"; "" for root-level files
	IsIndex     bool   // true if this page is the section's index.md (or root index.md)
}

//sngl:pure
func Pages() []Page {
	var pages []Page
	fs.WalkDir(content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		if path == "learn/tour.md" {
			return nil // rendered separately as the interactive tutorial
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, _, err := docsite.ParseFrontMatter(data)
		if err != nil {
			return nil
		}

		slug := strings.TrimSuffix(path, ".md")
		isIndex := false
		if before, ok := strings.CutSuffix(slug, "/index"); ok {
			slug = before
			isIndex = true
		}
		if slug == "index" {
			slug = ""
			isIndex = true
		}

		section := ""
		if dir := filepath.Dir(path); dir != "." {
			section = dir
		}

		title := fm.Title
		if title == "" {
			title = autoTitle(filepath.Base(slug))
		}

		href := "/" + slug + ".html"
		if isIndex {
			href = "/" + strings.TrimPrefix(slug+"/index.html", "/")
		}

		pages = append(pages, Page{
			Title:       title,
			Slug:        slug,
			Href:        href,
			Description: fm.Description,
			Order:       fm.Order,
			Section:     section,
			IsIndex:     isIndex,
		})
		return nil
	})

	sort.Slice(pages, func(i, j int) bool {
		if pages[i].Section != pages[j].Section {
			return pages[i].Section < pages[j].Section
		}
		if pages[i].Order != pages[j].Order {
			return pages[i].Order < pages[j].Order
		}
		return pages[i].Title < pages[j].Title
	})

	return pages
}

type NavLink struct {
	Title string
	Href  string
	Order int
}

// NavGroup is a section in the sidebar. Title "" renders flat (one item, no
// heading) — used for top-level entries like Home and Playground. A non-empty
// Title renders as a section heading; Href links to the section's landing page.
type NavGroup struct {
	Title string
	Href  string
	Items []NavLink
}

// navExtras lists sidebar entries that have no markdown source — virtual
// generated pages (Components, Standard Library, etc.) and top-level entries
// like Home / Playground. Section "" places the entry at the sidebar root.
var navExtras = []struct {
	Section string
	Link    NavLink
}{
	{"", NavLink{Title: "Home", Href: "/index.html", Order: 0}},
	{"learn", NavLink{Title: "Tour", Href: "/tutorial.html", Order: 30}},
	{"reference", NavLink{Title: "Components", Href: "/components/index.html", Order: 1}},
	{"reference", NavLink{Title: "Standard Library", Href: "/docs/sngl/index.html", Order: 2}},
	{"reference", NavLink{Title: "Platforms", Href: "/platforms/index.html", Order: 3}},
	{"reference", NavLink{Title: "Languages", Href: "/languages/index.html", Order: 4}},
	{"", NavLink{Title: "Playground", Href: "/playground.html", Order: 1000}},
}

// The sidebar mirrors the docs directory: a subdirectory with an index.md is a
// section heading, its other markdown files the items. A page with no parent
// index.md (e.g. docs/index.md) is still emitted but stays out of the sidebar,
// so link to it explicitly from another page.
//
//sngl:pure
func NavTree() []NavGroup {
	type sec struct {
		title string
		href  string
		order int
		items []NavLink
		seen  bool
	}
	sections := map[string]*sec{}
	getSec := func(name string) *sec {
		s, ok := sections[name]
		if !ok {
			s = &sec{}
			sections[name] = s
		}
		return s
	}

	for _, p := range Pages() {
		if p.Section == "" {
			continue // root-level pages don't appear in the sidebar
		}
		s := getSec(p.Section)
		if p.IsIndex {
			s.title = p.Title
			s.href = p.Href
			s.order = p.Order
			s.seen = true
			continue
		}
		s.items = append(s.items, NavLink{
			Title: p.Title,
			Href:  p.Href,
			Order: p.Order,
		})
	}

	type topEntry struct {
		isSection bool
		order     int
		group     NavGroup
	}
	var top []topEntry
	for _, e := range navExtras {
		if e.Section == "" {
			top = append(top, topEntry{
				order: e.Link.Order,
				group: NavGroup{Items: []NavLink{e.Link}},
			})
			continue
		}
		s := getSec(e.Section)
		s.items = append(s.items, e.Link)
	}

	for name, s := range sections {
		if !s.seen {
			delete(sections, name)
		}
	}

	for _, s := range sections {
		sort.Slice(s.items, func(i, j int) bool {
			if s.items[i].Order != s.items[j].Order {
				return s.items[i].Order < s.items[j].Order
			}
			return s.items[i].Title < s.items[j].Title
		})
		top = append(top, topEntry{
			isSection: true,
			order:     s.order,
			group:     NavGroup{Title: s.title, Href: s.href, Items: s.items},
		})
	}

	sort.SliceStable(top, func(i, j int) bool {
		if top[i].order != top[j].order {
			return top[i].order < top[j].order
		}
		// Stable tiebreak: sections by title, links by their single item title.
		ti, tj := top[i].group.Title, top[j].group.Title
		if ti == "" && len(top[i].group.Items) > 0 {
			ti = top[i].group.Items[0].Title
		}
		if tj == "" && len(top[j].group.Items) > 0 {
			tj = top[j].group.Items[0].Title
		}
		return ti < tj
	})

	groups := make([]NavGroup, len(top))
	for i, e := range top {
		groups[i] = e.group
	}
	return groups
}

type Component struct {
	Name string
	// Pkg is the import path a program writes to reach it, and Href the page
	// its documentation is generated at.
	Pkg      string
	Href     string
	Doc      string // full doc string
	Blurb    string // first sentence of Doc, for index/card listings
	Children string // "none", "exactly one", "at most one", "many"
	Props    []ComponentProp
	// Slots are in declaration order, not sorted: the rest slot conventionally
	// ends the list and a reader looking for where bare children go reads down.
	Slots           []ComponentSlot
	Events          []ComponentEvent
	Examples        []string // raw .sngl source for each example
	HighlightedCode string   // syntax-highlighted HTML of the first example
	PreviewHTML     string   // compiled HTML for interactive iframe preview
}

type ComponentProp struct {
	Name string
	Type string
	Doc  string
}

// ComponentSlot is one slot of a component. Type is spelled as the declaration
// wrote it, so a rest slot's `...` and a scoped slot's parameter names -- which
// by-name matching makes contract -- are both visible.
type ComponentSlot struct {
	Name string
	Type string
	Rest bool
}

type ComponentEvent struct {
	Name        string
	PayloadType string
}

// LibraryComponents is every exported component of every public library
// package, each carrying the package it belongs to. One package at a time,
// because a component is a declaration in a package and two packages may
// declare one name.
//
//sngl:pure
func LibraryComponents() []Component {
	var comps []Component
	for _, uri := range lib.PublicPackages() {
		comps = append(comps, packageComponents(uri)...)
	}
	sort.Slice(comps, func(i, j int) bool {
		if comps[i].Pkg != comps[j].Pkg {
			return comps[i].Pkg < comps[j].Pkg
		}
		return comps[i].Name < comps[j].Name
	})
	return comps
}

// CuratedComponents is the starter set the /components page leads with, in the
// order docsite.Starter lists them rather than alphabetically: it is a reading
// order, not an index.
//
//sngl:pure
func CuratedComponents() []Component {
	byName := map[string]Component{}
	for _, c := range packageComponents(docsite.StarterPkg) {
		byName[c.Name] = c
	}
	out := make([]Component, 0, len(docsite.Starter))
	for _, name := range docsite.Starter {
		if c, ok := byName[name]; ok {
			out = append(out, c)
		}
	}
	return out
}

// CuratedRest is how many components of the starter package the curated list
// leaves out, for the page's link on to the full listing.
//
//sngl:pure
func CuratedRest() int {
	rest := len(packageComponents(docsite.StarterPkg)) - len(CuratedComponents())
	if rest < 0 {
		return 0
	}
	return rest
}

// CuratedPkg is the import path the curated list is drawn from.
//
//sngl:pure
func CuratedPkg() string { return "sngl:" + docsite.StarterPkg }

// CuratedPkgHref is the page listing everything CuratedComponents leaves out.
//
//sngl:pure
func CuratedPkgHref() string { return lookup.PackageHref(CuratedPkg()) }

// CuratedRestLabel is the link on to the full listing, formatted here because
// SNGL would have to convert the count to a string at the call site.
//
//sngl:pure
func CuratedRestLabel() string {
	rest := CuratedRest()
	if rest == 0 {
		return "Browse " + CuratedPkg()
	}
	return fmt.Sprintf("Browse the other %d components in %s", rest, CuratedPkg())
}

// packageComponents takes the library package's URI ("ui"), which is what the
// checker keys a schema by; the import path is that under the scheme.
func packageComponents(uri string) []Component {
	path := "sngl:" + uri
	registry := checker.PackageSchema(uri)
	examples := checker.PackageExamples(uri)

	var comps []Component
	for name, schema := range registry {
		c := Component{
			Name:     name,
			Pkg:      path,
			Href:     lookup.DeclHref(path, "components", name),
			Doc:      schema.Doc,
			Blurb:    firstSentence(schema.Doc),
			Children: docsite.ChildPolicyString(schema.Children),
		}
		for pname, ps := range schema.Props {
			c.Props = append(c.Props, ComponentProp{
				Name: pname,
				Type: ps.Type.String(),
				Doc:  ps.Doc,
			})
		}
		sort.Slice(c.Props, func(i, j int) bool { return c.Props[i].Name < c.Props[j].Name })
		for _, sl := range schema.Slots {
			c.Slots = append(c.Slots, ComponentSlot{
				Name: sl.Name,
				Type: parser.FormatType(sl.Type),
				Rest: sl.Rest,
			})
		}
		for ename, payload := range schema.Events {
			c.Events = append(c.Events, ComponentEvent{
				Name:        ename,
				PayloadType: payload,
			})
		}
		sort.Slice(c.Events, func(i, j int) bool { return c.Events[i].Name < c.Events[j].Name })

		if srcs, ok := examples[name]; ok {
			c.Examples = srcs
			if len(srcs) > 0 {
				c.HighlightedCode = docsite.HighlightSNGL(srcs[0])
				c.PreviewHTML = buildPreviewSection(name, srcs[0], detectPlatforms(name))
			}
		}

		comps = append(comps, c)
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].Name < comps[j].Name })
	return comps
}

var platformOrder = []string{"html", "android", "bubbletea", "fyne"}

var platformDisplayName = map[string]string{
	"html":      "HTML (Interactive)",
	"android":   "Android",
	"bubbletea": "Bubbletea",
	"fyne":      "Fyne",
}

func detectPlatforms(name string) []string {
	dir := filepath.Join("lib", "snapshots")
	var platforms []string
	for _, p := range platformOrder {
		if _, err := os.Stat(filepath.Join(dir, name+"_"+p+".png")); err == nil {
			platforms = append(platforms, p)
		}
	}
	return platforms
}

func buildPreviewSection(name, source string, platforms []string) string {
	iframeHTML := compilePreview(source)
	if iframeHTML == "" {
		return ""
	}

	escaped := strings.ReplaceAll(iframeHTML, "&", "&amp;")
	escaped = strings.ReplaceAll(escaped, "\"", "&quot;")
	escaped = strings.ReplaceAll(escaped, "<", "&lt;")
	escaped = strings.ReplaceAll(escaped, ">", "&gt;")

	var b strings.Builder
	b.WriteString(`<div class="platform-tabs">`)
	for i, p := range platforms {
		label := platformDisplayName[p]
		if label == "" {
			label = strings.ToUpper(p[:1]) + p[1:]
		}
		if i == 0 {
			b.WriteString(`<button class="active" onclick="switchPlatform('` + p + `', this)">` + label + `</button>`)
		} else {
			b.WriteString(`<button onclick="switchPlatform('` + p + `', this)">` + label + `</button>`)
		}
	}
	b.WriteString(`</div><div class="preview-frame">`)

	for i, p := range platforms {
		hidden := ""
		if i > 0 {
			hidden = " hidden"
		}
		if p == "html" {
			b.WriteString(`<div class="preview" data-platform="html"` + hidden + `>`)
			b.WriteString(`<iframe srcdoc="` + escaped + `"></iframe>`)
			b.WriteString(`</div>`)
		} else {
			b.WriteString(`<div class="preview" data-platform="` + p + `"` + hidden + `>`)
			b.WriteString(`<img src="/assets/gallery/` + name + `_` + p + `.png" alt="` + name + ` on ` + platformDisplayName[p] + `">`)
			b.WriteString(`</div>`)
		}
	}
	b.WriteString(`</div>`)
	b.WriteString(`<script>function switchPlatform(p, btn) { document.querySelectorAll('.preview').forEach(el => el.hidden = el.dataset.platform !== p); document.querySelectorAll('.platform-tabs button').forEach(b => b.classList.remove('active')); btn.classList.add('active'); }</script>`)
	return b.String()
}

// source is a `component main { ... }` block, as checker.PrefixedExamples
// returns: written inside sngl:ui, so it names that package's declarations
// unqualified, and a component, so it renders only where a window places it.
func compilePreview(source string) string {
	program := "import . \"sngl:ui\"\n\n" + source + "\n\nwindow {\n    main()\n}\n"
	doc, err := parser.Parse("example.sngl", []byte(program))
	if err != nil {
		return ""
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: codegen.CollectPlatforms(),
		Languages: codegen.CollectLangs(),
	})
	if len(diags) > 0 {
		return ""
	}
	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("none")
	if gen == nil || lang == nil {
		return ""
	}

	optCfg := &optimize.Config{Platform: "html", Language: "none"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return ""
	}

	feats, err := codegen.CapsFor(lang.LanguageIdentifier(), gen.PlatformIdentifier())
	if err != nil {
		return ""
	}
	if err := lower.Lower(pkg, feats, lower.Options{Platform: "html"}); err != nil {
		return ""
	}

	// Unconditional. It used to run only when the target had capabilities to lower
	// for, on the reading that a build lowering nothing had nothing new to fold --
	// which stopped being true when passQuery became always-on: it synthesizes a
	// thunk after the optimizer has walked every declaration, so the calls inside
	// one are calls nothing has looked at.
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return ""
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lang,
		Options: codegen.OptionsFromMap(map[string]any{"preview": true}),
	}, mem); err != nil {
		return ""
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	return ""
}

// firstSentence trims a doc at the first ". " boundary, or the first line.
func firstSentence(doc string) string {
	return lookup.FirstSentence(doc)
}

func autoTitle(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "Home"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Pure so SNGL components can call it inline: the optimizer folds it to a
// literal at compile time when both args are const.
//
//sngl:pure
func Highlight(src, language string) string { return docsite.Highlight(src, language) }
