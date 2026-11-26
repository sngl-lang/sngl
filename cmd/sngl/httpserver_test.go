package main

import (
	"sync"
	"testing"

	"duckfam.us/sngl/internal/testgrpc"
	"duckfam.us/sngl/internal/testhttp"
)

var startTestHTTPOnce sync.Once

// The interpreter's requests are this process's, so a replaying run serves
// them at a port of its own, and runs side by side do not contend for one. A
// launch -update or -verify runs for real is another process, which finds the
// server only at the address its program writes.
//
// A launch answered from its record makes no request, so a failure to listen
// fails only a script that runs one for real.
func startTestHTTP(t *testing.T, root string) {
	startTestHTTPOnce.Do(func() {
		start := testhttp.Start
		if *updateTestRuns || *verifyTestRuns {
			start = testhttp.StartAt
		}
		if err := start(root); err != nil {
			t.Logf("test HTTP server not started: %v", err)
		}
		startGRPC := testgrpc.Start
		if *updateTestRuns || *verifyTestRuns {
			startGRPC = testgrpc.StartAt
		}
		if err := startGRPC(); err != nil {
			t.Logf("test gRPC server not started: %v", err)
		}
	})
}
