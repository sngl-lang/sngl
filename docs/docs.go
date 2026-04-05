// Package docs embeds the SNGL documentation markdown files and provides
// pure functions for compile-time page generation.
package docs

import (
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

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

func autoTitle(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "Home"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
