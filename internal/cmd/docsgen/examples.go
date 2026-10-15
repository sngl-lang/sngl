package main

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/txtar"
)

type example struct {
	name, label, source string
}

// exampleArchive is an example directory as the txtar the playground reads:
// one root .sngl file as the archive's comment, which the editor's language
// services answer for, and every other text file as a section. ok is false
// for an example the playground cannot build.
func exampleArchive(fsys fs.FS) (source string, ok bool, err error) {
	var names []string
	files := map[string][]byte{}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil
		}
		names = append(names, p)
		files[p] = data
		return nil
	})
	if err != nil {
		return "", false, err
	}
	sort.Strings(names)

	main := ""
	for _, n := range names {
		if !strings.HasSuffix(n, ".sngl") {
			continue
		}
		// The go: scheme needs a Go toolchain, which the browser has none of.
		if bytes.Contains(files[n], []byte(`"go:`)) {
			return "", false, nil
		}
		if path.Dir(n) != "." || n == "doc.sngl" || strings.HasSuffix(n, "_test.sngl") {
			continue
		}
		if main == "" {
			main = n
		}
	}
	if main == "" {
		return "", false, nil
	}

	arc := &txtar.Archive{Comment: files[main]}
	for _, n := range names {
		if n != main {
			arc.Files = append(arc.Files, txtar.File{Name: n, Data: files[n]})
		}
	}
	return strings.TrimSpace(string(txtar.Format(arc))), true, nil
}

func exampleLabel(name string) string {
	words := strings.Fields(strings.ReplaceAll(name, "-", " "))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func loadExamples(dir string) []example {
	entries, _ := os.ReadDir(dir)
	var out []example
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		src, ok, err := exampleArchive(os.DirFS(filepath.Join(dir, entry.Name())))
		if err != nil {
			log.Printf("playground: %s: %v", entry.Name(), err)
			continue
		}
		if !ok {
			continue
		}
		out = append(out, example{name: entry.Name(), label: exampleLabel(entry.Name()), source: src})
	}
	return out
}

func injectExamples(outDir string) error {
	pgHTML := filepath.Join(outDir, "playground.html")
	htmlData, err := os.ReadFile(pgHTML)
	if err != nil {
		return fmt.Errorf("reading playground.html: %w", err)
	}
	page := string(htmlData)

	examples := loadExamples("examples")
	var scriptTags strings.Builder
	if len(examples) > 0 {
		var optionTags strings.Builder
		for i, ex := range examples {
			fmt.Fprintf(&optionTags, "<option value=%q>%s</option>", ex.name, html.EscapeString(ex.label))
			id := ex.name + "-source"
			if i == 0 {
				id = "default-source"
			}
			fmt.Fprintf(&scriptTags, "<script type=\"text/sngl\" id=%q>%s</script>\n", id, escapeScriptContent(ex.source))
		}
		page = strings.Replace(page, `<option value="default">Hello</option>`, optionTags.String(), 1)
		log.Printf("playground: %d examples injected", len(examples))
	} else {
		scriptTags.WriteString("<script type=\"text/sngl\" id=\"default-source\">import ui \"sngl:ui\"\n\nui.window {\n    ui.vbox(style={padding=16px}) {\n        ui.text(value=\"Hello, SNGL!\")\n    }\n}</script>")
	}

	page = strings.Replace(page, `<div id="playground-sources">`, `<div id="playground-sources">`+scriptTags.String(), 1)
	if err := os.WriteFile(pgHTML, []byte(page), 0o644); err != nil {
		return fmt.Errorf("writing playground.html: %w", err)
	}
	log.Printf("playground: ready")
	return nil
}
