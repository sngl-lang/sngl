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
		FS:         os.DirFS("."),
		Dir:        ".",
		IsMain:     true,
		Platforms:  []ir.Platform{&testStubPlatform{}},
		LibSources: testStubDocs(),
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	// RootComponent names `main`: these fixtures are about a lowering pass and
	// not about program shape, so they declare no window and say instead that
	// the harness renders one component -- the same thing `sngl test` says for
	// a component under test. Left unset, there is no root to inline into and
	// every inliner fixture lowered to nothing.
	lowerErr := Lower(pkg, caps, Options{RootComponent: "main"})
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
	gotBytes := []byte(got)

	if *update {
		writeUpdatedExpected(t, path, arc, gotBytes)
		return
	}

	if !reflect.DeepEqual(gotBytes, expected) {
		t.Errorf("lowered output mismatch\n--- want ---\n%s\n--- got ---\n%s", expected, gotBytes)
	}
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

// parseCapsHeader reads a fixture's `caps:` line, which names the passes it
// wants run -- `caps: NoLambda, NoRef`.
//
// It starts from NoLowering rather than the zero value, and that is the whole
// of what the Features/Caps collapse changed here: the header names passes,
// which under the old record was the same thing as naming fields, and under
// the new one is its opposite. A fixture asking for one pass must not get the
// other thirty, so the names withdraw capabilities from a target that claims
// everything. No fixture header changed.
func parseCapsHeader(comment string) (Features, error) {
	c := NoLowering()
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
				return Features{}, err
			}
		}
	}
	return c, nil
}

// setCapByName turns on the pass a fixture named, by withdrawing the
// capability that gates it. The two `StructComponents` spellings are wants and
// so are set rather than withdrawn.
func setCapByName(c *Features, name string) error {
	switch name {
	case "NoToggle":
		c.Toggle = false
	case "NoTernary":
		c.Ternary = false
	case "NoLambda":
		c.Lambda = false
	case "NoRef":
		c.Ref = false
	case "NoUnit":
		c.Unit = false
	case "NoEnum":
		c.Enum = false
	case "NoComputed":
		c.Computed = false
	case "NoContext", "StructComponents":
		c.StructComponents = true
	case "StdlibContextParam":
		c.StdlibContextParam = true
	case "NoReactivity":
		c.Reactivity = false
	case "NoDeclarative":
		c.Declarative = false
	case "NoListLambdas":
		c.ListLambdas = false
	case "NoInlineComponents":
		c.InlineComponents = false
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
// golden fixtures can use `import "sngl:platform/teststub"` to exercise the
// strict-mode branch of passInlinePure. The
// platform exposes two wrapper components — one pure, one impure — and
// nothing else.
type testStubPlatform struct{}

func (testStubPlatform) PlatformIdentifier() string { return "teststub" }
func (testStubPlatform) Description() string        { return "in-test platform stub" }
func (testStubPlatform) Resolve(string) ir.Symbol   { return nil }

const testStubSource = `
import sngl "sngl:ui"

component Cleanwrap(value string) sngl.node {
    sngl.text(value=value)
}

component Statefulwrap() sngl.node {
    var count int = 0
    sngl.text(value=string(count))
}
`

// testStubDocs is the stub's sngl:platform/teststub source. There is no
// lib/platforms/teststub directory, so it reaches the checker through
// Config.LibSources.
func testStubDocs() map[string][]*ast.Document {
	doc, err := parser.Parse("teststub.sngl", []byte(testStubSource))
	if err != nil {
		panic("teststub parse: " + err.Error())
	}
	return map[string][]*ast.Document{"platform/teststub": {doc}}
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
