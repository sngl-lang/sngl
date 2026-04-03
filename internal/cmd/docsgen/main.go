// Command docsgen builds the documentation site with platform snapshots.
//
// Usage: go tool docsgen [-docs docs] [-out _site] [-http :3580]
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/playground"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

func main() {
	docsDir := flag.String("docs", "docs", "documentation source directory")
	outDir := flag.String("out", "_site", "output directory")
	httpAddr := flag.String("http", "", "start HTTP server after build (e.g., :3580)")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Generate snapshots for all examples.
	generateSnapshots(*docsDir, *outDir)

	// docsgen always builds the playground, so include it in nav.
	docsite.IncludePlayground = true

	// Build the documentation site.
	if err := docsite.Build(*docsDir, *outDir); err != nil {
		log.Fatal(err)
	}

	// Build and copy playground assets.
	if err := buildPlayground(*docsDir, *outDir); err != nil {
		log.Fatal(err)
	}

	// Generate component gallery.
	generateGallery(*docsDir, *outDir)

	fmt.Printf("Site built in %s/\n", *outDir)

	if *httpAddr != "" {
		fmt.Printf("Serving on http://localhost%s\n", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, http.FileServer(http.Dir(*outDir))))
	}
}

func buildPlayground(docsDir, outDir string) error {
	playgroundDir := filepath.Join(outDir, "assets", "playground")
	os.MkdirAll(playgroundDir, 0o755)

	// Build WASM binary.
	wasmOut := filepath.Join(playgroundDir, "sngl.wasm")
	cmd := exec.Command("go", "build", "-o", wasmOut, "./internal/playground")
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
	wasmExecSrc := filepath.Join(goroot, "lib", "wasm", "wasm_exec.js")
	wasmExecData, err := os.ReadFile(wasmExecSrc)
	if err != nil {
		return fmt.Errorf("reading wasm_exec.js: %w", err)
	}
	if err := os.WriteFile(filepath.Join(playgroundDir, "wasm_exec.js"), wasmExecData, 0o644); err != nil {
		return err
	}
	log.Printf("playground: copied wasm_exec.js")

	// Copy embedded playground assets.
	assets, err := fs.Sub(playground.Assets, "assets")
	if err != nil {
		return err
	}
	fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(playgroundDir, path)
		os.MkdirAll(filepath.Dir(dest), 0o755)
		return os.WriteFile(dest, data, 0o644)
	})

	// playground.html goes at the site root, not in assets/playground/.
	htmlData, err := os.ReadFile(filepath.Join(playgroundDir, "playground.html"))
	if err != nil {
		return err
	}

	// Inject example .sngl files from examples/ directory.
	examplesDir := filepath.Join(filepath.Dir(docsDir), "examples")
	type example struct {
		name, label, source string
	}
	var examples []example
	entries, _ := os.ReadDir(examplesDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(examplesDir, entry.Name())
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
			// Skip examples with go:// imports (not supported in WASM playground)
			if strings.Contains(src, `"go://`) {
				continue
			}
			name := entry.Name()
			label := strings.ReplaceAll(strings.Title(name), "-", " ") //nolint:staticcheck
			examples = append(examples, example{name: name, label: label, source: src})
		}
	}

	// Build <option> elements and <script> blocks for each example
	var optionTags, scriptTags string
	for _, ex := range examples {
		optionTags += fmt.Sprintf("            <option value=\"%s\">%s</option>\n", ex.name, ex.label)
		scriptTags += fmt.Sprintf("    <script type=\"text/sngl\" id=\"%s-source\">%s</script>\n", ex.name, ex.source)
	}

	// Replace the static examples dropdown
	html := string(htmlData)
	html = strings.Replace(html, `            <option value="default">Todo App</option>`, optionTags, 1)

	// Replace the static default-source script with all example scripts
	oldDefault := `    <script type="text/sngl" id="default-source">
app {
    vbox style.padding=16 {
        text value="Hello, SNGL!"
    }
}
</script>`
	html = strings.Replace(html, oldDefault, scriptTags, 1)

	// Set the first example as default-source for initial load
	if len(examples) > 0 {
		html = strings.Replace(html, fmt.Sprintf(`id="%s-source"`, examples[0].name), `id="default-source"`, 1)
	}

	if err := os.WriteFile(filepath.Join(outDir, "playground.html"), []byte(html), 0o644); err != nil {
		return err
	}
	os.Remove(filepath.Join(playgroundDir, "playground.html"))

	log.Printf("playground: %d examples injected", len(examples))
	return nil
}

func generateSnapshots(docsDir, outDir string) {
	examplesDir := filepath.Join(filepath.Dir(docsDir), "examples")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		exampleDir := filepath.Join(examplesDir, entry.Name())
		var snglFiles []string
		filepath.WalkDir(exampleDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.HasSuffix(path, ".sngl") {
				snglFiles = append(snglFiles, path)
			}
			return nil
		})

		for _, sf := range snglFiles {
			snapDir := filepath.Join(outDir, "assets", "snapshots", entry.Name())
			// Generate snapshots per-platform so one failure doesn't skip all.
			platforms := snapshot.PlatformsForFile(sf)
			var results []snapshot.Result
			for _, plat := range platforms {
				r, err := snapshot.Generate(snapshot.Config{
					SourceFile: sf,
					Platforms:  []string{plat},
					OutDir:     snapDir,
				})
				if err != nil {
					log.Printf("skipping %s/%s: %v", entry.Name(), plat, err)
					continue
				}
				results = append(results, r...)
			}
			if len(results) == 0 {
				continue
			}
			for _, r := range results {
				log.Printf("  %s/%s → %s", entry.Name(), r.Platform, r.Path)
			}
		}
	}
}
