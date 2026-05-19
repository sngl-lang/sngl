package testutil

import (
	"testing"
)

func TestParseInlayDirectives_BareLiteral(t *testing.T) {
	src := `// INLAY(12em) "(192px)"
var x = 12em
`
	path := writeTemp(t, "inlay.sngl", src)
	dirs, err := ParseInlayDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d, want 1", len(dirs))
	}
	d := dirs[0]
	if d.Target != "12em" || d.Label != "(192px)" {
		t.Errorf("got %+v", d)
	}
	// "12em" appears on line 2 starting at col 9 (1-based)
	if d.Line != 2 || d.Col != 9 {
		t.Errorf("position = %d:%d, want 2:9", d.Line, d.Col)
	}
	// Length of target — used by harness to compute hint anchor (right after)
	if d.TargetLen != 4 {
		t.Errorf("target len = %d, want 4", d.TargetLen)
	}
	if d.Negate {
		t.Error("Negate true, want false")
	}
}

func TestParseInlayDirectives_ExplicitPosition(t *testing.T) {
	src := `// INLAY(@3:5) "(16px)"
//
    var y = 1em
`
	path := writeTemp(t, "explicit.sngl", src)
	dirs, err := ParseInlayDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatal("expected 1 dir")
	}
	if dirs[0].Line != 3 || dirs[0].Col != 5 {
		t.Errorf("position = %d:%d, want 3:5", dirs[0].Line, dirs[0].Col)
	}
	if dirs[0].TargetLen != 0 {
		t.Errorf("explicit position should set TargetLen to 0, got %d", dirs[0].TargetLen)
	}
}

func TestParseInlayDirectives_TargetNotFound(t *testing.T) {
	src := `// INLAY(missing) "(99px)"
var x = 12em
`
	path := writeTemp(t, "missing.sngl", src)
	_, err := ParseInlayDirectives(path)
	if err == nil {
		t.Fatal("expected error for unfindable target")
	}
}

func TestParseInlayDirectives_Negate(t *testing.T) {
	src := `// INLAY-NOT(8px) ""
var x = 8px
`
	path := writeTemp(t, "neg.sngl", src)
	dirs, err := ParseInlayDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || !dirs[0].Negate {
		t.Fatalf("got %+v", dirs)
	}
}
