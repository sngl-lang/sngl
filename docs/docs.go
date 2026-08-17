// Package docs embeds the SNGL documentation markdown files and provides
// pure functions for compile-time page generation.
package docs

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/docs/lookup"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

//go:embed *.md learn reference
var content embed.FS

// Page holds the data for a single documentation page.
type Page struct {
	Title       string
	Slug        string // URL slug, e.g. "learn/installation"
	Href        string // URL path, e.g. "/learn/installation.html"
	Description string
	Body        string // rendered HTML from markdown
	Order       int
	Section     string // parent directory, e.g. "learn", "reference"; "" for root-level files
	IsIndex     bool   // true if this page is the section's index.md (or root index.md)
}

// Pages returns all documentation pages with rendered HTML bodies.
//
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
		if path == "learn/getting-started.md" {
			return nil // rendered separately by getting_started.sngl
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, body, err := docsite.ParseFrontMatter(data)
		if err != nil {
			return nil
		}
		html, err := docsite.RenderMarkdown(body)
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
		if slug == "" {
			href = "/index.html"
		}

		pages = append(pages, Page{
			Title:       title,
			Slug:        slug,
			Href:        href,
			Description: fm.Description,
			Body:        string(html),
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

// NavLink is a single sidebar entry.
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
	{"learn", NavLink{Title: "Getting Started", Href: "/learn/getting-started.html", Order: 20}},
	{"learn", NavLink{Title: "Tour", Href: "/tutorial.html", Order: 30}},
	{"reference", NavLink{Title: "Components", Href: "/components/index.html", Order: 1}},
	{"reference", NavLink{Title: "Standard Library", Href: "/docs/sngl/index.html", Order: 2}},
	{"reference", NavLink{Title: "Platforms", Href: "/platforms/index.html", Order: 3}},
	{"reference", NavLink{Title: "Languages", Href: "/languages/index.html", Order: 4}},
	{"", NavLink{Title: "Playground", Href: "/playground.html", Order: 1000}},
}

// NavTree returns the resolved sidebar tree. The structure mirrors the docs
// directory: each subdirectory with an index.md becomes a section heading,
// other markdown files in that subdirectory become items under it. Top-level
// entries (Home, Playground) come from navExtras with Section "".
//
// Pages with no parent index.md (e.g. docs/index.md) are still emitted but
// stay out of the sidebar — link to them explicitly from another page if you
// want them reachable.
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

	// navExtras: top-level entries go straight to the output; section
	// extras attach to an existing section.
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

	// Skip sections that have no index.md — they're considered hidden.
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

// Component holds metadata for a single stdlib component.
type Component struct {
	Name            string
	Doc             string // full doc string
	Blurb           string // first sentence of Doc, for index/card listings
	Tier            string
	Children        string // "none", "one", "many"
	Props           []ComponentProp
	Events          []ComponentEvent
	Examples        []string // raw .sngl source for each example
	HighlightedCode string   // syntax-highlighted HTML of the first example
	PreviewHTML     string   // compiled HTML for interactive iframe preview
}

// ComponentProp describes a component property.
type ComponentProp struct {
	Name string
	Type string
	Doc  string
}

// ComponentEvent describes a component event.
type ComponentEvent struct {
	Name        string
	PayloadType string
}

// Tier groups components by their tier for the gallery.
type Tier struct {
	Name       string
	Components []Component
}

// StdlibComponents returns metadata for all stdlib components.
//
//sngl:pure
func StdlibComponents() []Component {
	registry, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	tiers := docsite.AssignTiers(registry)
	stdlibExamples, _ := checker.StdlibExamples()

	var comps []Component
	for name, schema := range registry {
		c := Component{
			Name:     name,
			Doc:      schema.Doc,
			Blurb:    firstSentence(schema.Doc),
			Tier:     tiers[name],
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
		for ename, payload := range schema.Events {
			c.Events = append(c.Events, ComponentEvent{
				Name:        ename,
				PayloadType: payload,
			})
		}
		sort.Slice(c.Events, func(i, j int) bool { return c.Events[i].Name < c.Events[j].Name })

		if srcs, ok := stdlibExamples[name]; ok {
			c.Examples = srcs
			if len(srcs) > 0 {
				c.HighlightedCode = docsite.HighlightSNGL(srcs[0])
				c.PreviewHTML = buildPreviewSection(name, srcs[0], detectPlatforms(name))
			}
		}

		comps = append(comps, c)
	}

	// Sort by tier order then name.
	tierIdx := map[string]int{}
	for i, t := range docsite.TierOrder {
		tierIdx[t] = i
	}
	sort.Slice(comps, func(i, j int) bool {
		ti := tierIdx[comps[i].Tier]
		tj := tierIdx[comps[j].Tier]
		if ti != tj {
			return ti < tj
		}
		return comps[i].Name < comps[j].Name
	})

	return comps
}

// ComponentsByTier returns stdlib components grouped by tier for the gallery.
//
//sngl:pure
func ComponentsByTier() []Tier {
	comps := StdlibComponents()
	tierMap := map[string][]Component{}
	for _, c := range comps {
		tierMap[c.Tier] = append(tierMap[c.Tier], c)
	}
	var tiers []Tier
	for _, t := range docsite.TierOrder {
		if cs, ok := tierMap[t]; ok {
			tiers = append(tiers, Tier{Name: t, Components: cs})
		}
	}
	return tiers
}

// StdlibLookup returns the stdlib component with the given name.
//
//sngl:pure
func StdlibLookup(name string) Component {
	for _, c := range StdlibComponents() {
		if c.Name == name {
			return c
		}
	}
	return Component{Name: name, Doc: "Component not found."}
}

func parseDir(dir string) (*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var doc *ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		d, err := parser.Parse(e.Name(), data)
		if err != nil {
			continue
		}
		if doc == nil {
			doc = d
		} else {
			doc.Stmts = append(doc.Stmts, d.Stmts...)
		}
	}
	if doc == nil {
		return nil, fs.ErrNotExist
	}
	return doc, nil
}

// platformOrder defines the display order for platform tabs.
var platformOrder = []string{"html", "android", "bubbletea", "fyne"}

// platformDisplayName returns a human-readable label for a platform.
var platformDisplayName = map[string]string{
	"html":      "HTML (Interactive)",
	"android":   "Android",
	"bubbletea": "Bubbletea",
	"fyne":      "Fyne",
}

// detectPlatforms scans the stdlib snapshots directory and returns the
// platforms that have a .png snapshot for the given component name,
// sorted by platformOrder.
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

// buildPreviewSection generates the full tabbed preview HTML including
// interactive iframe and per-platform screenshot tabs.
func buildPreviewSection(name, source string, platforms []string) string {
	iframeHTML := compilePreview(source)
	if iframeHTML == "" {
		return ""
	}

	// Use html.EscapeString for srcdoc attribute
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

// compilePreview compiles a .sngl example to HTML for iframe preview.
// The source is a full `component main { ... }` block (as returned by
// checker.PrefixedExamples).
func compilePreview(source string) string {
	doc, err := parser.Parse("example.sngl", []byte(source))
	if err != nil {
		return ""
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
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

	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "html"}); err != nil {
		return ""
	}

	if caps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return ""
		}
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

// firstSentence trims a doc to its first sentence (". " boundary) or first
// line. Used for one-line blurbs on index/card lists.
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

// Highlight runs Chroma over a source string for the given lexer name and
// returns the resulting HTML. Pure so SNGL components can call it inline;
// the optimizer folds it to a literal at compile time when both args are
// const.
//
//sngl:pure
func Highlight(src, language string) string { return docsite.Highlight(src, language) }
