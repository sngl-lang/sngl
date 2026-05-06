package html

import (
	"strings"
	"testing"
)

func TestEmitTimer_AsyncTick(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.timers = []timerDef{{
		index:      0,
		intervalMs: 1000,
		activeVar:  "running",
		body:       "await poll();",
		bodyAsync:  true,
	}}
	var b strings.Builder
	g.emitTimers(&b)
	out := b.String()
	if !strings.Contains(out, "async function $timer_0_tick()") {
		t.Fatalf("expected async tick, got:\n%s", out)
	}
	if strings.Contains(out, "async function $timer_0_sync()") {
		t.Fatalf("sync wrapper must not be async, got:\n%s", out)
	}
}

func TestEmitTimer_SyncTick(t *testing.T) {
	g := newMinimalHTMLGen(t)
	g.timers = []timerDef{{
		index:      0,
		intervalMs: 500,
		activeVar:  "running",
		body:       "doStuff();",
		bodyAsync:  false,
	}}
	var b strings.Builder
	g.emitTimers(&b)
	out := b.String()
	if strings.Contains(out, "async function") {
		t.Fatalf("expected sync tick, got:\n%s", out)
	}
	if !strings.Contains(out, "function $timer_0_tick()") {
		t.Fatalf("expected plain function tick, got:\n%s", out)
	}
}
