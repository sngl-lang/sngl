package lower

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

var update = flag.Bool("update", false, "rewrite expected.sngl in golden txtar files")

func TestLower(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no testdata/*.txtar files found")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txtar")
		t.Run(name, func(t *testing.T) {
			runGolden(t, file)
		})
	}
}

func runGolden(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	arc := txtar.Parse(raw)

	caps, err := parseCapsHeader(string(arc.Comment))
	if err != nil {
		t.Fatalf("caps header: %v", err)
	}

	var input, expected []byte
	for _, f := range arc.Files {
		switch f.Name {
		case "input.sngl":
			input = f.Data
		case "expected.sngl":
			expected = f.Data
		default:
			t.Fatalf("unexpected file %q in archive (only input.sngl and expected.sngl allowed)", f.Name)
		}
	}
	if input == nil {
		t.Fatal("missing input.sngl section")
	}
	if expected == nil && !*update {
		t.Fatal("missing expected.sngl section (run with -update to seed)")
	}

	doc, err := parser.Parse("input.sngl", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS("."),
		Dir:    ".",
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	if err := Lower(pkg, caps, Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	got := parser.Format(ir.Convert(pkg))
	gotBytes := []byte(got)

	if *update {
		writeUpdatedExpected(t, path, arc, gotBytes)
		return
	}

	if !reflect.DeepEqual(gotBytes, expected) {
		t.Errorf("lowered output mismatch\n--- want ---\n%s\n--- got ---\n%s", expected, gotBytes)
	}
}

func parseCapsHeader(comment string) (Caps, error) {
	var c Caps
	for line := range strings.SplitSeq(comment, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "caps:") {
			continue
		}
		fields := strings.FieldsFunc(strings.TrimPrefix(line, "caps:"), func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		for _, name := range fields {
			if err := setCapByName(&c, name); err != nil {
				return Caps{}, err
			}
		}
	}
	return c, nil
}

func setCapByName(c *Caps, name string) error {
	switch name {
	case "NoToggle":
		c.NoToggle = true
	case "NoTernary":
		c.NoTernary = true
	case "NoLambda":
		c.NoLambda = true
	case "NoRef":
		c.NoRef = true
	case "NoUnit":
		c.NoUnit = true
	case "NoEnum":
		c.NoEnum = true
	case "NoComputed":
		c.NoComputed = true
	case "NoTimer":
		c.NoTimer = true
	case "NoReactivity":
		c.NoReactivity = true
	case "NoDeclarative":
		c.NoDeclarative = true
	default:
		return errCapsName(name)
	}
	return nil
}

type errCapsName string

func (e errCapsName) Error() string {
	return "unknown caps flag " + string(e) + " (valid: " + strings.Join(PassNames(), ", ") + ")"
}

func writeUpdatedExpected(t *testing.T, path string, arc *txtar.Archive, got []byte) {
	t.Helper()
	for i := range arc.Files {
		if arc.Files[i].Name == "expected.sngl" {
			arc.Files[i].Data = got
			goto out
		}
	}
	arc.Files = append(arc.Files, txtar.File{Name: "expected.sngl", Data: got})
out:
	if err := os.WriteFile(path, txtar.Format(arc), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}
