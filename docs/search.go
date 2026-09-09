package docs

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/docs/lookup"
)

// SearchEntry is one searchable record surfaced to the docs site's search
// box. Built once at compile time by SearchIndex and rendered into a SNGL
// component (no runtime fetch). Field names are kept short — they ship as
// part of the generated output for every page on the site.
type SearchEntry struct {
	Title string // display title ("button", "Tutorial", "color.darken")
	Href  string // absolute URL within the site
	Crumb string // breadcrumb-style category ("Standard Library / Types")
	Blurb string // first sentence of the doc, may be empty
	Kind  string // discriminator: "component"|"type"|"method"|"enum"|"member"|"func"|"const"|"data"|"override"|"platform-type"|"platform"|"language"|"page"|"lesson"
}

// Stable ordering: titles ascending after dedupe, so the SNGL renderer doesn't
// have to sort at compile time.
//
//sngl:pure
func SearchIndex() []SearchEntry {
	seen := map[string]bool{}
	var out []SearchEntry
	add := func(e SearchEntry) {
		if e.Href == "" || seen[e.Href] {
			return
		}
		seen[e.Href] = true
		out = append(out, e)
	}

	for _, pkg := range lookup.LibraryPackages() {
		idx := lookup.PackageIndex(pkg.Path)
		if !idx.Found {
			continue
		}
		pkgCrumb := pkg.Title
		for _, c := range idx.Components {
			add(SearchEntry{
				Title: c.Name,
				Href:  declHref(pkg.Path, "components", c.Name, ""),
				Crumb: pkgCrumb + " / Components",
				Blurb: c.Doc,
				Kind:  "component",
			})
		}
		for _, t := range idx.Types {
			add(SearchEntry{
				Title: t.Name,
				Href:  declHref(pkg.Path, "types", t.Name, ""),
				Crumb: pkgCrumb + " / Types",
				Blurb: lookup.FirstSentence(t.Doc),
				Kind:  "type",
			})
			for _, m := range t.Methods {
				add(SearchEntry{
					Title: t.Name + "." + m.Short,
					Href:  declHref(pkg.Path, "types", t.Name, m.Short),
					Crumb: pkgCrumb + " / Types / " + t.Name,
					Blurb: lookup.FirstSentence(m.Doc),
					Kind:  "method",
				})
			}
		}
		for _, e := range idx.Enums {
			add(SearchEntry{
				Title: e.Name,
				Href:  declHref(pkg.Path, "enums", e.Name, ""),
				Crumb: pkgCrumb + " / Enums",
				Blurb: e.Doc,
				Kind:  "enum",
			})
		}
		for _, f := range idx.Functions {
			add(SearchEntry{
				Title: f.Name,
				Href:  declHref(pkg.Path, "functions", f.Name, ""),
				Crumb: pkgCrumb + " / Functions",
				Blurb: f.Doc,
				Kind:  "func",
			})
		}
		for _, c := range idx.Constants {
			add(SearchEntry{
				Title: c.Name,
				Href:  declHref(pkg.Path, "constants", c.Name, ""),
				Crumb: pkgCrumb + " / Constants",
				Blurb: c.Doc,
				Kind:  "const",
			})
		}
		for _, d := range idx.Data {
			add(SearchEntry{
				Title: d.Name,
				Href:  declHref(pkg.Path, "data", d.Name, ""),
				Crumb: pkgCrumb + " / Data",
				Blurb: d.Doc,
				Kind:  "data",
			})
		}
		for _, o := range idx.Overrides {
			add(SearchEntry{
				Title: o.Name,
				Href:  declHref(pkg.Path, "overrides", o.Name, ""),
				Crumb: pkgCrumb + " / Overrides",
				Blurb: o.Doc,
				Kind:  "override",
			})
		}
	}

	// Enum members — pulled from AllDeclPages because PackageIndex's enum
	// summary doesn't expose members.
	for _, p := range lookup.AllDeclPages() {
		if p.Kind != "enums" || p.Ident2 == "" {
			continue
		}
		add(SearchEntry{
			Title: p.Name + "." + p.Ident2,
			Href:  p.Href,
			Crumb: "Standard Library / Enums / " + p.Name,
			Kind:  "member",
		})
	}

	for _, p := range Pages() {
		crumb := "Docs"
		if p.Section != "" {
			crumb = strings.Title(p.Section)
		}
		add(SearchEntry{
			Title: p.Title,
			Href:  p.Href,
			Crumb: crumb,
			Blurb: p.Description,
			Kind:  "page",
		})
	}

	cat := Targets()
	for _, p := range cat.Platforms {
		add(SearchEntry{
			Title: p.Name,
			Href:  "/platforms/index.html#" + p.Name,
			Crumb: "Reference / Platforms",
			Blurb: p.Doc,
			Kind:  "platform",
		})
	}
	for _, l := range cat.Languages {
		add(SearchEntry{
			Title: l.Name,
			Href:  "/languages/index.html#" + l.Name,
			Crumb: "Reference / Languages",
			Blurb: l.Doc,
			Kind:  "language",
		})
	}

	// The tutorial page reads `#lesson=<slug>` on load.
	for _, s := range Tutorial() {
		for _, l := range s.Lessons {
			add(SearchEntry{
				Title: l.Title,
				Href:  "/tutorial.html#lesson=" + l.Slug,
				Crumb: "Learn / Tour / " + s.Title,
				Kind:  "lesson",
			})
		}
	}

	return out
}

// declHref mirrors lookup.declPageHref (which is unexported). Kept in sync
// by inspection — both build the canonical /docs/<pkg>/<kind>/<name>.html
// (with optional /<ident2>) URL shape.
func declHref(pkg, kind, name, ident2 string) string {
	base := "/docs/" + strings.ReplaceAll(pkg, ":", "/") + "/" + name
	if ident2 != "" {
		return base + "/" + ident2 + ".html"
	}
	return base + ".html"
}
