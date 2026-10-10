//go:build !js

package html

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// A browser that never answers fails the launch with the step's name, rather
// than holding `sngl test` -- and the test binary waiting on it -- forever.
func TestLaunchTestNamesAStalledLaunch(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer swapBrowser(fake, browserSteps{launch: time.Second})()

	start := time.Now()
	ch, cleanup, err := (&Generator{}).LaunchTest(context.Background(), t.TempDir(), nil, nil)
	if err == nil {
		cleanup()
		ch.Close()
		t.Fatal("a browser that never printed its DevTools URL launched")
	}
	if !strings.Contains(err.Error(), "launch browser") {
		t.Errorf("error does not name the step: %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the stalled launch took %v to fail", d.Round(time.Second))
	}
}

// An agent that stops talking mid-run fails the read with the step's name.
func TestAgentSilenceNamesTheStep(t *testing.T) {
	defer swapBrowser("", browserSteps{agentIdle: 200 * time.Millisecond})()

	accepted := make(chan *wsChannel, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- newWSChannel(context.Background(), c)
		<-r.Context().Done()
	}))
	defer srv.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()

	ch := <-accepted
	done := make(chan error, 1)
	go func() {
		_, err := ch.Read(make([]byte, 64))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "run tests") {
			t.Errorf("silent agent: got %v, want an error naming the step", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read from a silent agent never returned")
	}
}

// swapBrowser points LaunchTest at bin and shortens the steps given, putting
// both back when the returned func runs.
func swapBrowser(bin string, steps browserSteps) func() {
	oldBin, oldSteps := findBrowser, cdpSteps
	if bin != "" {
		findBrowser = func() (string, bool) { return bin, true }
	}
	merged := cdpSteps
	for _, p := range []struct{ dst, src *time.Duration }{
		{&merged.launch, &steps.launch}, {&merged.connect, &steps.connect},
		{&merged.openPage, &steps.openPage}, {&merged.agentConnect, &steps.agentConnect},
		{&merged.agentIdle, &steps.agentIdle}, {&merged.close, &steps.close},
	} {
		if *p.src != 0 {
			*p.dst = *p.src
		}
	}
	cdpSteps = merged
	return func() { findBrowser, cdpSteps = oldBin, oldSteps }
}
