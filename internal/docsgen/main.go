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
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/internal/docsite"
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

	fmt.Printf("Site built in %s/\n", *outDir)
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
