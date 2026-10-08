package fyne

import (
	"context"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// stubGC builds a GoIRContext suitable for translator unit tests —
// minimal ExprCtx over an empty Package so EvalExpr can resolve
// idents (falling through to the bare name when no symbol matches).
func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(codegen.NewExprCtx(&ir.Package{}))
}

// renderStmts feeds translator output through gc.EvalStmt to produce
// the rendered Go source the existing assertions match against.
func renderStmts(gc *golang.GoIRContext, stmts []ir.Stmt) string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, gc.EvalStmt(s)...)
	}
	return strings.Join(lines, "\n")
}

// specsAt is the node-id → Spec map a real compile builds by decoding the
// `spec` record off each created node (collectNodes). These tests exercise the
// translator rather than the decoding, so they hand it Specs built directly;
// that the records in fyne.sngl decode to these is what the integration tests
// cover.
func specsAt(pairs map[string]*fyneSpec) map[string]*fyneSpec { return pairs }

func labelSpec() *fyneSpec {
	return &fyneSpec{
		New:      fyneNative{Path: "fyne.io/fyne/v2/widget", Name: "NewLabel"},
		Args:     []fyneArg{{Raw: `""`}},
		GoType:   fyneNative{Path: "fyne.io/fyne/v2/widget", Name: "*Label"},
		Setters:  map[string]string{"text": "SetText"},
		Handlers: map[string]fyneHandler{},
	}
}

func entrySpec() *fyneSpec {
	return &fyneSpec{
		New:     fyneNative{Path: "fyne.io/fyne/v2/widget", Name: "NewEntry"},
		GoType:  fyneNative{Path: "fyne.io/fyne/v2/widget", Name: "*Entry"},
		Setters: map[string]string{"text": "SetText"},
		Handlers: map[string]fyneHandler{
			"input": {Field: "OnChanged", Signature: "func(s string)", Param: "s"},
		},
	}
}

// synthNodeRef builds the IR shape passed to the translator for a
// node previously created via OnCreateNode — bare Ident with
// Synthesized=true. The walker passes this verbatim from the
// underlying intrinsic call.
func synthNodeRef(name string) ir.Expr {
	return &ir.Ident{Name: name, Synthesized: true, IsElementRef: true}
}

func TestFyneTranslator_OnCreateNode_Label(t *testing.T) {
	gc := stubGC()
	var fields []string
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(name, goType string) {
		fields = append(fields, name+" "+goType)
	}, nil, nil)
	got := renderStmts(gc, tr.OnCreateNode(context.Background(), "__n0", "Label"))
	if !strings.Contains(got, "m.__n0 = widget.NewLabel(") {
		t.Errorf("expected widget.NewLabel constructor; got: %s", got)
	}
	want := "__n0 *widget.Label"
	if len(fields) != 1 || fields[0] != want {
		t.Errorf("expected field registration %q; got: %v", want, fields)
	}
}

// A node the pre-scan found no Spec for is not a fyne widget — a user
// component the inliner left in place, say — and the translator emits nothing
// for it.
func TestFyneTranslator_OnCreateNode_UnknownNode(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := tr.OnCreateNode(context.Background(), "__n9", "wibble")
	if len(got) != 0 {
		t.Errorf("expected empty emission for a node with no Spec; got: %v", got)
	}
}

