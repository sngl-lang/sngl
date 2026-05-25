package lower

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/ast"
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
	wantErr := parseExpectedErrorHeader(string(arc.Comment))

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
	if expected == nil && !*update && wantErr == "" {
		t.Fatal("missing expected.sngl section (run with -update to seed)")
	}

	doc, err := parser.Parse("input.sngl", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        os.DirFS("."),
		Dir:       ".",
		IsMain:    true,
		Platforms: []ir.Platform{&testStubPlatform{}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	lowerErr := Lower(pkg, caps, Options{})
	if wantErr != "" {
		if lowerErr == nil {
			t.Fatalf("expected lower error containing %q; got nil", wantErr)
		}
		if !strings.Contains(lowerErr.Error(), wantErr) {
			t.Fatalf("expected lower error containing %q; got: %v", wantErr, lowerErr)
		}
		return
	}
	if lowerErr != nil {
		t.Fatalf("lower: %v", lowerErr)
	}

	got := parser.Format(ir.Convert(pkg))
	got = stripAutoImports(got)
	gotBytes := []byte(got)

	if *update {
		writeUpdatedExpected(t, path, arc, gotBytes)
		return
	}

	if !reflect.DeepEqual(gotBytes, expected) {
		t.Errorf("lowered output mismatch\n--- want ---\n%s\n--- got ---\n%s", expected, gotBytes)
	}
}

// stripAutoImports removes leading auto-injected stdlib imports from the
// formatted output. The lower-pass goldens never reference these imports
// in their inputs and never need them in their expected outputs; they're
// noise from the checker's auto-import behavior. Removing them here keeps
// fixtures focused on the actual lowered output.
func stripAutoImports(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	skipping := true
	for _, line := range lines {
		if skipping {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "import ") &&
				(strings.Contains(trimmed, `"internal://stdlib"`) ||
					strings.Contains(trimmed, `"internal://alert"`) ||
					strings.Contains(trimmed, `"internal://file"`) ||
					strings.Contains(trimmed, `"internal://intl"`) ||
					strings.Contains(trimmed, `"internal://lower"`)) {
				continue
			}
			// First non-import, non-blank line ends the skip phase.
			skipping = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// parseExpectedErrorHeader reads an "expected_error:" line from the txtar
// comment and returns the trimmed substring the test asserts is contained
// in the lower error. Empty string means no error is expected.
func parseExpectedErrorHeader(comment string) string {
	for line := range strings.SplitSeq(comment, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "expected_error:") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "expected_error:"))
	}
	return ""
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
	case "NoContext", "StructComponents":
		c.StructComponents = true
	case "StdlibContextParam":
		c.StdlibContextParam = true
	case "NoReactivity":
		c.NoReactivity = true
	case "NoDeclarative":
		c.NoDeclarative = true
	case "NoStdlibWrappers":
		c.NoStdlibWrappers = true
	case "NoListLambdas":
		c.NoListLambdas = true
	case "NoInlineComponents":
		c.NoInlineComponents = true
	default:
		return errCapsName(name)
	}
	return nil
}

type errCapsName string

func (e errCapsName) Error() string {
	return "unknown caps flag " + string(e) + " (valid: " + strings.Join(PassNames(), ", ") + ")"
}

// testStubPlatform is a minimal in-test ir.Platform registration so
// golden fixtures can use `import "platform://teststub"` to exercise the
// strict-mode (Caps.NoStdlibWrappers) branch of passInlinePure. The
// platform exposes two wrapper components — one pure, one impure — and
// nothing else.
type testStubPlatform struct{}

func (testStubPlatform) PlatformIdentifier() string { return "teststub" }
func (testStubPlatform) Description() string        { return "in-test platform stub" }
func (testStubPlatform) Resolve(string) ir.Symbol   { return nil }

const testStubSource = `
component Cleanwrap(value string) {
    text(value=value)
}

component Statefulwrap() {
    var count int = 0
    text(value=string(count))
}
`

func (testStubPlatform) Package() []*ast.Document {
	doc, err := parser.Parse("teststub.sngl", []byte(testStubSource))
	if err != nil {
		panic("teststub parse: " + err.Error())
	}
	return []*ast.Document{doc}
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
