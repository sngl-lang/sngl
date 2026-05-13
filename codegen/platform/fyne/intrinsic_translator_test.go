package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// stubGC builds a GoIRContext suitable for translator unit tests —
// minimal ExprCtx over an empty Package so EvalExpr can resolve
// idents (falling through to the bare name when no symbol matches).
func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(codegen.NewExprCtx(&ir.Package{}))
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
	got := tr.OnAppendChild("parent", "m.__n0")
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
	got := tr.OnAttachHandler("m.__n0", "input", "m.handleInput")
	if !strings.Contains(got, "m.__n0.OnChanged = m.handleInput") {
		t.Errorf("expected OnChanged assignment; got: %s", got)
	}
}

func TestFyneTranslator_OnAttachHandler_UnknownEvent(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "input")
	got := tr.OnAttachHandler("m.__n0", "wibble", "m.h")
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

func TestFyneStmtDispatch_SlotTeardownFor(t *testing.T) {
	removeChild := &ir.Func{Name: "RemoveChild", Intrinsic: "RemoveChild"}
	stmt := &ir.For{
		Key:  "__entry",
		Iter: &ir.Ident{Name: "__slot0", Type: ir.ListOf(ir.TypDyn), Synthesized: true},
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Receiver: &ir.Ident{Name: "lower"},
				Func:     removeChild,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: "parent", IsElementRef: true}},
					{Value: &ir.Ident{Name: "__entry"}},
				},
			}},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "for _, __entry := range m.__slot0") {
		t.Errorf("expected range over m.__slot0; got:\n%s", got)
	}
	if !strings.Contains(got, "container.Remove(__entry)") {
		t.Errorf("expected container.Remove(__entry); got:\n%s", got)
	}
}

func TestFyneStmtDispatch_IfRecurses(t *testing.T) {
	listPush := &ir.Func{Name: "ListPush", Intrinsic: "ListPush"}
	stmt := &ir.If{
		Cond: &ir.Ident{Name: "visible"},
		Body: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "__slot0", Synthesized: true},
				Value: &ir.Call{
					Receiver: &ir.Ident{Name: "stdlib"},
					Func:     listPush,
					Args: []ir.CallArg{
						{Value: &ir.Ident{Name: "__slot0", Synthesized: true}},
						{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
					},
				},
			},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "if ") || !strings.Contains(got, "visible") {
		t.Errorf("expected 'if visible' shape; got: %s", got)
	}
	if !strings.Contains(got, "m.__slot0 = append(m.__slot0, m.__n0)") {
		t.Errorf("expected ListPush translated inside body; got:\n%s", got)
	}
}

func TestFyneStmtDispatch_SlotReset(t *testing.T) {
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
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
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
		Value: &ir.Call{
			Receiver: &ir.Ident{Name: "stdlib"},
			Func:     listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0", Synthesized: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
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

func TestFyneStmtDispatch_ReactiveForRecurses(t *testing.T) {
	// Simulates the inner For inside a reactive-for slot Func body:
	// the loop iterates a regular Model field, and the body creates
	// widgets via lower.CreateNode + appendChild.
	createNode := &ir.Func{Name: "CreateNode", Intrinsic: "CreateNode"}
	appendChild := &ir.Func{Name: "AppendChild", Intrinsic: "AppendChild"}
	stmt := &ir.For{
		Key: "item",
		Iter: &ir.Ident{
			Name: "items",
			Type: ir.ListOf(ir.TypString),
		},
		Body: []ir.Stmt{
			&ir.LocalVar{
				Name: "__n5",
				Type: ir.TypDyn,
				Init: &ir.Call{
					Receiver: &ir.Ident{Name: "lower"},
					Func:     createNode,
					Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "text"}}},
				},
			},
			&ir.CallStmt{Call: &ir.Call{
				Receiver: &ir.Ident{Name: "lower"},
				Func:     appendChild,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: "parent", IsElementRef: true}},
					{Value: &ir.Ident{Name: "__n5", IsElementRef: true, Synthesized: true}},
				},
			}},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "for _, item := range") {
		t.Errorf("expected 'for _, item := range'; got:\n%s", got)
	}
	if !strings.Contains(got, "m.__n5 = widget.NewLabel") {
		t.Errorf("expected widget.NewLabel inside For body; got:\n%s", got)
	}
	if !strings.Contains(got, "container.Add(m.__n5)") {
		t.Errorf("expected container.Add inside For body; got:\n%s", got)
	}
	if strings.Contains(got, "lower.CreateNode") {
		t.Errorf("expected intrinsic call NOT to leak through verbatim; got:\n%s", got)
	}
}
