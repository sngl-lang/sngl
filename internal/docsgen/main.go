// Command docsgen builds the documentation site with platform snapshots.
//
// Usage: go tool docsgen [-docs docs] [-out _site]
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/playground"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func main() {
	docsDir := flag.String("docs", "docs", "documentation source directory")
	outDir := flag.String("out", "_site", "output directory")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Generate snapshots for all examples.
	generateSnapshots(*docsDir, *outDir)

	// Build the documentation site.
	if err := docsite.Build(*docsDir, *outDir); err != nil {
		log.Fatal(err)
	}

	// Build and copy playground assets.
	if err := buildPlayground(*docsDir, *outDir); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Site built in %s/\n", *outDir)
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

	// Embed default example source.
	examplesDir := filepath.Join(filepath.Dir(docsDir), "_examples")
	for _, name := range []string{"todo.sngl", "todo.sngl.kdl"} {
		examplePath := filepath.Join(examplesDir, "todo", name)
		exampleData, err := os.ReadFile(examplePath)
		if err != nil {
			continue
		}
		placeholder := `app {
    vbox style.padding=16 {
        text value="Hello, SNGL!"
    }
}`
		htmlData = []byte(strings.Replace(string(htmlData), placeholder, strings.TrimSpace(string(exampleData)), 1))
		break
	}

	if err := os.WriteFile(filepath.Join(outDir, "playground.html"), htmlData, 0o644); err != nil {
		return err
	}
	os.Remove(filepath.Join(playgroundDir, "playground.html"))

	log.Printf("playground: assets ready")
	return nil
}

func generateSnapshots(docsDir, outDir string) {
	examplesDir := filepath.Join(filepath.Dir(docsDir), "_examples")
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
			lower := filepath.Ext(path)
			if lower == ".kdl" || lower == ".sngl" {
				if filepath.Ext(path[:len(path)-len(lower)]) == ".sngl" || lower == ".sngl" {
					snglFiles = append(snglFiles, path)
				}
			}
			return nil
		})

		for _, sf := range snglFiles {
			snapDir := filepath.Join(outDir, "assets", "snapshots", entry.Name())
			results, err := snapshot.Generate(snapshot.Config{
				SourceFile: sf,
				OutDir:     snapDir,
			})
			if err != nil {
				log.Printf("skipping snapshots for %s: %v", sf, err)
				continue
			}
			for _, r := range results {
				log.Printf("  %s/%s → %s", entry.Name(), r.Platform, r.Path)
			}
		}
	}
}
