package gtk4

import (
	"bytes"
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
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	if err := lower.Lower(pkg, lower.Caps{NoReactivity: true, NoDeclarative: true}, lower.Options{Platform: "gtk4"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	resp, err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("generate error: %s", resp.Error)
	}
	if len(resp.Files) == 0 {
		t.Fatal("no files emitted")
	}

	var buf bytes.Buffer
	if _, err := resp.Files[0].WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	out := buf.String()

	for _, snippet := range []string{
		"func (m *Model) __renderSlot0(container *C.GtkBox)",
		"C.gtk_box_remove",
		"m.__slot0 = nil",
		"C.gtk_label_new",
		"m.__n0 = (*C.GtkLabel)",
		"C.gtk_label_set_",
		"C.gtk_box_append",
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
