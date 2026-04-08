// Command docsgen builds the documentation site.
//
// Usage: go tool docsgen [-out _site] [-http :3580]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

func main() {
	outDir := flag.String("out", "_site", "output directory")
	httpAddr := flag.String("http", "", "start HTTP server after build (e.g., :3580)")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Compile website.sngl → output files.
	if err := compileSNGL("website.sngl", *outDir); err != nil {
		log.Fatal(err)
	}

	// Copy pre-generated stdlib example snapshots to gallery.
	copySnapshots(*outDir)

	// Build playground (WASM + static assets).
	if err := buildPlayground(*outDir); err != nil {
		log.Printf("playground: %v", err)
	}

	fmt.Printf("Site built in %s/\n", *outDir)

	if *httpAddr != "" {
		fmt.Printf("Serving on http://localhost%s\n", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, http.FileServer(http.Dir(*outDir))))
	}
}

func copySnapshots(outDir string) {
	srcDir := filepath.Join("internal", "checker", "stdlib", "snapshots")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		log.Printf("gallery: no snapshots in %s (run: go generate ./internal/checker/...)", srcDir)
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
	cmd := exec.Command("go", "tool", "sngl", "compile", "--out", outDir, filename)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func buildPlayground(outDir string) error {
	playgroundDir := filepath.Join(outDir, "assets", "playground")
	os.MkdirAll(playgroundDir, 0o755)

	// Build WASM binary.
	wasmOut := filepath.Join(playgroundDir, "sngl.wasm")
	cmd := exec.Command("go", "build", "-o", wasmOut, "./internal/playground/cmd")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building playground wasm: %w", err)
	}
	log.Printf("playground: built %s", wasmOut)

	// Copy wasm_exec.js from GOROOT.
	gorootOut, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("finding GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(gorootOut))
	wasmExecData, err := os.ReadFile(filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return fmt.Errorf("reading wasm_exec.js: %w", err)
	}
	os.WriteFile(filepath.Join(playgroundDir, "wasm_exec.js"), wasmExecData, 0o644)

	// Copy playground CSS and JS assets.
	for _, name := range []string{"playground.css", "playground.js"} {
		src := filepath.Join("internal", "playground", "assets", name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(playgroundDir, name), data, 0o644)
	}

	// Inject examples into the SNGL-generated playground.html.
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
			if strings.Contains(src, `"go://`) {
				continue // skip examples with go:// imports (not supported in WASM)
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
		scriptTags = "<script type=\"text/sngl\" id=\"default-source\">component main {\n    vbox(style={padding=16}) {\n        text(value=\"Hello, SNGL!\")\n    }\n}</script>"
	}

	html := strings.Replace(string(htmlData), `<div id="playground-sources">`, `<div id="playground-sources">`+scriptTags, 1)
	os.WriteFile(pgHTML, []byte(html), 0o644)
	log.Printf("playground: ready")
	return nil
}
