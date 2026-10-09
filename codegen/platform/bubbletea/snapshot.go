//go:build !js

package bubbletea

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
	"github.com/charmbracelet/x/cellbuf"
)

type htmlSnapshotter interface {
	SnapshotHTML(html []byte, width, height int) ([]byte, error)
}

// snapshotGoMod builds the go.mod for a snapshot harness module. It mirrors the
// go.buildModule path: codegen.DetectHostGoMod supplies the host Go version and
// — critically — a `replace duckfam.us/sngl => <repo>` directive
// when run from inside the sngl source tree. Without that replace the harness
// resolves pkg/go/tui to the *published* module, so any tui helper added in an
// unpublished commit (e.g. WidgetWidth) fails `go mod tidy`, the harness never
// builds, and the snapshot comes out blank/stale.
func snapshotGoMod() string {
	goVersion, goModExtra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
	if goModExtra != "" {
		mod += "\n" + goModExtra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	return mod
}

func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.runSnapshot(pkg, lang, width, height)
	if err != nil {
		return nil, err
	}

	cols := width / 10
	rows := height / 20
	html := viewToHTML(view, cols, rows)

	htmlPlat := codegen.LookupPlatform("html")
	hs, ok := htmlPlat.(htmlSnapshotter)
	if !ok {
		return nil, fmt.Errorf("html platform does not implement SnapshotHTML")
	}
	return hs.SnapshotHTML(html, width, height)
}

// SnapshotText renders the TUI output as ANSI text.
func (g *Generator) SnapshotText(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.runSnapshot(pkg, lang, width, height)
	if err != nil {
		return nil, err
	}
	return []byte(view), nil
}

// runSnapshot generates real BubbleTea code, builds it with a snapshot
// harness, and captures the initial View() output as an ANSI string.
func (g *Generator) runSnapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go not found in PATH")
	}

	sink := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{
		Pkg:  pkg,
		Lang: lang,
		Options: codegen.OptionsFromMap(map[string]any{
			"package": "main",
			"main":    false,
		}),
	}, sink); err != nil {
		return "", fmt.Errorf("generating bubbletea code: %w", err)
	}
	files := sink.Files()

	cols := width / 10
	rows := height / 20
	harness := fmt.Sprintf(`package main

import "fmt"

func main() {
	m := New()
	m.SetTerminalSize(%d, %d)
	fmt.Print(m.View().Content)
}
`, cols, rows)

	files["snapshot_main.go"] = []byte(harness)
	files["go.mod"] = []byte(snapshotGoMod())

	// Keyed by the generated content, so an unchanged snapshot reuses the
	// directory and with it the Go build cache.
	tmpDir, release, err := codegen.BuildDirForContent("snapshot-bubbletea", files)
	if err != nil {
		return "", fmt.Errorf("creating build dir: %w", err)
	}
	defer release()

	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return "", fmt.Errorf("go mod tidy: %w", err)
	}

	run := exec.Command(goPath, "run", ".")
	run.Dir = tmpDir
	run.Env = append(os.Environ(), "COLORTERM=truecolor", "TERM=xterm-256color")
	var stdout bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = os.Stderr
	if err := run.Run(); err != nil {
		return "", fmt.Errorf("running snapshot: %w", err)
	}

	return stdout.String(), nil
}

// BatchSnapshot generates code for multiple documents into sub-packages of a
// single Go module, compiles once, then runs the binary per document to capture
// each screenshot. This amortises go mod tidy + compilation across all docs.
func (g *Generator) BatchSnapshot(docs []codegen.BatchDoc, width, height int) (map[string][]byte, error) {
	views, err := g.runBatchSnapshot(docs, width, height)
	if err != nil {
		return nil, err
	}

	cols := width / 10
	rows := height / 20

	htmlPlat := codegen.LookupPlatform("html")
	hs, ok := htmlPlat.(htmlSnapshotter)
	if !ok {
		return nil, fmt.Errorf("html platform does not implement SnapshotHTML")
	}

	results := make(map[string][]byte, len(views))
	for id, view := range views {
		html := viewToHTML(view, cols, rows)
		png, err := hs.SnapshotHTML(html, width, height)
		if err != nil {
			return nil, fmt.Errorf("screenshot %s: %w", id, err)
		}
		results[id] = png
	}
	return results, nil
}

// BatchSnapshotText generates code for multiple documents into sub-packages,
// compiles once, then runs the binary per document to capture ANSI text.
func (g *Generator) BatchSnapshotText(docs []codegen.BatchDoc, width, height int) (map[string][]byte, error) {
	views, err := g.runBatchSnapshot(docs, width, height)
	if err != nil {
		return nil, err
	}
	results := make(map[string][]byte, len(views))
	for id, view := range views {
		results[id] = []byte(view)
	}
	return results, nil
}

