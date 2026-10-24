package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeTokens renders delta-encoded tokens as "line:col text type mods".
func decodeTokens(src string, data []uint32) []string {
	lines := strings.Split(src, "\n")
	var out []string
	line, col := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		if data[i] > 0 {
			col = 0
		}
		line += int(data[i])
		col += int(data[i+1])
		text := string([]rune(lines[line])[col : col+int(data[i+2])])
		var mods []string
		for b, m := range semanticModifiers {
			if data[i+4]&(1<<b) != 0 {
				mods = append(mods, m)
			}
		}
		out = append(out, fmt.Sprintf("%d:%d %s %s %s", line+1, col+1, text, semanticLegend[data[i+3]], strings.Join(mods, ",")))
	}
	return out
}

func TestSemanticTokensClassifyByDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.sngl")
	src := `import ui "sngl:ui"

const limit = 3

component Counter(label string) ui.node {
    var count = 0
    func bump() {
        count += limit
    }

    ui.button(text=label, @click { bump() })
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New()
	fs := srv.ws.open(pathToURI(path), src, 1)
	if diags := srv.analyze(fs); len(diags) != 0 {
		t.Fatalf("fixture does not check: %+v", diags)
	}
	got := decodeTokens(src, semanticTokens(src, fs.Index, path))
	want := []string{
		"1:8 ui namespace declaration",
		"3:7 limit variable declaration,readonly",
		"5:11 Counter component declaration",
		"5:19 label property declaration",
		"5:25 string type defaultLibrary",
		"5:33 ui namespace ",
		"5:36 node component defaultLibrary",
		"6:9 count variable declaration",
		"7:10 bump method declaration",
		"8:9 count variable ",
		"8:18 limit variable readonly",
		"11:5 ui namespace ",
		"11:8 button component defaultLibrary",
		"11:15 text property defaultLibrary",
		"11:20 label property ",
		"11:28 click event defaultLibrary",
		"11:36 bump method ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tokens:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A name past a character outside the Basic Multilingual Plane is two UTF-16
// units further along than its rune column.
func TestSemanticTokensCountUTF16(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.sngl")
	src := "const smile = \"😀\"\nconst both = \"😀\" + smile\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New()
	fs := srv.ws.open(pathToURI(path), src, 1)
	srv.analyze(fs)
	data := semanticTokens(src, fs.Index, path)
	// The second token is both, at the start of line 2's names; the third,
	// the read of smile after the emoji, is at rune column 19 and UTF-16
	// column 20.
	if len(data) < 15 || data[5] != 1 || data[10] != 0 || data[6]+data[11] != 20 {
		t.Errorf("tokens = %v, want smile on line 2 at UTF-16 column 20", data)
	}
}
