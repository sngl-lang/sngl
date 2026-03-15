package snapshot_test

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/snapshot"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func TestGenerate(t *testing.T) {
	// Find the todo example relative to the module root.
	sourceFile, err := filepath.Abs("../../_examples/todo/todo.sngl.kdl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sourceFile); err != nil {
		t.Skipf("example file not found: %v", err)
	}

	outDir := t.TempDir()

	results, err := snapshot.Generate(snapshot.Config{
		SourceFile: sourceFile,
		Width:      1280,
		Height:     720,
		OutDir:     outDir,
	})
	if err != nil {
		t.Skipf("snapshot generation failed (Chrome unavailable?): %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	for _, r := range results {
		t.Run(r.Platform, func(t *testing.T) {
			info, err := os.Stat(r.Path)
			if err != nil {
				t.Fatalf("output file missing: %v", err)
			}
			if info.Size() == 0 {
				t.Fatal("output PNG is empty")
			}

			f, err := os.Open(r.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			cfg, err := png.DecodeConfig(f)
			if err != nil {
				t.Fatalf("invalid PNG: %v", err)
			}
			if cfg.Width != 1280 || cfg.Height != 720 {
				t.Errorf("unexpected dimensions: %dx%d, want 1280x720", cfg.Width, cfg.Height)
			}
		})
	}
}
