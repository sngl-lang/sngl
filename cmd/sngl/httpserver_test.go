package main

import (
	"sync"
	"testing"

	"duckfam.us/sngl/internal/testhttp"
)

var startTestHTTPOnce sync.Once

// A launch answered from its record makes no request, so a failure to listen
// fails only a script that runs one for real.
func startTestHTTP(t *testing.T, root string) {
	startTestHTTPOnce.Do(func() {
		if err := testhttp.Start(root); err != nil {
			t.Logf("test HTTP server not started: %v", err)
		}
	})
}
