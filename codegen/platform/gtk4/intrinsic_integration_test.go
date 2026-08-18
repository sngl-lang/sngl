package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIntegration_ReactiveIfEmitsRenderSlot(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4"}); err != nil {
		t.Fatalf("lower: %v", err)
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

	// This program uses only the bounded stdlib widget surface (button/text/if),
	// so gtk4 codegen emits it in wrapped mode: pkg/go/gtk4rt calls over
	// gtk4rt.Handle, with no inline cgo. The reactive-if still lowers to a
	// __renderSlot0 with teardown-and-rebuild; only the target API changes.
	for _, snippet := range []string{
		"func (m *Model) __renderSlot0(parent gtk4rt.Handle)",
		"gtk4rt.BoxRemove(parent,",
		"m.__slot0 = nil",
		"gtk4rt.LabelNew(",
		// The slot-local label does not escape __renderSlot0 (it is created,
		// appended, and tracked in m.__slot0 all within this scope), so the
		// node-escape lower pass emits it as a function-local `__n0 := ...`
		// rather than a shared Model field. The slot slice keeps it alive.
		"__n0 := gtk4rt.LabelNew(",
		"gtk4rt.LabelSetText(",
		"gtk4rt.BoxAppend(",
		"m.__slot0 = append(m.__slot0",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted Go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

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
}

// TestIntegration_StdlibComponentsWrapWithoutCgo asserts the coverage goal: a
// program built only from stdlib components (which lower to the bounded native
// widget surface) generates in wrapped mode with zero inline cgo, so it imports
// pkg/go/gtk4rt and never `import "C"`. Users only pay the cgo compile cost
// when they reach outside the stdlib (raw gtk4.* widgets).
func TestIntegration_StdlibComponentsWrapWithoutCgo(t *testing.T) {
	src := `
component main {
    var name string = ""
    var on bool = false
    vbox {
        text(value="hello")
        button(text="go", @click { on = !on })
        input(value=name, @input(e) { name = e.value })
        checkbox(checked=on, label="check")
        image(src="/x.png", alt="x")
        hbox {
            text(value="a")
            text(value="b")
        }
        if on {
            text(value="shown")
        }
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, src := range mem.Files() {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.Contains(string(src), `import "C"`) {
			t.Errorf("%s used inline cgo; stdlib-only program should be fully wrapped", name)
		}
		if bytesUseCgo(src) {
			t.Errorf("%s has residual cgo reference; stdlib-only program should be fully wrapped", name)
		}
	}
	if !strings.Contains(string(mem.Files()["model.go"]), "gtk4rt.") {
		t.Error("expected model.go to call the gtk4rt runtime")
	}
}
