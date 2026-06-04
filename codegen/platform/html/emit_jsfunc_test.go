package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
)

func newMinimalHTMLGen(t *testing.T) *htmlGen {
	t.Helper()
	lang := codegen.LookupLang("js")
	if lang == nil {
		t.Fatal("javascript lang translator not registered")
	}
	return &htmlGen{
		lang:        lang,
		ctx:         codegen.NewExprCtx(nil),
		idToNode:    map[string]*ir.NodeInst{},
		loweredRefs: map[string]bool{},
	}
}

func TestEmitJSFunc_AsyncKeyword(t *testing.T) {
	g := newMinimalHTMLGen(t)
	fn := &ir.Func{
		Name:    "loadAll",
		IsAsync: true,
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Raw: `"x"`}},
		},
	}
	var b strings.Builder
	g.emitJSFunc(&b, fn)
	got := b.String()
	if !strings.HasPrefix(got, "async function loadAll") {
		t.Fatalf("missing async keyword: %q", got)
	}
}

func TestEmitJSFunc_NoAsyncKeywordWhenSync(t *testing.T) {
	g := newMinimalHTMLGen(t)
	fn := &ir.Func{
		Name:    "loadAll",
		IsAsync: false,
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Raw: `"x"`}},
		},
	}
	var b strings.Builder
	g.emitJSFunc(&b, fn)
	got := b.String()
	if !strings.HasPrefix(got, "function loadAll") {
		t.Fatalf("expected sync function, got: %q", got)
	}
	if strings.HasPrefix(got, "async") {
		t.Fatalf("unexpected async keyword: %q", got)
	}
}
