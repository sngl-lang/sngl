// Command docsgen builds the documentation site.
//
// Usage: go tool docsgen [-out _site] [-http :3580]
package main

import (
	"flag"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/docs"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

func main() {
	outDir := flag.String("out", "_site", "output directory")
	httpAddr := flag.String("http", "", "start HTTP server after build (e.g., :3580)")
	binaries := flag.Bool("binaries", false, "cross-compile sngl CLI downloads and inject the download table")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Build playground WASM + stage wasm_exec.js into the project tree so
	// website.sngl's file: import resolves them. Must precede compileSNGL
	// so the SNGL compiler picks up content-hashed filenames.
	if err := prebuildPlaygroundAssets(); err != nil {
		log.Fatalf("playground prebuild: %v", err)
	}

	// Compile website.sngl → output files.
	if err := compileSNGL("website.sngl", *outDir); err != nil {
		log.Fatal(err)
	}

	// Copy pre-generated stdlib example snapshots to gallery.
	copySnapshots(*outDir)

	// Optionally cross-compile sngl downloads and inject the download table.
	// Gated by -binaries so local builds stay fast; CI passes the flag.
	if *binaries {
		version, commit, date := resolveVersion()
		repoRoot, err := os.Getwd()
		if err != nil {
			log.Fatalf("binaries: %v", err)
		}
		arts, err := buildBinaries(repoRoot, *outDir, version, commit, date)
		if err != nil {
			log.Fatalf("binaries: %v", err)
		}
		if err := injectDownloads(*outDir, arts); err != nil {
			log.Printf("downloads: %v", err)
		}
	}

	// Inject runtime example/lesson sources into the SNGL-generated pages.
	if err := injectExamples(*outDir); err != nil {
		log.Printf("playground: %v", err)
	}
	if err := injectTutorialLessons(*outDir); err != nil {
		log.Printf("tutorial: %v", err)
	}

	// Stamp every page footer with the commit the site was built from.
	if err := injectFooter(*outDir, resolveStamp()); err != nil {
		log.Printf("footer: %v", err)
	}

	// Inject the theme-toggle bootstrap into every page. Must run after
	// any other post-processing that rewrites HTML (playground, tutorial).
	if err := injectThemeBootstrap(*outDir); err != nil {
		log.Printf("theme bootstrap: %v", err)
	}

	fmt.Printf("Site built in %s/\n", *outDir)

	if *httpAddr != "" {
		fmt.Printf("Serving on http://localhost%s\n", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, http.FileServer(http.Dir(*outDir))))
	}
}

func copySnapshots(outDir string) {
	srcDir := filepath.Join("lib", "snapshots")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		log.Printf("gallery: no snapshots in %s (run: go generate ./lib/...)", srcDir)
		return
	}
	galleryDir := filepath.Join(outDir, "assets", "gallery")
	os.MkdirAll(galleryDir, 0o755)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(galleryDir, e.Name()), data, 0o644)
	}
	log.Printf("gallery: copied %d snapshots", len(entries))
}

func compileSNGL(filename, outDir string) error {
	cmd := exec.Command("go", "tool", "sngl", "generate",
		"--platform", "html", "--lang", "none", "--out", outDir, filename)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// prebuildPlaygroundAssets builds the playground WASM and copies wasm_exec.js
// into internal/playground/assets/ so website.sngl can reference them via its
// file: import. Both targets are gitignored build artifacts. Sibling
// playground.css / playground.js / tutorial.* files already live in that
// directory.
func prebuildPlaygroundAssets() error {
	stageDir := filepath.Join("internal", "playground", "assets")

	wasmOut := filepath.Join(stageDir, "sngl.wasm")
	cmd := exec.Command("go", "build", "-o", wasmOut, "./internal/playground/cmd")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building playground wasm: %w", err)
	}
	log.Printf("playground: built %s", wasmOut)

	gorootOut, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("finding GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(gorootOut))
	wasmExecData, err := os.ReadFile(filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return fmt.Errorf("reading wasm_exec.js: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "wasm_exec.js"), wasmExecData, 0o644); err != nil {
		return fmt.Errorf("writing wasm_exec.js: %w", err)
	}
	return nil
}

func injectExamples(outDir string) error {
	pgHTML := filepath.Join(outDir, "playground.html")
	htmlData, err := os.ReadFile(pgHTML)
	if err != nil {
		return fmt.Errorf("reading playground.html: %w", err)
	}

	type example struct {
		name, label, source string
	}
	var examples []example
	exEntries, _ := os.ReadDir("examples")
	for _, entry := range exEntries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join("examples", entry.Name())
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".sngl") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			src := strings.TrimSpace(string(data))
			if strings.Contains(src, `"go:`) {
				continue // skip examples with go: imports (not supported in WASM)
			}
			name := entry.Name()
			label := strings.ReplaceAll(name, "-", " ")
			words := strings.Fields(label)
			for i, w := range words {
				if len(w) > 0 {
					words[i] = strings.ToUpper(w[:1]) + w[1:]
				}
			}
			label = strings.Join(words, " ")
			examples = append(examples, example{name: name, label: label, source: src})
		}
	}

	// Build example source scripts and inject into page.
	var scriptTags string
	if len(examples) > 0 {
		var optionTags strings.Builder
		for _, ex := range examples {
			optionTags.WriteString(fmt.Sprintf("<option value=%q>%s</option>", ex.name, ex.label))
			scriptTags += fmt.Sprintf("<script type=\"text/sngl\" id=%q>%s</script>\n", ex.name+"-source", ex.source)
		}
		scriptTags = strings.Replace(scriptTags, fmt.Sprintf(`id="%s-source"`, examples[0].name), `id="default-source"`, 1)

		html := string(htmlData)
		html = strings.Replace(html, `<option value="default">Hello</option>`, optionTags.String(), 1)
		htmlData = []byte(html)
		log.Printf("playground: %d examples injected", len(examples))
	} else {
		scriptTags = "<script type=\"text/sngl\" id=\"default-source\">import . \"sngl:std\"\n\ncomponent main {\n    vbox(style={padding=16}) {\n        text(value=\"Hello, SNGL!\")\n    }\n}</script>"
	}

	pgHTMLStr := strings.Replace(string(htmlData), `<div id="playground-sources">`, `<div id="playground-sources">`+scriptTags, 1)
	os.WriteFile(pgHTML, []byte(pgHTMLStr), 0o644)
	log.Printf("playground: ready")
	return nil
}

