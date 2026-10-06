package fyne

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

func TestIntegration_ReactiveIfEmitsRenderSlot(t *testing.T) {
	src := `
import . "sngl:ui"
window {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	pkg := checkForFyne(t, src)
	// fyne's two: explicit updaters and a flattened tree. Everything else
	// claimed, so the intrinsic calls under test are the only thing arriving.
	feats := lower.NoLowering()
	feats.Reactivity, feats.Declarative = false, false
	// And fyne's third: a window is a component on fyne, spliced like any
	// other, which is the inliner this platform withholds inlineComponents
	// to ask for.
	feats.InlineComponents = false
	if err := lower.Lower(pkg, feats, lower.Options{Platform: "fyne", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	files := mem.Files()
	modelSrc, ok := files["model.go"]
	if !ok {
		t.Fatal("model.go not found in generated files")
	}
	out := string(modelSrc)

	for _, snippet := range []string{
		"func (m *Model) __renderSlot0(__parent *fyne.Container)",
		"for _, __entry := range m.__slot0",
		"fynelayout.Remove(__parent, __entry)",
		"m.__slot0 = nil",
		"if m.visible",
		// The slot-local label does not escape __renderSlot0 (created,
		// SetText'd, added, and tracked in m.__slot0 all within this scope),
		// so the node-escape lower pass emits it as a function-local
		// `__n1 := ...` rather than a shared Model field. `__n0` is the
		// window's Toplevel.
		"__n1 := widget.NewLabel",
		// `text=value` in the fyne override forwards a `string` prop to a
		// `string` prop, so the setter takes the value as it stands — the
		// blueprint form's `fmt.Sprint` wrapper existed only because the
		// prop it named carried no type.
		`__n1.SetText("hi")`,
		"fynelayout.InsertBefore(__parent, m.__slot0_at, __n1)",
		"m.__slot0 = append(m.__slot0, __n1)",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted Go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

	// Negative assertions: untranslated intrinsic strings must not leak.
	for _, leak := range []string{
		"lower.CreateNode",
		"lower.AppendChild",
		"lower.RemoveChild",
		"stdlib.ListPush",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("untranslated intrinsic %q leaked into emitted Go", leak)
		}
	}

	// Compile-check: write the emitted source under the main module so
	// fyne imports resolve through the project's go.mod/go.sum. Catches
	// __root-style "undefined ident" failures the snippet matcher misses.
	tmp, err := os.MkdirTemp(".", "_fyne-emit-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = tmp
	combined, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Errorf("emitted Go failed to compile: %v\n--- output ---\n%s\n--- source ---\n%s", buildErr, combined, out)
	}
}
