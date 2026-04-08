package fyne

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Snapshot generates real Fyne code, builds it with a snapshot harness that
// renders to a headless test window, and returns the captured PNG.
func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go not found in PATH")
	}

	tmpDir, err := os.MkdirTemp("", "sngl-fyne-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	resp, err := g.Generate(&codegen.Request{
		Doc:  doc,
		Lang: lang,
		Options: map[string]string{
			"package": "main",
			"main":    "false",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("generating fyne code: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("generating fyne code: %s", resp.Error)
	}

	for _, file := range resp.Files {
		path := filepath.Join(tmpDir, file.Name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		f, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("creating %s: %w", file.Name, err)
		}
		_, writeErr := file.WriteTo(f)
		f.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return nil, fmt.Errorf("writing %s: %w", file.Name, writeErr)
		}
	}

	harness := fmt.Sprintf(`package main

import (
	"image/png"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func main() {
	a := test.NewApp()
	w := a.NewWindow("Snapshot")
	m := New()
	w.SetContent(m.BuildUI())
	size := fyne.NewSize(%d, %d)
	w.Resize(size)
	w.Canvas().Content().Resize(size)
	w.Resize(size)
	png.Encode(os.Stdout, w.Canvas().Capture())
	a.Quit()
}
`, width, height)

	if err := os.WriteFile(filepath.Join(tmpDir, "snapshot_main.go"), []byte(harness), 0o644); err != nil {
		return nil, fmt.Errorf("writing harness: %w", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module tmp\n\ngo 1.23\n"), 0o644); err != nil {
		return nil, fmt.Errorf("writing go.mod: %w", err)
	}

	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w", err)
	}

	run := exec.Command(goPath, "run", ".")
	run.Dir = tmpDir
	var stdout bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = os.Stderr
	if err := run.Run(); err != nil {
		return nil, fmt.Errorf("running snapshot: %w", err)
	}

	return stdout.Bytes(), nil
}
