// Package docs embeds the SNGL documentation markdown files and provides
// pure functions for compile-time page generation.
package docs

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

var workspaceDir string

// SetWorkspaceDir sets the directory to scan for workspace .sngl files.
// When set, Components() includes workspace component declarations before stdlib.
func SetWorkspaceDir(dir string) { workspaceDir = dir }

//go:embed *.md getting-started language
var content embed.FS

// Page holds the data for a single documentation page.
type Page struct {
	Title       string
	Slug        string // URL slug, e.g. "getting-started/installation"
	Href        string // URL path, e.g. "/getting-started/installation.html"
	Description string
	Body        string // rendered HTML from markdown
	Order       int
	Section     string // "getting-started", "language", "" for root
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
		if before, ok := strings.CutSuffix(slug, "/index"); ok {
			slug = before
		}
		if slug == "index" {
			slug = ""
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

// Component holds metadata for a single stdlib component.
type Component struct {
	Name            string
	Doc             string
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
	registry, _, _, _, _, _, err := checker.LoadStdlib()
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
				c.PreviewHTML = buildPreviewSection(name, srcs[0])
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

// Components returns metadata for all components. When a workspace directory
// has been set via SetWorkspaceDir, workspace declarations appear first,
// followed by stdlib components.
func Components() []Component {
	var comps []Component

	// Workspace components (if dir set).
	if workspaceDir != "" {
		if doc, err := parseDir(workspaceDir); err == nil {
			pd := checker.ExtractPackageDocs(doc)
			for _, d := range pd.Components {
				if !strings.HasPrefix(d.Name, "sngl.") {
					comps = append(comps, Component{Name: d.Name, Doc: d.Doc, Tier: "workspace"})
				}
			}
		}
	}

	comps = append(comps, StdlibComponents()...)
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

// Lookup returns the component with the given name, or a zero Component if not found.
func Lookup(name string) Component {
	for _, c := range Components() {
		if c.Name == name {
			return c
		}
	}
	return Component{Name: name, Doc: "Component not found."}
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
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		d, err := parser.Parse(e.Name(), f)
		f.Close()
		if err != nil {
			continue
		}
		if doc == nil {
			doc = d
		} else {
			doc.Components = append(doc.Components, d.Components...)
		}
	}
	if doc == nil {
		return nil, fs.ErrNotExist
	}
	return doc, nil
}

// buildPreviewSection generates the full tabbed preview HTML including
// interactive iframe, bubbletea/fyne screenshot tabs.
func buildPreviewSection(name, source string) string {
	iframeHTML := compilePreview(source)
	if iframeHTML == "" {
		return ""
	}

	// Use html.EscapeString for srcdoc attribute
	escaped := strings.ReplaceAll(iframeHTML, "&", "&amp;")
	escaped = strings.ReplaceAll(escaped, "\"", "&quot;")
	escaped = strings.ReplaceAll(escaped, "<", "&lt;")
	escaped = strings.ReplaceAll(escaped, ">", "&gt;")

	return `<div class="platform-tabs">` +
		`<button class="active" onclick="switchPlatform('html', this)">HTML (Interactive)</button>` +
		`<button onclick="switchPlatform('bubbletea', this)">Bubbletea</button>` +
		`<button onclick="switchPlatform('fyne', this)">Fyne</button>` +
		`</div>` +
		`<div class="preview-frame">` +
		`<div class="preview" data-platform="html">` +
		`<iframe srcdoc="` + escaped + `"></iframe>` +
		`</div>` +
		`<div class="preview" data-platform="bubbletea" hidden>` +
		`<img src="/assets/gallery/` + name + `-bubbletea.png" alt="` + name + ` on Bubbletea" onerror="this.parentElement.remove()">` +
		`</div>` +
		`<div class="preview" data-platform="fyne" hidden>` +
		`<img src="/assets/gallery/` + name + `-fyne.png" alt="` + name + ` on Fyne" onerror="this.parentElement.remove()">` +
		`</div>` +
		`</div>` +
		`<script>function switchPlatform(p, btn) { document.querySelectorAll('.preview').forEach(el => el.hidden = el.dataset.platform !== p); document.querySelectorAll('.platform-tabs button').forEach(b => b.classList.remove('active')); btn.classList.add('active'); }</script>`
}

// compilePreview compiles a .sngl example to HTML for iframe preview.
func compilePreview(source string) string {
	doc, err := parser.Parse("example.sngl", strings.NewReader(source))
	if err != nil {
		return ""
	}
	if err := checker.Check(doc, nil, "", nil, nil, nil, true); err != nil {
		return ""
	}
	clone := doc.Clone()
	optimize.Optimize(clone, optimize.Config{Platform: "html", Language: "js"})

	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("js")
	if gen == nil || lang == nil {
		return ""
	}
	resp, err := gen.Generate(&codegen.Request{
		Doc: clone, Lang: lang,
		Options: map[string]string{"preview": "true"},
	})
	if err != nil || resp.Error != "" {
		return ""
	}
	for _, f := range resp.Files {
		if strings.HasSuffix(f.Name, ".html") {
			var buf bytes.Buffer
			f.WriteTo(&buf)
			return buf.String()
		}
	}
	return ""
}

func autoTitle(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "Home"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
