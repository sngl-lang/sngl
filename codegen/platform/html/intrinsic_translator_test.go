package html

import (
	"context"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// stubJsCtx builds a JsIRContext suitable for translator unit tests —
// minimal ExprCtx over an empty Package so EvalExpr can resolve idents
// (falling through to the bare name when no symbol matches).
func stubJsCtx() *javascript.JsIRContext {
	return javascript.NewIRContext(codegen.NewExprCtx(&ir.Package{}))
}

// renderStmts feeds translator output through jc.EvalStmt to produce
// the rendered JS source the assertions match against.
func renderStmts(jc *javascript.JsIRContext, stmts []ir.Stmt) string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, jc.EvalStmt(s)...)
	}
	return strings.Join(lines, "\n")
}

// newTranslatorForTest builds a translator carrying the REAL `element`
// declaration from lib/platforms/html. The translator answers which props are
// boolean and which events exist from that declaration, so a stub here would
// let the two drift apart silently — the bug these tests now cover.
func newTranslatorForTest(t *testing.T, jc *javascript.JsIRContext) *htmlTranslator {
	t.Helper()
	return &htmlTranslator{jc: jc, idTags: map[string]string{}, rawElem: elementDeclForTest(t)}
}

// elementDeclForTest checks a one-line program that names the html platform
// package, then pulls the wildcard element declaration back out of its IR.
func elementDeclForTest(t *testing.T) *ir.Component {
	t.Helper()
	const src = "import . \"sngl://std\"\nimport html \"sngl://platforms/html\"\n\nwindow(\"t\") {\n    html.div {}\n}\n"
	doc, err := parser.Parse("elemdecl.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The html platform is passed in directly: an internal test in this
	// package cannot import internal/testtargets without closing an import
	// cycle back through codegen/platform.
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{&Generator{}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}
	c := rawElementDecl(pkg)
	if c == nil {
		t.Fatal("no wildcard element declaration found in lib/platforms/html")
	}
	return c
}

// synthNodeRef builds the IR shape the walker passes to the
// translator for a node previously created via OnCreateNode — a bare
// Ident with Synthesized=true.
func synthNodeRef(name string) ir.Expr {
	return &ir.Ident{Name: name, Synthesized: true, IsElementRef: true}
}

func TestHTMLTranslator_OnCreateNode_Text(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	// passInlinePure substitutes stdlib `text` → html.sngl's `html.span`,
	// so the translator receives the native DOM tag.
	got := renderStmts(jc, tr.OnCreateNode(context.Background(), "__n0", "span"))
	if !strings.Contains(got, `document.createElement("span")`) {
		t.Errorf("expected createElement(\"span\"); got: %s", got)
	}
	if !strings.Contains(got, "let __n0 =") {
		t.Errorf("expected let __n0 binding; got: %s", got)
	}
}

func TestHTMLTranslator_OnCreateNode_Button(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	got := renderStmts(jc, tr.OnCreateNode(context.Background(), "__n1", "button"))
	if !strings.Contains(got, `document.createElement("button")`) {
		t.Errorf("expected createElement(\"button\"); got: %s", got)
	}
}

func TestHTMLTranslator_OnCreateNode_ArbitraryTag(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	// html accepts any tag as a valid DOM element name (post-Plan-G the
	// translator passes the tag through verbatim to document.createElement;
	// validation lives in the checker via the html platform's Resolver).
	got := renderStmts(jc, tr.OnCreateNode(context.Background(), "__n0", "mystery"))
	if !strings.Contains(got, `document.createElement("mystery")`) {
		t.Errorf("expected createElement(\"mystery\"); got: %s", got)
	}
}

func TestHTMLTranslator_OnAppendChild(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	parent := synthNodeRef("__n0")
	child := synthNodeRef("__n1")
	got := renderStmts(jc, tr.OnAppendChild(context.Background(), parent, child))
	if !strings.Contains(got, "__n0.appendChild(__n1)") {
		t.Errorf("expected __n0.appendChild(__n1); got: %s", got)
	}
}

func TestHTMLTranslator_OnRemoveChild(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	parent := synthNodeRef("__root")
	child := synthNodeRef("__entry")
	got := renderStmts(jc, tr.OnRemoveChild(context.Background(), parent, child))
	if !strings.Contains(got, "__root.removeChild(__entry)") {
		t.Errorf("expected __root.removeChild(__entry); got: %s", got)
	}
}

func TestHTMLTranslator_OnPropAssign_TextValue(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	// html.sngl's sngl.text body uses html.span(textContent=value), so
	// post-inline the prop landing here is "textContent" directly.
	_ = tr.OnCreateNode(context.Background(), "__n0", "span")
	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	got := renderStmts(jc, tr.OnPropAssign(context.Background(), node, "textContent", val))
	if !strings.Contains(got, `__n0.textContent = "hi"`) {
		t.Errorf("expected __n0.textContent = \"hi\"; got: %s", got)
	}
}

func TestHTMLTranslator_OnPropAssign_InputValue(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	_ = tr.OnCreateNode(context.Background(), "__n0", "input")
	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypString, Raw: "x"}
	got := renderStmts(jc, tr.OnPropAssign(context.Background(), node, "value", val))
	if !strings.Contains(got, `__n0.value = "x"`) {
		t.Errorf("expected __n0.value = \"x\"; got: %s", got)
	}
}

