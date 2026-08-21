package bubbletea

import (
	goparser "go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

func hasErrors(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func firstError(diags []ir.Diagnostic) string {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return d.Error()
		}
	}
	return ""
}

func compileAndVerify(t *testing.T, doc *ast.Document, pkg ...*ir.Package) []byte {
	t.Helper()
	var p *ir.Package
	if len(pkg) > 0 {
		p = pkg[0]
	}
	if p != nil {
		if err := optimize.Optimize(p, &optimize.Config{Platform: "bubbletea", Language: "go"}); err != nil {
			t.Fatalf("optimize: %v", err)
		}
		gen := &Generator{}
		goLang := codegen.LookupLang("go")
		if err := lower.Lower(p, gen.Capabilities(goLang).ToLowerCaps(), lower.Options{Platform: "bubbletea"}); err != nil {
			t.Fatalf("lower: %v", err)
		}
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: p}, "bubbletea")
	src, err := CompileIR(ctx, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify the generated code parses as valid Go
	fset := token.NewFileSet()
	_, err = goparser.ParseFile(fset, "generated.go", src, goparser.AllErrors)
	if err != nil {
		t.Fatalf("generated code is not valid Go:\n%s\nerror: %v", src, err)
	}
	return src
}

func TestFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true})
			if hasErrors(diags) {
				t.Fatalf("check: %s", firstError(diags))
			}
			compileAndVerify(t, doc, pkg)
		})
	}
}

func TestGettersSetters(t *testing.T) {
	src := `import . "sngl://std"
struct Todo {
    text string
    done bool
}

component main {
    var (
        count = 0
        todos list<Todo>
    )
}
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	out := compileAndVerify(t, doc, pkg)
	code := string(out)

	checks := map[string]string{
		"getter":       "func (m Model) Todos() []Todo",
		"setter":       "func (m Model) SetTodos(v []Todo) Model",
		"msg type":     "type setTodosMsg struct",
		"cmd func":     "func SetTodosCmd(",
		"update case":  "case setTodosMsg:",
		"count getter": "func (m Model) Count() int",
		"count setter": "func (m Model) SetCount(v int) Model",
	}
	for name, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %s: %q\n\ngenerated:\n%s", name, check, code)
		}
	}
}

// TestOverlayFocusCapture verifies Phase H2: while a modal/drawer overlay is
// open, the background is frozen (Tab nav + activation cases gated on
// !overlayOpen, widget forwarding wrapped in `if !overlayOpen`), Escape closes
// the topmost open overlay (setting its open var false), and overlay-content
// activation handlers stay unguarded.
func TestOverlayFocusCapture(t *testing.T) {
	src := `import . "sngl://std"
component main {
    var (
        showModal = false
        showDrawer = false
        count = 0
    )
    button(text="Open", @click { showModal = true })
    modal(open=showModal, closable=true) {
        button(text="Inc", @click { count += 1 })
    }
    drawer(open=showDrawer, side="right") {
        button(text="Close", @click { showDrawer = false })
    }
}
`
	code := compileBubbletea(t, src)

	mustContain := map[string]string{
		"overlayOpen gate":                     "overlayOpen := m.showModal || m.showDrawer",
		"escape case":                          "case msg.Code == tea.KeyEsc:",
		"escape closes drawer (topmost first)": "case m.showDrawer:\n\t\t\t\tm.showDrawer = false",
		"escape closes modal":                  "case m.showModal:\n\t\t\t\tm.showModal = false",
		"tab nav frozen":                       "msg.Code == tea.KeyTab && msg.Mod == 0 && !overlayOpen:",
		"forwarding frozen":                    "if !overlayOpen {",
	}
	for name, want := range mustContain {
		if !strings.Contains(code, want) {
			t.Errorf("missing %s: %q\n\ngenerated:\n%s", name, want, code)
		}
	}

	// The background "Open" button (focusID 0) must be frozen.
	if !strings.Contains(code, "m.__focusID == 0 && !overlayOpen") {
		t.Errorf("background button not frozen\n\ngenerated:\n%s", code)
	}
	// Overlay-content handlers stay live: the modal's `count += 1` button and the
	// drawer's `showDrawer = false` button each follow an unguarded activation
	// case (focusID 1 and 2 in source order).
	for _, want := range []string{
		"case msg.Code == tea.KeyEnter && m.__focusID == 1:\n\t\t\tm.count += 1",
		"case msg.Code == tea.KeyEnter && m.__focusID == 2:\n\t\t\tm.showDrawer = false",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("overlay-content handler missing or wrongly frozen: %q\n\ngenerated:\n%s", want, code)
		}
	}
}

func TestCompileTodo(t *testing.T) {
	doc, err := testutil.ParseFile("../../../examples/todo/todo.sngl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{FS: os.DirFS("../../../examples/todo"), IsMain: true})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	compileAndVerify(t, doc, pkg)
}