// runBatchSnapshot generates all docs into sub-packages, builds once, then
// runs the binary per doc to capture View() output as ANSI strings.
func (g *Generator) runBatchSnapshot(docs []codegen.BatchDoc, width, height int) (map[string]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if len(docs) == 1 {
		view, err := g.runSnapshot(docs[0].Pkg, docs[0].Lang, width, height)
		if err != nil {
			return nil, err
		}
		return map[string]string{docs[0].ID: view}, nil
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go not found in PATH")
	}

	tmpDir, release, err := codegen.BuildDir("snapshot-bubbletea-batch", codegen.BatchKey(docs)...)
	if err != nil {
		return nil, fmt.Errorf("creating build dir: %w", err)
	}
	defer release()

	type docPkg struct {
		id      string
		pkgName string
	}
	var pkgs []docPkg

	for i, d := range docs {
		pkgName := fmt.Sprintf("doc%d", i)
		pkgDir := filepath.Join(tmpDir, pkgName)

		if err := g.Generate(&codegen.Request{
			Pkg:  d.Pkg,
			Lang: d.Lang,
			Options: codegen.OptionsFromMap(map[string]any{
				"package": pkgName,
				"main":    false,
			}),
		}, codegen.NewDirSink(pkgDir)); err != nil {
			return nil, fmt.Errorf("generating bubbletea code for %s: %w", d.ID, err)
		}

		pkgs = append(pkgs, docPkg{id: d.ID, pkgName: pkgName})
	}

	cols := width / 10
	rows := height / 20

	// Build dispatch harness.
	var imports, cases strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&imports, "\t\"tmp/%s\"\n", p.pkgName)
		fmt.Fprintf(&cases, "\tcase %q:\n\t\tm := %s.New()\n\t\tm.SetTerminalSize(%d, %d)\n\t\tview = m.View().Content\n", p.pkgName, p.pkgName, cols, rows)
	}

	harness := fmt.Sprintf(`package main

import (
	"fmt"
	"os"

%s)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <pkg>")
		os.Exit(1)
	}
	var view string
	switch os.Args[1] {
%s	default:
		fmt.Fprintf(os.Stderr, "unknown doc: %%s\n", os.Args[1])
		os.Exit(1)
	}
	fmt.Print(view)
}
`, imports.String(), cases.String())

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(harness), 0o644); err != nil {
		return nil, fmt.Errorf("writing harness: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(snapshotGoMod()), 0o644); err != nil {
		return nil, fmt.Errorf("writing go.mod: %w", err)
	}

	// Single go mod tidy + build.
	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w", err)
	}

	// Beside the directory, not in it: the directory is emptied before each
	// generation, and `go build -o` relinks whenever its output is missing.
	binPath := tmpDir + ".snapshot"
	build := exec.Command(goPath, "build", "-o", binPath, ".")
	build.Dir = tmpDir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("go build: %w", err)
	}

	// Run once per doc.
	results := make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		var stdout bytes.Buffer
		run := exec.Command(binPath, p.pkgName)
		run.Dir = tmpDir
		run.Env = append(os.Environ(), "COLORTERM=truecolor", "TERM=xterm-256color")
		run.Stdout = &stdout
		run.Stderr = os.Stderr
		if err := run.Run(); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", p.id, err)
		}
		results[p.id] = stdout.String()
	}

	return results, nil
}

// viewToHTML converts ANSI-styled terminal text to an HTML page for screenshotting.
func viewToHTML(view string, cols, rows int) []byte {
	buf := cellbuf.NewBuffer(cols, rows)
	cellbuf.SetContent(buf, view)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html><head><style>
body { margin: 0; padding: 0; background: #1e1e2e; color: #cdd6f4; }
pre { margin: 0; padding: 0; font-family: monospace; font-size: 14px; line-height: 1.4; }
</style></head><body><pre>`)

	for y := 0; y < buf.Height(); y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		var lastStyle string
		spanOpen := false
		for x := 0; x < buf.Width(); x++ {
			cell := buf.Cell(x, y)
			css := cellCSS(cell)
			if css != lastStyle {
				if spanOpen {
					b.WriteString("</span>")
				}
				if css != "" {
					fmt.Fprintf(&b, `<span style="%s">`, css)
					spanOpen = true
				} else {
					spanOpen = false
				}
				lastStyle = css
			}
			if cell == nil || cell.Rune == 0 {
				b.WriteByte(' ')
			} else {
				switch cell.Rune {
				case '<':
					b.WriteString("&lt;")
				case '>':
					b.WriteString("&gt;")
				case '&':
					b.WriteString("&amp;")
				default:
					b.WriteRune(cell.Rune)
				}
			}
		}
		if spanOpen {
			b.WriteString("</span>")
		}
	}

	b.WriteString("</pre></body></html>")
	return []byte(b.String())
}

func cellCSS(cell *cellbuf.Cell) string {
	if cell == nil {
		return ""
	}
	var parts []string

	if cell.Style.Fg != nil {
		r, g, b, _ := cell.Style.Fg.RGBA()
		parts = append(parts, fmt.Sprintf("color:rgb(%d,%d,%d)", r>>8, g>>8, b>>8))
	}
	if cell.Style.Bg != nil {
		r, g, b, _ := cell.Style.Bg.RGBA()
		parts = append(parts, fmt.Sprintf("background:rgb(%d,%d,%d)", r>>8, g>>8, b>>8))
	}
	if cell.Style.Attrs&cellbuf.BoldAttr != 0 {
		parts = append(parts, "font-weight:bold")
	}
	if cell.Style.Attrs&cellbuf.FaintAttr != 0 {
		parts = append(parts, "opacity:0.5")
	}
	if cell.Style.Attrs&cellbuf.ItalicAttr != 0 {
		parts = append(parts, "font-style:italic")
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";")
}
