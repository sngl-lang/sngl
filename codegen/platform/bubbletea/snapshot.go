//go:build !js

package bubbletea

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"github.com/charmbracelet/x/cellbuf"
)

type htmlSnapshotter interface {
	SnapshotHTML(html []byte, width, height int) ([]byte, error)
}

func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.runSnapshot(doc, lang, width, height)
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
func (g *Generator) SnapshotText(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.runSnapshot(doc, lang, width, height)
	if err != nil {
		return nil, err
	}
	return []byte(view), nil
}

// runSnapshot generates real BubbleTea code, builds it with a snapshot
// harness, and captures the initial View() output as an ANSI string.
func (g *Generator) runSnapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go not found in PATH")
	}

	tmpDir, err := os.MkdirTemp("", "sngl-bubbletea-snapshot-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
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
		return "", fmt.Errorf("generating bubbletea code: %w", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("generating bubbletea code: %s", resp.Error)
	}

	for _, file := range resp.Files {
		path := filepath.Join(tmpDir, file.Name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		f, err := os.Create(path)
		if err != nil {
			return "", fmt.Errorf("creating %s: %w", file.Name, err)
		}
		_, writeErr := file.WriteTo(f)
		f.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return "", fmt.Errorf("writing %s: %w", file.Name, writeErr)
		}
	}

	cols := width / 10
	rows := height / 20
	harness := fmt.Sprintf(`package main

import "fmt"

func main() {
	m := New()
	m.width = %d
	m.height = %d
	fmt.Print(m.View().Content)
}
`, cols, rows)

	if err := os.WriteFile(filepath.Join(tmpDir, "snapshot_main.go"), []byte(harness), 0o644); err != nil {
		return "", fmt.Errorf("writing harness: %w", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module tmp\n\ngo 1.23\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing go.mod: %w", err)
	}

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