func TestHTMLTranslator_OnPropAssign_CheckboxChecked(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	_ = tr.OnCreateNode(context.Background(), "__n0", "checkbox")
	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypBool, Raw: "true"}
	got := renderStmts(jc, tr.OnPropAssign(context.Background(), node, "checked", val))
	if !strings.Contains(got, "__n0.checked = true") {
		t.Errorf("expected __n0.checked = true; got: %s", got)
	}
}

func TestHTMLTranslator_OnPropAssign_FallbackSetAttribute(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	_ = tr.OnCreateNode(context.Background(), "__n0", "text")
	node := synthNodeRef("__n0")
	val := &ir.Literal{Type: ir.TypString, Raw: "abc"}
	got := renderStmts(jc, tr.OnPropAssign(context.Background(), node, "data-x", val))
	if !strings.Contains(got, `__n0.setAttribute("data-x", "abc")`) {
		t.Errorf("expected setAttribute fallback; got: %s", got)
	}
}

func TestHTMLTranslator_OnAttachHandler_Click(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	_ = tr.OnCreateNode(context.Background(), "__n0", "button")
	node := synthNodeRef("__n0")
	handler := &ir.Ident{Name: "handleClick"}
	got := renderStmts(jc, tr.OnAttachHandler(context.Background(), node, "click", handler))
	if !strings.Contains(got, `__n0.addEventListener("click", handleClick)`) {
		t.Errorf("expected addEventListener(click, handleClick); got: %s", got)
	}
}

// A lowercase name the element declares no payload for still reaches a
// listener: its wildcard event answers to every DOM event name, which is why
// the translator reads the declaration instead of holding a list of four.
func TestHTMLTranslator_OnAttachHandler_WildcardEvent(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	node := synthNodeRef("__n0")
	handler := &ir.Ident{Name: "fn"}
	got := renderStmts(jc, tr.OnAttachHandler(context.Background(), node, "mouseover", handler))
	if !strings.Contains(got, `__n0.addEventListener("mouseover", fn)`) {
		t.Errorf("expected addEventListener(mouseover, fn); got: %s", got)
	}
}

// A name the declaration cannot answer to — an HTML event name is lowercase —
// attaches nothing rather than emitting a listener for an event that is a
// misspelling.
func TestHTMLTranslator_OnAttachHandler_UndeclaredEvent(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	node := synthNodeRef("__n0")
	handler := &ir.Ident{Name: "fn"}
	stmts := tr.OnAttachHandler(context.Background(), node, "MouseOver", handler)
	if stmts != nil {
		t.Errorf("expected nil for an undeclared event; got: %v", stmts)
	}
}

func TestHTMLTranslator_OnSlotReset(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	slot := &ir.Var{Name: "__slot0", Type: ir.TypDyn, Synthesized: true}
	got := renderStmts(jc, tr.OnSlotReset(context.Background(), slot))
	if !strings.Contains(got, "__slot0 = []") {
		t.Errorf("expected __slot0 = []; got: %s", got)
	}
}

func TestHTMLTranslator_OnSlotAppend(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	slot := &ir.Var{Name: "__slot0", Type: ir.TypDyn, Synthesized: true}
	child := synthNodeRef("__n0")
	got := renderStmts(jc, tr.OnSlotAppend(context.Background(), slot, child))
	if !strings.Contains(got, "__slot0.push(__n0)") {
		t.Errorf("expected __slot0.push(__n0); got: %s", got)
	}
}

func TestHTMLTranslator_OnIter_Passthrough(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	iter := &ir.Ident{Name: "items", Synthesized: true}
	got := tr.OnIter(context.Background(), iter)
	if got != iter {
		t.Errorf("expected passthrough iter; got: %v", got)
	}
}

func TestHTMLTranslator_OnCond_Passthrough(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	cond := &ir.Ident{Name: "visible"}
	got := tr.OnCond(context.Background(), cond)
	if got != cond {
		t.Errorf("expected passthrough cond; got: %v", got)
	}
}

func TestHTMLTranslator_OnDefault_Passthrough(t *testing.T) {
	jc := stubJsCtx()
	tr := newTranslatorForTest(t, jc)
	stmt := &ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "x"}}
	got := tr.OnDefault(context.Background(), stmt)
	if len(got) != 1 || got[0] != stmt {
		t.Errorf("expected single-element passthrough; got: %v", got)
	}
}
