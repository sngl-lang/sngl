package main

import (
	"log"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
)

func generateGallery(docsDir, outDir string) {
	layout, _ := docsite.LoadLayout(docsDir)
	err := docsite.GenerateGallery(docsDir, outDir, layout, func(name, examplesDir, componentsDir, galleryAssetsDir string) {
		generateComponentSnapshots(name, examplesDir, componentsDir, galleryAssetsDir)
	})
	if err != nil {
		log.Printf("gallery: %v", err)
	}
}

func generateComponentSnapshots(name, examplesDir, componentsDir, galleryAssetsDir string) {
	sourceFile := filepath.Join(examplesDir, name+".sngl")

	// Compile to HTML for interactive iframe preview.
	html, err := snapshot.CompilePreviewHTML(sourceFile, "html", "js")
	if err != nil {
		log.Printf("gallery: %s: compile preview: %v", name, err)
		return
	}
	htmlPath := filepath.Join(componentsDir, name+"-html.html")
	if err := os.WriteFile(htmlPath, html, 0o644); err != nil {
		log.Printf("gallery: %s: write html: %v", name, err)
	}

	// Generate PNG snapshots for other platforms (optional, best-effort).
	for _, platform := range []string{"bubbletea", "fyne"} {
		results, err := snapshot.Generate(snapshot.Config{
			SourceFile: sourceFile,
			Platforms:  []string{platform},
			OutDir:     galleryAssetsDir,
			Width:      800,
			Height:     400,
		})
		if err != nil {
			log.Printf("gallery: %s: snapshot %s: %v (skipping)", name, platform, err)
			continue
		}
		// Rename output to {name}-{platform}.png.
		for _, r := range results {
			target := filepath.Join(galleryAssetsDir, name+"-"+platform+".png")
			if r.Path != target {
				os.Rename(r.Path, target)
			}
		}
	}
}
