package docsite

import (
	"path/filepath"
	"sort"
	"strings"
)

// NavItem represents a page or section in the site navigation.
type NavItem struct {
	Title    string
	Href     string
	Order    int
	Children []*NavItem
}

// Page holds all the data for a single documentation page.
type Page struct {
	FrontMatter
	// RelPath is the path relative to the docs root (e.g. "getting-started/installation.md").
	RelPath string
	// Href is the URL path for the page (e.g. "getting-started/installation.html").
	Href string
	// Body is the markdown content after front matter is stripped.
	Body []byte
}

// BuildNav creates a nav tree from a set of pages. Pages are grouped by
// directory (first path segment) and sorted by their Order field.
func BuildNav(pages []Page) []*NavItem {
	sections := map[string]*NavItem{}
	var topLevel []*NavItem

	for i := range pages {
		p := &pages[i]
		dir := filepath.Dir(p.RelPath)
		base := filepath.Base(p.RelPath)

		item := &NavItem{
			Title: p.FrontMatter.Title,
			Href:  p.Href,
			Order: p.FrontMatter.Order,
		}

		if dir == "." {
			// Top-level page
			topLevel = append(topLevel, item)
			continue
		}

		section, ok := sections[dir]
		if !ok {
			section = &NavItem{
				Title: sectionTitle(dir),
				Order: p.FrontMatter.Order, // use first page's order for section
			}
			sections[dir] = section
			topLevel = append(topLevel, section)
		}

		// index.md items set the section title/href
		if base == "index.md" {
			section.Title = p.FrontMatter.Title
			section.Href = p.Href
			section.Order = p.FrontMatter.Order
		} else {
			section.Children = append(section.Children, item)
		}
	}

	for _, s := range sections {
		sort.Slice(s.Children, func(i, j int) bool {
			if s.Children[i].Order != s.Children[j].Order {
				return s.Children[i].Order < s.Children[j].Order
			}
			return s.Children[i].Title < s.Children[j].Title
		})
	}

	topLevel = append(topLevel, &NavItem{
		Title: "Components",
		Href:  "components/index.html",
		Order: 9998,
	})

	topLevel = append(topLevel, &NavItem{
		Title: "Playground",
		Href:  "playground.html",
		Order: 9999,
	})

	sort.Slice(topLevel, func(i, j int) bool {
		if topLevel[i].Order != topLevel[j].Order {
			return topLevel[i].Order < topLevel[j].Order
		}
		return topLevel[i].Title < topLevel[j].Title
	})

	return topLevel
}

func sectionTitle(dir string) string {
	name := filepath.Base(dir)
	name = strings.ReplaceAll(name, "-", " ")
	return strings.Title(name) //nolint:staticcheck
}
