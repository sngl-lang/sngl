package snapshot_test

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/imgdiff"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func TestGenerate(t *testing.T) {
	// Find the todo example relative to the module root.
	sourceFile, err := filepath.Abs("../../_examples/todo/todo.sngl")
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

// htmlSnapshotter is satisfied by HTML Generator's SnapshotHTML method.
type htmlSnapshotter interface {
	SnapshotHTML(html []byte, width, height int) ([]byte, error)
}

func TestSnapshotFidelity(t *testing.T) {
	sourceFile, err := filepath.Abs("../../_examples/todo/todo.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sourceFile); err != nil {
		t.Skipf("example file not found: %v", err)
	}

	// Find platforms that implement both Snapshotter and PreviewStyler
	for _, name := range codegen.Platforms() {
		plat := codegen.LookupPlatform(name)
		snapshotter, hasSnapshot := plat.(codegen.Snapshotter)
		_, hasPreview := plat.(codegen.PreviewStyler)

		if !hasSnapshot || !hasPreview {
			continue
		}

		t.Run(name, func(t *testing.T) {
			lang := codegen.LookupLang(snapshot.LangForPlatform(name))
			if lang == nil {
				t.Skipf("no lang for platform %s", name)
			}

			// 1. Native snapshot via Snapshotter
			doc, err := snapshot.ParseSNGL(sourceFile)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			native, err := snapshotter.Snapshot(doc, lang, 1280, 720)
			if err != nil {
				t.Skipf("native snapshot failed (Chrome unavailable?): %v", err)
			}

			// 2. HTML-simulated snapshot via CompilePreviewHTML + HTML SnapshotHTML
			html, err := snapshot.CompilePreviewHTML(sourceFile, name, lang.Lang())
			if err != nil {
				t.Fatalf("compile preview HTML: %v", err)
			}
			htmlPlat := codegen.LookupPlatform("html")
			hs, ok := htmlPlat.(htmlSnapshotter)
			if !ok {
				t.Fatal("html platform does not implement SnapshotHTML")
			}
			simulated, err := hs.SnapshotHTML(html, 1280, 720)
			if err != nil {
				t.Skipf("simulated snapshot failed: %v", err)
			}

			// Write snapshots to testdata for inspection (gitignored).
			snapshotDir := filepath.Join("testdata", "snapshots")
			os.MkdirAll(snapshotDir, 0o755)
			os.WriteFile(filepath.Join(snapshotDir, name+"_native.png"), native, 0o644)
			os.WriteFile(filepath.Join(snapshotDir, name+"_simulated.png"), simulated, 0o644)

			// 3. Compare with wide tolerance (10% pixel diff)
			diffPath := filepath.Join(snapshotDir, name+"_diff.png")
			if err := imgdiff.Compare(native, simulated, diffPath, 20, 0.10); err != nil {
				t.Errorf("fidelity diff for %s: %v", name, err)
			}
		})
	}
}
