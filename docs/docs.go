// Package docs embeds the SNGL documentation markdown files and provides
// pure functions for compile-time page generation.
package docs

import (
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
)

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
		if strings.HasSuffix(slug, "/index") {
			slug = strings.TrimSuffix(slug, "/index")
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
	Name     string
	Doc      string
	Tier     string
	Children string // "none", "one", "many"
	Props    []ComponentProp
	Events   []ComponentEvent
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

// Components returns metadata for all stdlib components, grouped by tier.
//
//sngl:pure
func Components() []Component {
	registry, _, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	tiers := docsite.AssignTiers(registry)

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
		comps = append(comps, c)
	}

	// Sort by tier order then name
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

func autoTitle(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "Home"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