// injectTutorialLessons rewrites /tutorial.html to embed each lesson's seed
// source and rendered prose as <script type="text/sngl"> and <template> blocks
// inside the pre-existing <div id="lesson-sources"> placeholder. tutorial.js
// reads these by id at runtime.
func injectTutorialLessons(outDir string) error {
	tutPath := filepath.Join(outDir, "tutorial.html")
	data, err := os.ReadFile(tutPath)
	if err != nil {
		return fmt.Errorf("reading tutorial.html: %w", err)
	}

	var b strings.Builder
	count := 0
	for _, s := range docs.Tutorial() {
		for _, l := range s.Lessons {
			count++
			fmt.Fprintf(&b,
				"<script type=\"text/sngl\" id=\"lesson-code-%s\">%s</script>\n",
				html.EscapeString(l.Slug),
				escapeScriptContent(l.Code),
			)
			fmt.Fprintf(&b,
				"<template id=\"lesson-prose-%s\">%s</template>\n",
				html.EscapeString(l.Slug),
				l.BodyHTML,
			)
		}
	}

	out := strings.Replace(string(data), `<div id="lesson-sources">`, `<div id="lesson-sources">`+b.String(), 1)
	if err := os.WriteFile(tutPath, []byte(out), 0o644); err != nil {
		return fmt.Errorf("writing tutorial.html: %w", err)
	}
	log.Printf("tutorial: %d lessons injected", count)
	return nil
}

// escapeScriptContent keeps SNGL code intact inside a <script> tag. The only
// sequence the HTML parser treats specially is "</script>", which would end
// the block early; escape the opening angle bracket so the browser does not
// terminate the tag.
func escapeScriptContent(s string) string {
	return strings.ReplaceAll(s, "</script>", "<\\/script>")
}

// themeBootstrap runs synchronously in <head> before any paint. It applies
// a previously-saved manual theme so dark-mode users don't see a white flash
// when they've forced light, and vice versa.
const themeBootstrap = `<script>(function(){var t=localStorage.getItem('theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);})();</script>`

// themeToggle wires the sidebar toggle button (delegated so it survives nav
// re-renders) and persists the choice to localStorage.
const themeToggle = `<script>document.addEventListener('click',function(e){var t=e.target&&e.target.closest&&e.target.closest('#theme-toggle');if(!t)return;var c=document.documentElement.getAttribute('data-theme');var n=c==='dark'?'light':'dark';if(!c){n=window.matchMedia('(prefers-color-scheme: dark)').matches?'light':'dark';}document.documentElement.setAttribute('data-theme',n);localStorage.setItem('theme',n);});</script>`

// injectThemeBootstrap walks the output directory and patches every HTML
// page so the manual light/dark toggle works site-wide without requiring
// SNGL to render <head>.
func injectThemeBootstrap(outDir string) error {
	count := 0
	err := filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(data)
		// Idempotent — skip if we've already injected.
		if strings.Contains(s, "data-theme") && strings.Contains(s, "#theme-toggle") {
			return nil
		}
		s = strings.Replace(s, "<head>", "<head>"+themeBootstrap, 1)
		s = strings.Replace(s, "</body>", themeToggle+"</body>", 1)
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	log.Printf("theme: bootstrap injected into %d pages", count)
	return nil
}
