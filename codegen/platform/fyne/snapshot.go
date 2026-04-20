package fyne

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Snapshot generates real Fyne code, builds it with a snapshot harness that
// renders to a headless test window, and returns the captured PNG.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
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
		Pkg:  pkg,
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

// BatchSnapshot generates code for multiple documents into sub-packages of a
// single Go module, compiles once, then runs the binary per document to capture
// each screenshot. This amortises go mod tidy + compilation across all docs.
func (g *Generator) BatchSnapshot(docs []codegen.BatchDoc, width, height int) (map[string][]byte, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if len(docs) == 1 {
		png, err := g.Snapshot(docs[0].Pkg, docs[0].Lang, width, height)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{docs[0].ID: png}, nil
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go not found in PATH")
	}

	tmpDir, err := os.MkdirTemp("", "sngl-fyne-batch-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate each doc into its own sub-package.
	type docPkg struct {
		id      string
		pkgName string
	}
	var pkgs []docPkg

	for i, d := range docs {
		pkgName := fmt.Sprintf("doc%d", i)
		pkgDir := filepath.Join(tmpDir, pkgName)

		resp, err := g.Generate(&codegen.Request{
			Pkg:  d.Pkg,
			Lang: d.Lang,
			Options: map[string]string{
				"package": pkgName,
				"main":    "false",
			},
		})
		if err != nil {
			return nil, fmt.Errorf("generating fyne code for %s: %w", d.ID, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("generating fyne code for %s: %s", d.ID, resp.Error)
		}

		for _, file := range resp.Files {
			path := filepath.Join(pkgDir, file.Name)
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

		pkgs = append(pkgs, docPkg{id: d.ID, pkgName: pkgName})
	}

	// Build dispatch harness.
	var imports, cases strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&imports, "\t\"tmp/%s\"\n", p.pkgName)
		fmt.Fprintf(&cases, "\tcase %q:\n\t\tm := %s.New()\n\t\tcontent = m.BuildUI()\n", p.pkgName, p.pkgName)
	}

	harness := fmt.Sprintf(`package main

import (
	"fmt"
	"image/png"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
%s)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <pkg>")
		os.Exit(1)
	}
	a := test.NewApp()
	w := a.NewWindow("Snapshot")
	var content fyne.CanvasObject
	switch os.Args[1] {
%s	default:
		fmt.Fprintf(os.Stderr, "unknown doc: %%s\n", os.Args[1])
		os.Exit(1)
	}
	w.SetContent(content)
	size := fyne.NewSize(%d, %d)
	w.Resize(size)
	w.Canvas().Content().Resize(size)
	w.Resize(size)
	png.Encode(os.Stdout, w.Canvas().Capture())
	a.Quit()
}
`, imports.String(), cases.String(), width, height)

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(harness), 0o644); err != nil {
		return nil, fmt.Errorf("writing harness: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module tmp\n\ngo 1.23\n"), 0o644); err != nil {
		return nil, fmt.Errorf("writing go.mod: %w", err)
	}

	// Single go mod tidy + build.
	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w", err)
	}

	binPath := filepath.Join(tmpDir, "snapshot")
	build := exec.Command(goPath, "build", "-o", binPath, ".")
	build.Dir = tmpDir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("go build: %w", err)
	}

	// Run once per doc.
	results := make(map[string][]byte, len(pkgs))
	for _, p := range pkgs {
		var stdout bytes.Buffer
		run := exec.Command(binPath, p.pkgName)
		run.Dir = tmpDir
		run.Stdout = &stdout
		run.Stderr = os.Stderr
		if err := run.Run(); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", p.id, err)
		}
		results[p.id] = stdout.Bytes()
	}

	return results, nil
}
