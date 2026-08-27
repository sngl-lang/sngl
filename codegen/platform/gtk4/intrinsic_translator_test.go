package gtk4

import (
	"context"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

func stubGC() *golang.GoIRContext {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	return golang.NewIRContext(ctx)
}

// seedWidget registers one widget declaration on the translator plus the GIR
// entry the emitter reads its C API from, which together are what a real
// compile supplies: collectTagComponents finds the declaration, the platform
// hands over the registry it was generated from.
func seedWidget(tr *gtk4Translator, tag, cType, constructor string) {
	tr.tagComponent[tag] = &ir.Component{Name: tag, Intrinsic: intrinsicPrefix + cType}
	if tr.registry == nil {
		tr.registry = &gir.TypeRegistry{ByCType: map[string]*gir.ClassInfo{}}
	}
	tr.registry.ByCType[cType] = &gir.ClassInfo{
		CType:        cType,
		Constructors: []gir.ConstructorInfo{{Name: constructor}},
	}
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
	seedWidget(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
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
	shared := &emitShared{}
	tr := newGtk4Translator(gc, func(_, _ string) {}).withShared(shared)
	stmts := tr.OnCreateNode(context.Background(), "__n0", "wibble")
	if len(stmts) != 0 {
		t.Errorf("expected no stmts for unknown tag; got %d", len(stmts))
	}
	// Emitting nothing is only correct if the caller is told: a dropped node is
	// a widget missing from the window the user asked for.
	if len(shared.errs) != 1 {
		t.Fatalf("expected one diagnostic for an unknown tag; got %v", shared.errs)
	}
	if !strings.Contains(shared.errs[0].Error(), "wibble") {
		t.Errorf("diagnostic %q does not name the tag", shared.errs[0])
	}
}

func TestGtk4Translator_OnCreateNode_UnimplementedStdlibComponent(t *testing.T) {
	gc := stubGC()
	shared := &emitShared{}
	tr := newGtk4Translator(gc, func(_, _ string) {}).withShared(shared)
	// What an abstract stdlib component looks like at this point: no
	// #[intrinsic] C type, and no gtk4 entry in the overrides the checker
	// collected across every registered platform.
	tr.tagComponent["progress"] = &ir.Component{
		Name:           "progress",
		Stdlib:         true,
		Pkg:            "sngl://std",
		PlatformBodies: map[string][]ir.Stmt{"html": nil},
	}
	if stmts := tr.OnCreateNode(context.Background(), "__n0", "progress"); len(stmts) != 0 {
		t.Errorf("expected no stmts; got %d", len(stmts))
	}
	if len(shared.errs) != 1 {
		t.Fatalf("expected one diagnostic; got %v", shared.errs)
	}
	got := shared.errs[0].Error()
	for _, want := range []string{`"progress"`, "gtk4"} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostic %q does not mention %s", got, want)
		}
	}
}

func TestGtk4Translator_OnCreateNode_UserComponentIsSilent(t *testing.T) {
	gc := stubGC()
	shared := &emitShared{}
	tr := newGtk4Translator(gc, func(_, _ string) {}).withShared(shared)
	// A component the program declared itself with an empty body draws nothing
	// on purpose. Only a library declaration this platform failed to implement
	// is a gap.
	tr.tagComponent["label"] = &ir.Component{Name: "label"}
	if stmts := tr.OnCreateNode(context.Background(), "__n0", "label"); len(stmts) != 0 {
		t.Errorf("expected no stmts; got %d", len(stmts))
	}
	if len(shared.errs) != 0 {
		t.Fatalf("expected no diagnostic for a user component; got %v", shared.errs)
	}
}

