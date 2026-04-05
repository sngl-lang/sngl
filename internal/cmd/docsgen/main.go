// Command docsgen builds the documentation site with platform snapshots.
//
// Usage: go tool docsgen [-out _site] [-http :3580]
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

	"git.duckfam.us/jonathan/sngl/internal/snapshot"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

func main() {
	outDir := flag.String("out", "_site", "output directory")
	httpAddr := flag.String("http", "", "start HTTP server after build (e.g., :3580)")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Generate snapshots for all examples.
	generateSnapshots(*outDir)

	// Compile website.sngl → output files.
	if err := compileSNGL("website.sngl", *outDir); err != nil {
		log.Fatal(err)
	}

	// Generate component screenshots for gallery.
	generateComponentSnapshots(*outDir)

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

func generateComponentSnapshots(outDir string) {
	componentsDir := filepath.Join("docs", "components")
	entries, err := os.ReadDir(componentsDir)
	if err != nil {
		return
	}
	galleryDir := filepath.Join(outDir, "assets", "gallery")
	os.MkdirAll(galleryDir, 0o755)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sngl")
		sourceFile := filepath.Join(componentsDir, e.Name())

		for _, platform := range []string{"bubbletea", "fyne"} {
			results, err := snapshot.Generate(snapshot.Config{
				SourceFile: sourceFile,
				Platforms:  []string{platform},
				OutDir:     galleryDir,
				Width:      800,
				Height:     400,
			})
			if err != nil {
				continue
			}
			for _, r := range results {
				target := filepath.Join(galleryDir, name+"-"+platform+".png")
				if r.Path != target {
					os.Rename(r.Path, target)
				}
				log.Printf("gallery: %s/%s → %s", name, platform, target)
			}
		}
	}
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

	// Copy playground assets (HTML, CSS, JS).
	for _, name := range []string{"playground.html", "playground.css", "playground.js"} {
		src := filepath.Join("internal", "playground", "assets", name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(playgroundDir, name), data, 0o644)
	}

	// Inject examples from examples/ directory into playground HTML.
	pgHTML := filepath.Join(playgroundDir, "playground.html")
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
			// Simple title case
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

	if len(examples) > 0 {
		// Build <option> elements
		var optionTags string
		for _, ex := range examples {
			optionTags += fmt.Sprintf("            <option value=%q>%s</option>\n", ex.name, ex.label)
		}

		// Build <script> blocks
		var scriptTags string
		for _, ex := range examples {
			scriptTags += fmt.Sprintf("    <script type=\"text/sngl\" id=%q>%s</script>\n", ex.name+"-source", ex.source)
		}

		html := string(htmlData)
		// Replace the static dropdown option
		html = strings.Replace(html, `            <option value="default">Todo App</option>`, optionTags, 1)
		// Replace the static default source
		oldDefault := `    <script type="text/sngl" id="default-source">
app {
    vbox style.padding=16 {
        text value="Hello, SNGL!"
    }
}
</script>`
		html = strings.Replace(html, oldDefault, scriptTags, 1)
		// Set the first example as default
		if len(examples) > 0 {
			html = strings.Replace(html, fmt.Sprintf(`id="%s-source"`, examples[0].name), `id="default-source"`, 1)
		}
		htmlData = []byte(html)
		log.Printf("playground: %d examples injected", len(examples))
	}

	os.WriteFile(filepath.Join(outDir, "playground.html"), htmlData, 0o644)
	os.Remove(pgHTML)

	log.Printf("playground: ready")
	return nil
}

func compileSNGL(filename, outDir string) error {
	cmd := exec.Command("go", "tool", "sngl", "compile", "--out", outDir, filename)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func generateSnapshots(outDir string) {
	examplesDir := "examples"
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
			for _, r := range results {
				log.Printf("  %s/%s → %s", entry.Name(), r.Platform, r.Path)
			}
		}
	}
}
