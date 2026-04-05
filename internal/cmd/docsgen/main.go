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

	// Copy static assets from public/ and docs/
	copyAssets(*outDir)

	fmt.Printf("Site built in %s/\n", *outDir)

	if *httpAddr != "" {
		fmt.Printf("Serving on http://localhost%s\n", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, http.FileServer(http.Dir(*outDir))))
	}
}

func copyAssets(outDir string) {
	assetsDir := filepath.Join(outDir, "assets")
	os.MkdirAll(assetsDir, 0o755)

	// Copy public/ directory contents to assets/
	filepath.WalkDir("public", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel("public", path)
		dest := filepath.Join(assetsDir, rel)
		os.MkdirAll(filepath.Dir(dest), 0o755)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		os.WriteFile(dest, data, 0o644)
		return nil
	})

	// Copy docs/ images (svg, png)
	filepath.WalkDir("docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		switch ext {
		case ".svg", ".png", ".jpg", ".ico", ".gif":
			rel, _ := filepath.Rel("docs", path)
			dest := filepath.Join(assetsDir, rel)
			os.MkdirAll(filepath.Dir(dest), 0o755)
			data, _ := os.ReadFile(path)
			os.WriteFile(dest, data, 0o644)
		}
		return nil
	})
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
