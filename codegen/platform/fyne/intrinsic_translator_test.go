package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// stubGC builds a GoIRContext suitable for translator unit tests —
// no real package context, just enough machinery to emit Go strings.
func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(nil)
}

func TestFyneTranslator_OnCreateNode_Text(t *testing.T) {
	var fields []string
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(name, goType string) {
		fields = append(fields, name+" "+goType)
	})
	got := tr.OnCreateNode("__n0", "text")
	if !strings.Contains(got, "m.__n0 = widget.NewLabel(") {
		t.Errorf("expected widget.NewLabel constructor; got: %s", got)
	}
	want := "__n0 *widget.Label"
	if len(fields) != 1 || fields[0] != want {
		t.Errorf("expected field registration %q; got: %v", want, fields)
	}
}

func TestFyneTranslator_OnCreateNode_UnknownTag(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnCreateNode("__n0", "wibble")
	if got != "" {
		t.Errorf("expected empty emission for unknown tag; got: %s", got)
	}
}

func TestFyneTranslator_OnAppendChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnAppendChild("parent", "__n0")
	want := "parent.Add(m.__n0)\n"
	if got != want {
		t.Errorf("OnAppendChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnRemoveChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnRemoveChild("parent", "__entry")
	want := "parent.Remove(__entry)\n"
	if got != want {
		t.Errorf("OnRemoveChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnAttachHandler_InputChanged(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "input")
	got := tr.OnAttachHandler("__n0", "input", "m.handleInput")
	if !strings.Contains(got, "m.__n0.OnChanged = m.handleInput") {
		t.Errorf("expected OnChanged assignment; got: %s", got)
	}
}

func TestFyneTranslator_OnAttachHandler_UnknownEvent(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "input")
	got := tr.OnAttachHandler("__n0", "wibble", "m.h")
	if got != "" {
		t.Errorf("expected empty emission for unknown event; got: %s", got)
	}
}

func TestFyneTranslator_OnPropAssign_TextValue(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "text")

	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	got := tr.OnPropAssign("__n0", "value", val)
	want := `m.__n0.SetText(fmt.Sprint("hi"))` + "\n"
	if got != want {
		t.Errorf("OnPropAssign mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneTranslator_OnPropAssign_UnknownProp(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "text")
	val := &ir.Literal{Type: ir.TypString, Raw: "x"}
	got := tr.OnPropAssign("__n0", "wibble", val)
	if got != "" {
		t.Errorf("expected empty emission for unknown prop; got %q", got)
	}
}

func TestFyneStmtDispatch_SlotReset(t *testing.T) {
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0"},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	want := "m.__slot0 = nil"
	if got != want {
		t.Errorf("slot reset dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneStmtDispatch_SlotListPush(t *testing.T) {
	listPush := &ir.Func{Name: "ListPush", Intrinsic: "ListPush"}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0"},
		Value: &ir.Call{
			Receiver: &ir.Ident{Name: "stdlib"},
			Func:     listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0"}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	want := "m.__slot0 = append(m.__slot0, m.__n0)"
	if got != want {
		t.Errorf("slot ListPush dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}