func TestGtk4Translator_OnAppendChild_Box(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedWidget(tr, "GtkBox", "GtkBox", "gtk_box_new")
	seedWidget(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
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
	seedWidget(tr, "GtkBox", "GtkBox", "gtk_box_new")
	seedWidget(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
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
	seedWidget(tr, "GtkLabel", "GtkLabel", "gtk_label_new")
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
	seedWidget(tr, "GtkButton", "GtkButton", "gtk_button_new")
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

// seedShared gives the translator the per-file accumulator a real compile
// hands it, so a refusal is recorded rather than dropped by the nil sink.
func seedShared(tr *gtk4Translator) *emitShared {
	s := &emitShared{}
	tr.withShared(s)
	return s
}

// TestGtk4Translator_OnAttachHandler_RefusesWrongArity pins the emitter half
// of the signal-shape rule. declgen withholds a signal the fixed (instance,
// user_data) trampoline cannot carry, so this path is reached only when the
// event arrives some other way — a static table entry, or a stdlib override
// written against a signal that is not connectable. It has to refuse the
// build rather than connect a callback that misfires.
func TestGtk4Translator_OnAttachHandler_RefusesWrongArity(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedWidget(tr, "GtkEntry", "GtkEntry", "gtk_entry_new")
	// One argument of its own: GTK would pass it where the callback index
	// is expected.
	tr.registry.ByCType["GtkEntry"].Signals = []gir.Signal{
		{Name: "icon-press", Params: 1, ReturnType: "none"},
		{Name: "changed", Params: 0, ReturnType: "none"},
	}
	shared := seedShared(tr)
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkEntry")
	node := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	handler := &ir.Ident{Name: "onPress", Synthesized: true}

	if got := renderStmts(gc, tr.OnAttachHandler(context.Background(), node, "iconPress", handler)); got != "" {
		t.Errorf("emitted a connection for icon-press: %s", got)
	}
	if len(shared.errs) != 1 {
		t.Fatalf("recorded %d errors; want 1: %v", len(shared.errs), shared.errs)
	}
	for _, want := range []string{"GtkEntry", "iconPress", "icon-press", "1 argument"} {
		if !strings.Contains(shared.errs[0].Error(), want) {
			t.Errorf("error %q does not mention %q", shared.errs[0], want)
		}
	}
	// The connectable sibling still connects.
	if got := renderStmts(gc, tr.OnAttachHandler(context.Background(), node, "changed", handler)); !strings.Contains(got, `"changed"`) {
		t.Errorf("changed was not connected: %s", got)
	}
}

// TestGtk4Translator_OnPropAssign_RefusesConstructOnly pins the emitter half
// of the construct-only rule: GObject answers a post-construction write with a
// g_critical and no change, so emitting the generic set would compile and do
// nothing. declgen withholds these, making this the same defence-in-depth as
// the signal case.
func TestGtk4Translator_OnPropAssign_RefusesConstructOnly(t *testing.T) {
	gc := stubGC()
	tr := newGtk4Translator(gc, func(_, _ string) {})
	seedWidget(tr, "GtkAssistant", "GtkAssistant", "gtk_assistant_new")
	tr.registry.ByCType["GtkAssistant"].Props = []gir.Prop{
		{Name: "use-header-bar", ConstructOnly: true, GIRType: "gint", IRType: &ir.Type{Kind: ir.TypeInt}},
	}
	shared := seedShared(tr)
	_ = tr.OnCreateNode(context.Background(), "__n0", "GtkAssistant")
	node := &ir.Ident{Name: "__n0", Synthesized: true, IsElementRef: true}
	val := &ir.Literal{Type: ir.TypInt, Raw: "1"}

	if got := renderStmts(gc, tr.OnPropAssign(context.Background(), node, "useHeaderBar", val)); got != "" {
		t.Errorf("emitted a set for a construct-only property: %s", got)
	}
	if len(shared.errs) != 1 {
		t.Fatalf("recorded %d errors; want 1: %v", len(shared.errs), shared.errs)
	}
	for _, want := range []string{"GtkAssistant", "use-header-bar", "construct-only"} {
		if !strings.Contains(shared.errs[0].Error(), want) {
			t.Errorf("error %q does not mention %q", shared.errs[0], want)
		}
	}
}
