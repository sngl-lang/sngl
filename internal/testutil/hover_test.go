package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHoverDirectives_BareWord(t *testing.T) {
	src := `// HOVER(add) "func add"
func add(a int) int => a + 1
`
	path := writeTemp(t, "bareword.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1", len(dirs))
	}
	d := dirs[0]
	if d.Target != "add" {
		t.Errorf("target = %q, want %q", d.Target, "add")
	}
	if d.Substring != "func add" {
		t.Errorf("substring = %q, want %q", d.Substring, "func add")
	}
	if d.Negate {
		t.Error("Negate true, want false")
	}
	// Position: "add" appears first at line 2, col 6 (1-based)
	if d.Line != 2 || d.Col != 6 {
		t.Errorf("position = %d:%d, want 2:6", d.Line, d.Col)
	}
}

func TestParseHoverDirectives_HexLiteral(t *testing.T) {
	src := `// HOVER(#ff0000) "rgb(255, 0, 0)"
component App node { var c = #ff0000 }
`
	path := writeTemp(t, "color.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d", len(dirs))
	}
	d := dirs[0]
	if d.Target != "#ff0000" || d.Substring != "rgb(255, 0, 0)" {
		t.Errorf("got %+v", d)
	}
	if d.Line != 2 {
		t.Errorf("line = %d, want 2", d.Line)
	}
}

func TestParseHoverDirectives_ExplicitPosition(t *testing.T) {
	src := `// HOVER(@3:7) "something"
//
component Foo node {}
`
	path := writeTemp(t, "explicit.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatal("expected 1 dir")
	}
	if dirs[0].Line != 3 || dirs[0].Col != 7 {
		t.Errorf("position = %d:%d, want 3:7", dirs[0].Line, dirs[0].Col)
	}
	if dirs[0].Target != "@3:7" {
		t.Errorf("target = %q", dirs[0].Target)
	}
}

func TestParseHoverDirectives_Negate(t *testing.T) {
	src := `// HOVER-NOT(add) "broken"
func add() {}
`
	path := writeTemp(t, "neg.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || !dirs[0].Negate {
		t.Fatalf("got %+v", dirs)
	}
}

func TestParseHoverDirectives_TargetNotFound(t *testing.T) {
	src := `// HOVER(missing) "x"
func add() {}
`
	path := writeTemp(t, "missing.sngl", src)
	_, err := ParseHoverDirectives(path)
	if err == nil {
		t.Fatal("expected error for unfindable target")
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
