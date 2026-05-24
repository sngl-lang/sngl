// Package testagent is the Go runtime linked into a SNGL-emitted binary
// when codegen runs in agent mode (sngl test). It exposes a T type that
// mirrors *testing.T's surface so the same lowering output works in both
// native and agent modes — only the import path differs.
package testagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

// T is the per-test handle passed to user test functions. Its method
// surface mirrors a subset of *testing.T's so that golang.LowerTestFunc
// output is identical across modes.
type T struct {
	name    string
	parent  *T
	w       *testrpc.Writer
	failed  bool
	skipped bool
}

// abortSentinel is panicked by FailNow/SkipNow and recovered by the
// per-test goroutine to unwind the test body.
type abortSentinel struct{}

// newAgentT is exported for testing; production code goes through runOne.
func newAgentT(out io.Writer, name string) *T {
	return &T{name: name, w: testrpc.NewWriter(out)}
}

// --- API surface (mirrors testing.T) ---

func (t *T) Log(msg string) {
	_ = t.w.Notify("log", map[string]any{"test": t.name, "msg": msg})
}

func (t *T) Logf(format string, args ...any) { t.Log(fmt.Sprintf(format, args...)) }

func (t *T) Fail() {
	t.failed = true
	_ = t.w.Notify("markFail", map[string]any{"test": t.name})
}

func (t *T) FailNow() {
	t.Fail()
	panic(abortSentinel{})
}

func (t *T) Errorf(format string, args ...any) {
	t.Log(fmt.Sprintf(format, args...))
	t.Fail()
}

func (t *T) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(abortSentinel{})
}

func (t *T) Skip(args ...any) {
	t.skipped = true
	reason := fmt.Sprint(args...)
	_ = t.w.Notify("markSkip", map[string]any{"test": t.name, "reason": reason})
	panic(abortSentinel{})
}

// Run runs a subtest. Mirrors testing.T.Run.
func (t *T) Run(name string, body func(*T)) bool {
	full := name
	if t.name != "" {
		full = t.name + "/" + name
	}
	child := &T{name: full, parent: t, w: t.w}
	runBody(t.w, full, child, body)
	return !child.failed
}

// --- Registry ---

var (
	regMu    sync.Mutex
	registry = map[string]func(*T){}
)

func RegisterTest(name string, fn func(*T)) {
	regMu.Lock()
	registry[name] = fn
	regMu.Unlock()
}

func resetRegistry() {
	regMu.Lock()
	registry = map[string]func(*T){}
	regMu.Unlock()
}

func listTests() []string {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- Driver loop ---

// Main is the entry point a SNGL-emitted main package calls. It speaks
// JSON-RPC over stdin/stdout: stdin carries Driver→Agent requests;
// stdout carries Agent→Driver notifications + responses.
func Main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := testrpc.NewReader(os.Stdin)
	out := testrpc.NewWriter(os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msg, err := in.Read()
		if err == io.EOF {
			return
		}
		if err != nil {
			_ = out.Respond(0, nil, &testrpc.RPCError{Code: -32700, Message: err.Error()})
			continue
		}
		if msg.ID == nil {
			continue // ignore stray notifications driver→agent
		}
		id := *msg.ID
		switch msg.Method {
		case "list":
			_ = out.Respond(id, map[string]any{"tests": listTests()}, nil)
		case "run":
			var params struct{ Filter string }
			_ = json.Unmarshal(msg.Params, &params)
			runFiltered(out, params.Filter)
			_ = out.Respond(id, map[string]any{}, nil)
		case "cancel":
			cancel()
			_ = out.Respond(id, map[string]any{}, nil)
		default:
			_ = out.Respond(id, nil, &testrpc.RPCError{Code: -32601, Message: "method not found: " + msg.Method})
		}
	}
}

func runFiltered(w *testrpc.Writer, filter string) {
	var passed, failed, skipped int
	for _, name := range listTests() {
		if filter != "" && !match(name, filter) {
			continue
		}
		switch runOne(w, name) {
		case "pass":
			passed++
		case "fail":
			failed++
		case "skip":
			skipped++
		}
	}
	_ = w.Notify("runComplete", map[string]any{
		"passed":  passed,
		"failed":  failed,
		"skipped": skipped,
	})
}

func match(name, filter string) bool {
	// Simple substring match for now — mirrors go test -run's default.
	return bytes.Contains([]byte(name), []byte(filter))
}

func runOne(w *testrpc.Writer, name string) string {
	regMu.Lock()
	fn, ok := registry[name]
	regMu.Unlock()
	if !ok {
		_ = w.Notify("testEnd", map[string]any{"test": name, "status": "fail", "durationMs": 0})
		return "fail"
	}
	at := &T{name: name, w: w}
	return runBody(w, name, at, fn)
}

func runBody(w *testrpc.Writer, name string, t *T, fn func(*T)) string {
	_ = w.Notify("testStart", map[string]any{"test": name})
	start := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(abortSentinel); !ok {
					t.failed = true
					_ = w.Notify("log", map[string]any{"test": name, "msg": fmt.Sprintf("panic: %v", r)})
				}
			}
		}()
		fn(t)
	}()
	status := "pass"
	if t.skipped {
		status = "skip"
	} else if t.failed {
		status = "fail"
	}
	_ = w.Notify("testEnd", map[string]any{
		"test":       name,
		"status":     status,
		"durationMs": int(time.Since(start).Milliseconds()),
	})
	return status
}
