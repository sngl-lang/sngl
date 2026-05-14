package gtk4

import (
	"context"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

func stubGC() *golang.GoIRContext {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	return golang.NewIRContext(ctx)
}

// seedNative registers a fake GIR-resolved native component on the
// translator's tagComponent map so OnCreateNode and friends can resolve
// the tag without a real GIR registry. Mirrors what collectTagComponents
// would produce from a lowered package.
func seedNative(tr *gtk4Translator, tag, cType, constructor string) {
	c := &ir.Component{
		Name:   tag,
		Native: &gtk4NativeComponent{CType: cType, Constructor: constructor},
	}
	tr.tagComponent[tag] = c
}

func renderStmts(gc *golang.GoIRContext, stmts []ir.Stmt) string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, gc.EvalStmt(s)...)
	}
	return strings.Join(lines, "\n")
}

func TestGtk4Translator_OnCreateNode_Text(t *testing.T) {
	gc := stubGC()
	var fields []string
	tr := newGtk4Translator(gc, func(name, cType string) {
		fields = append(fields, name+" "+cType)
	})
	seedNative(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
	stmts := tr.OnCreateNode(context.Background(), "__n0", "GtkLabel")
	got := renderStmts(gc, stmts)
	if !strings.Contains(got, "C.gtk_label_new") {
		t.Errorf("expected C.gtk_label_new; got: %s", got)
	}
	if !strings.Contains(got, "unsafe.Pointer") {
		t.Errorf("expected unsafe.Pointer cast; got: %s", got)
	}
	if !strings.Contains(got, "(*C.GtkLabel)") {
		t.Errorf("expected (*C.GtkLabel) cast; got: %s", got)
	}
	if len(fields) != 1 || fields[0] != "__n0 GtkLabel" {
		t.Errorf("expected field __n0 GtkLabel; got: %v", fields)
	}
}

func TestGtk4Translator_OnCreateNode_UnknownTag(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	stmts := tr.OnCreateNode(context.Background(), "__n0", "wibble")
	if len(stmts) != 0 {
		t.Errorf("expected no stmts for unknown tag; got %d", len(stmts))
	}
}

func TestGtk4Translator_OnAppendChild_Box(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedNative(tr, "GtkBox", "GtkBox", "gtk_box_new")
	seedNative(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkBox")
	_ = tr.OnCreateNode(context.Background(), "__n1", "GtkLabel")
	parent := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	child := &ir.Ident{Name: "__n1", Synthesized: true, IsElementRef: true}
	got := renderStmts(gc, tr.OnAppendChild(context.Background(), parent, child))
	if !strings.Contains(got, "C.gtk_box_append") {
		t.Errorf("expected C.gtk_box_append; got: %s", got)
	}
}

func TestGtk4Translator_OnRemoveChild_Box(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedNative(tr, "GtkBox", "GtkBox", "gtk_box_new")
	seedNative(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkBox")
	_ = tr.OnCreateNode(context.Background(), "__n1", "GtkLabel")
	parent := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	child := &ir.Ident{Name: "__n1", Synthesized: true, IsElementRef: true}
	got := renderStmts(gc, tr.OnRemoveChild(context.Background(), parent, child))
	if !strings.Contains(got, "C.gtk_box_remove") {
		t.Errorf("expected C.gtk_box_remove; got: %s", got)
	}
}

func TestGtk4Translator_OnPropAssign_LabelText(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedNative(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkLabel")
	node := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	// gtk4.sngl maps stdlib `value` prop onto `label` for GtkLabel; the
	// translator's value/text → label fallback covers that.
	got := renderStmts(gc, tr.OnPropAssign(context.Background(), node, "value", val))
	if !strings.Contains(got, "C.gtk_label_set_") {
		t.Errorf("expected gtk_label_set_*; got: %s", got)
	}
	if !strings.Contains(got, "C.CString") {
		t.Errorf("expected C.CString conversion; got: %s", got)
	}
}

func TestGtk4Translator_OnAttachHandler_ButtonClick(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedNative(tr, "GtkButton", "GtkButton", "gtk_button_new")
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkButton")
	node := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	handler := &ir.Ident{Name: "handleClick", Synthesized: true}
	got := renderStmts(gc, tr.OnAttachHandler(context.Background(), node, "click", handler))
	if !strings.Contains(got, "C.sngl_connect") {
		t.Errorf("expected C.sngl_connect; got: %s", got)
	}
	if !strings.Contains(got, `"clicked"`) {
		t.Errorf("expected signal name 'clicked'; got: %s", got)
	}
	if !strings.Contains(got, "snglCallbacks") {
		t.Errorf("expected snglCallbacks registration; got: %s", got)
	}
}

func TestGtk4Translator_OnSlotReset(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	slot := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	got := renderStmts(gc, tr.OnSlotReset(context.Background(), slot))
	if !strings.Contains(got, "m.__slot0 = nil") {
		t.Errorf("expected slot reset to nil; got: %s", got)
	}
}

func TestGtk4Translator_OnSlotAppend(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	slot := &ir.Var{Name: "__slot0", Synthesized: true, Type: ir.ListOf(ir.TypDyn)}
	child := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	got := renderStmts(gc, tr.OnSlotAppend(context.Background(), slot, child))
	if !strings.Contains(got, "append(m.__slot0") {
		t.Errorf("expected append; got: %s", got)
	}
	if !strings.Contains(got, "*C.GtkWidget") {
		t.Errorf("expected GtkWidget cast; got: %s", got)
	}
}

func TestGtk4Translator_OnIter_Slot(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	got := gc.EvalExpr(tr.OnIter(context.Background(), &ir.Ident{Name: "__slot0", Synthesized: true}))
	if got != "m.__slot0" {
		t.Errorf("expected 'm.__slot0'; got: %s", got)
	}
}
