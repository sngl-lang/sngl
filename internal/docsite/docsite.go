package docsite

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

//go:embed defaults/layout.html defaults/nav.html defaults/style.css
var defaultAssets embed.FS

// Build walks docsDir, parses markdown files, and writes HTML to outDir.
func Build(docsDir, outDir string) error {
	pages, err := collectPages(docsDir)
	if err != nil {
		return fmt.Errorf("collecting pages: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Errorf("no markdown files found in %s", docsDir)
	}

	nav := BuildNav(pages)
	tmpl, err := loadTemplates(docsDir)
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	chromaCSS, err := generateChromaCSS()
	if err != nil {
		return fmt.Errorf("generating syntax highlight CSS: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Write CSS
	cssDir := filepath.Join(outDir, "assets")
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		return err
	}

	userCSS, err := loadCSS(docsDir)
	if err != nil {
		return err
	}
	allCSS := userCSS + "\n" + chromaCSS
	if err := os.WriteFile(filepath.Join(cssDir, "style.css"), []byte(allCSS), 0o644); err != nil {
		return err
	}

	// Copy static assets (images, etc.)
	copyStaticAssets(docsDir, outDir)

	// Render each page
	for _, page := range pages {
		htmlContent, err := RenderMarkdown(page.Body)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", page.RelPath, err)
		}

		outPath := filepath.Join(outDir, page.Href)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		var buf bytes.Buffer
		data := templateData{
			Title:   page.FrontMatter.Title,
			Content: template.HTML(htmlContent),
			Nav:     nav,
			Current: page.Href,
		}
		if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
			return fmt.Errorf("executing template for %s: %w", page.RelPath, err)
		}

		if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}

	return nil
}

// Serve starts an HTTP server for the built site.
func Serve(dir string, port int) error {
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Serving %s on http://localhost%s\n", dir, addr)
	return http.ListenAndServe(addr, http.FileServer(http.Dir(dir)))
}

type templateData struct {
	Title   string
	Content template.HTML
	Nav     []*NavItem
	Current string
}

func collectPages(docsDir string) ([]Page, error) {
	var pages []Page
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		rel, _ := filepath.Rel(docsDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fm, body, err := ParseFrontMatter(data)
		if err != nil {
			return fmt.Errorf("parsing front matter in %s: %w", rel, err)
		}

		href := strings.TrimSuffix(rel, ".md") + ".html"
		if fm.Title == "" {
			fm.Title = pageTitleFromPath(rel)
		}

		pages = append(pages, Page{
			FrontMatter: fm,
			RelPath:     rel,
			Href:        href,
			Body:        body,
		})
		return nil
	})
	return pages, err
}

func loadTemplates(docsDir string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"isActive": func(href, current string) bool {
			return href == current
		},
		"hasActiveChild": func(children []*NavItem, current string) bool {
			for _, c := range children {
				if c.Href == current {
					return true
				}
			}
			return false
		},
	}

	tmpl := template.New("").Funcs(funcMap)

	// Try user templates first, fall back to defaults
	userLayout := filepath.Join(docsDir, "_templates", "layout.html")
	userNav := filepath.Join(docsDir, "_templates", "nav.html")

	if _, err := os.Stat(userLayout); err == nil {
		layoutData, _ := os.ReadFile(userLayout)
		navData, _ := os.ReadFile(userNav)
		if _, err := tmpl.New("layout.html").Parse(string(layoutData)); err != nil {
			return nil, err
		}
		if len(navData) > 0 {
			if _, err := tmpl.New("nav.html").Parse(string(navData)); err != nil {
				return nil, err
			}
		}
		return tmpl, nil
	}

	// Use embedded defaults
	layoutData, _ := fs.ReadFile(defaultAssets, "defaults/layout.html")
	navData, _ := fs.ReadFile(defaultAssets, "defaults/nav.html")
	if _, err := tmpl.New("layout.html").Parse(string(layoutData)); err != nil {
		return nil, err
	}
	if _, err := tmpl.New("nav.html").Parse(string(navData)); err != nil {
		return nil, err
	}
	return tmpl, nil
}

func loadCSS(docsDir string) (string, error) {
	cssPath := filepath.Join(docsDir, "assets", "style.css")
	data, err := os.ReadFile(cssPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Fall back to embedded default
			data, _ = fs.ReadFile(defaultAssets, "defaults/style.css")
			return string(data), nil
		}
		return "", err
	}
	return string(data), nil
}

func generateChromaCSS() (string, error) {
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true), chromahtml.ClassPrefix("hl-"))
	var buf bytes.Buffer
	if err := formatter.WriteCSS(&buf, style); err != nil {
		return "", err
	}
	// Also generate dark theme variant
	darkStyle := styles.Get("github-dark")
	if darkStyle == nil {
		darkStyle = styles.Get("monokai")
	}
	if darkStyle != nil {
		buf.WriteString("\n@media (prefers-color-scheme: dark) {\n")
		var darkBuf bytes.Buffer
		if err := formatter.WriteCSS(&darkBuf, darkStyle); err == nil {
			buf.Write(darkBuf.Bytes())
		}
		buf.WriteString("}\n")
	}
	return buf.String(), nil
}

func copyStaticAssets(docsDir, outDir string) {
	exts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true}
	filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return err
		}
		if !exts[filepath.Ext(path)] {
			return nil
		}
		rel, _ := filepath.Rel(docsDir, path)
		dest := filepath.Join(outDir, "assets", rel)
		os.MkdirAll(filepath.Dir(dest), 0o755)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		os.WriteFile(dest, data, 0o644)
		return nil
	})
}

func pageTitleFromPath(rel string) string {
	name := filepath.Base(rel)
	name = strings.TrimSuffix(name, ".md")
	if name == "index" {
		name = filepath.Base(filepath.Dir(rel))
		if name == "." {
			return "Home"
		}
	}
	name = strings.ReplaceAll(name, "-", " ")
	return strings.Title(name) //nolint:staticcheck
}