func TestFyneTranslator_OnAppendChild(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	// Walker passes the slot-func param Ident bare; translator renames
	// to slotParentParam to match emitIRSlotFunc's typed-slot prologue.
	parent := &ir.Ident{Name: "parent"}
	child := synthNodeRef("__n0")
	got := renderStmts(gc, tr.OnAppendChild(context.Background(), parent, child))
	want := "__parent.Add(m.__n0)"
	if got != want {
		t.Errorf("OnAppendChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnRemoveChild(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	// "__entry" is the For loop-key (non-synthesized) so stays bare.
	parent := &ir.Ident{Name: "parent"}
	child := &ir.Ident{Name: "__entry"}
	got := renderStmts(gc, tr.OnRemoveChild(context.Background(), parent, child))
	want := "fynelayout.Remove(__parent, __entry)"
	if got != want {
		t.Errorf("OnRemoveChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnAttachHandler_InputChanged(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": entrySpec()}), func(_, _ string) {}, nil, nil)
	_ = tr.OnCreateNode(context.Background(), "__n0", "Entry")
	node := synthNodeRef("__n0")
	handler := &ir.Ident{Name: "__handleInput"}
	got := renderStmts(gc, tr.OnAttachHandler(context.Background(), node, "input", handler))
	if !strings.Contains(got, "m.__n0.OnChanged = m.__handleInput") {
		t.Errorf("expected OnChanged assignment; got: %s", got)
	}
}

func TestFyneTranslator_OnAttachHandler_UnknownEvent(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": entrySpec()}), func(_, _ string) {}, nil, nil)
	_ = tr.OnCreateNode(context.Background(), "__n0", "Entry")
	node := synthNodeRef("__n0")
	handler := &ir.Ident{Name: "h"}
	got := tr.OnAttachHandler(context.Background(), node, "wibble", handler)
	if len(got) != 0 {
		t.Errorf("expected empty emission for unknown event; got: %v", got)
	}
}

func TestFyneTranslator_OnPropAssign_LabelText(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	_ = tr.OnCreateNode(context.Background(), "__n0", "Label")

	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypString, Value: "hi"}
	got := renderStmts(gc, tr.OnPropAssign(context.Background(), node, "text", val))
	want := `m.__n0.SetText("hi")`
	if got != want {
		t.Errorf("OnPropAssign mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneTranslator_OnPropAssign_UnknownProp(t *testing.T) {
	gc := stubGC()
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	_ = tr.OnCreateNode(context.Background(), "__n0", "Label")
	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypString, Value: "x"}
	got := tr.OnPropAssign(context.Background(), node, "wibble", val)
	if len(got) != 0 {
		t.Errorf("expected empty emission for unknown prop; got %v", got)
	}
}

func TestFyneStmtDispatch_SlotTeardownFor(t *testing.T) {
	gc := stubGC()
	removeChild := &ir.Func{Name: "RemoveChild", Intrinsic: "RemoveChild"}
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	stmt := &ir.For{
		Key:  "__entry",
		Iter: &ir.Ident{Name: "__slot0", Type: ir.ListOf(ir.TypDyn), Synthesized: true, Sym: slotVar},
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
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := renderStmts(gc, codegen.WalkLowered(context.Background(), []ir.Stmt{stmt}, tr))
	if !strings.Contains(got, "for _, __entry := range m.__slot0") {
		t.Errorf("expected range over m.__slot0; got:\n%s", got)
	}
	if !strings.Contains(got, "fynelayout.Remove(__parent, __entry)") {
		t.Errorf("expected fynelayout.Remove(__parent, __entry); got:\n%s", got)
	}
}

func TestFyneStmtDispatch_IfRecurses(t *testing.T) {
	gc := stubGC()
	listPush := &ir.Func{Name: "list.push", Intrinsic: "list.push"}
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	stmt := &ir.If{
		Cond: &ir.Ident{Name: "visible"},
		Body: []ir.Stmt{
			&ir.CallStmt{
				Call: &ir.Call{
					Receiver: &ir.Ident{Name: "stdlib"},
					Func:     listPush,
					Args: []ir.CallArg{
						{Value: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar}},
						{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
					},
				},
			},
		},
	}
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := renderStmts(gc, codegen.WalkLowered(context.Background(), []ir.Stmt{stmt}, tr))
	if !strings.Contains(got, "if ") || !strings.Contains(got, "visible") {
		t.Errorf("expected 'if visible' shape; got: %s", got)
	}
	if !strings.Contains(got, "m.__slot0 = append(m.__slot0, m.__n0)") {
		t.Errorf("expected ListPush translated inside body; got:\n%s", got)
	}
}

func TestFyneStmtDispatch_SlotReset(t *testing.T) {
	gc := stubGC()
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := renderStmts(gc, codegen.WalkLowered(context.Background(), []ir.Stmt{stmt}, tr))
	want := "m.__slot0 = nil\nm.__slot0_at = fynelayout.SlotAnchor(__parent, m.__slot0_at)"
	if strings.TrimSpace(got) != want {
		t.Errorf("slot reset dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneStmtDispatch_SlotListPush(t *testing.T) {
	gc := stubGC()
	listPush := &ir.Func{Name: "list.push", Intrinsic: "list.push"}
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	// A call, not an assignment: push mutates its receiver and returns nothing,
	// which is the shape renderSlotBody builds.
	stmt := &ir.CallStmt{
		Call: &ir.Call{
			Receiver: &ir.Ident{Name: "stdlib"},
			Func:     listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
			},
		},
	}
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := renderStmts(gc, codegen.WalkLowered(context.Background(), []ir.Stmt{stmt}, tr))
	want := "m.__slot0 = append(m.__slot0, m.__n0)"
	if strings.TrimSpace(got) != want {
		t.Errorf("slot ListPush dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneStmtDispatch_ReactiveForRecurses(t *testing.T) {
	// Simulates the inner For inside a reactive-for slot Func body:
	// the loop iterates a regular Model field, and the body creates
	// widgets via lower.CreateNode + appendChild.
	gc := stubGC()
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
					Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: "Label"}}},
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
	tr := newFyneTranslator(gc, specsAt(map[string]*fyneSpec{"__n0": labelSpec(), "__n5": labelSpec()}), func(_, _ string) {}, nil, nil)
	got := renderStmts(gc, codegen.WalkLowered(context.Background(), []ir.Stmt{stmt}, tr))
	// The body never reads `item`, and Go refuses a loop variable declared
	// and not used.
	if !strings.Contains(got, "for range items {") {
		t.Errorf("expected 'for range items {'; got:\n%s", got)
	}
	if !strings.Contains(got, "m.__n5 = widget.NewLabel") {
		t.Errorf("expected widget.NewLabel inside For body; got:\n%s", got)
	}
	if !strings.Contains(got, "__parent.Add(m.__n5)") {
		t.Errorf("expected __parent.Add inside For body; got:\n%s", got)
	}
	if strings.Contains(got, "lower.CreateNode") {
		t.Errorf("expected intrinsic call NOT to leak through verbatim; got:\n%s", got)
	}
}
