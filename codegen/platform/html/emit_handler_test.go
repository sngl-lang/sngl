package html

import (
	"strings"
	"testing"
)

func TestEventHandler_AsyncWrapper(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.handlers = []eventHandler{{
		elemID:  "$1",
		event:   "click",
		body:    "await loadAll();",
		isAsync: true,
	}}
	var b strings.Builder
	g.emitHandlers(&b)
	out := b.String()
	if !strings.Contains(out, `addEventListener("click", async function`) {
		t.Fatalf("expected async wrapper, got: %s", out)
	}
}

func TestEventHandler_SyncWrapper(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.handlers = []eventHandler{{
		elemID:  "$1",
		event:   "click",
		body:    "doStuff();",
		isAsync: false,
	}}
	var b strings.Builder
	g.emitHandlers(&b)
	out := b.String()
	if !strings.Contains(out, `addEventListener("click", function`) || strings.Contains(out, "async function") {
		t.Fatalf("expected sync wrapper, got: %s", out)
	}
}

func TestEventHandler_AsyncInputWrapper(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.handlers = []eventHandler{{
		elemID:  "$2",
		event:   "input",
		body:    "await save(e.target.value);",
		isAsync: true,
	}}
	var b strings.Builder
	g.emitHandlers(&b)
	out := b.String()
	if !strings.Contains(out, `addEventListener("input", async function(e)`) {
		t.Fatalf("expected async input wrapper, got: %s", out)
	}
}
