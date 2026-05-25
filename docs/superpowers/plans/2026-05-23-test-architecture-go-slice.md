# Test architecture — Go vertical slice — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Go-language end-to-end slice of the new SNGL test architecture: wire protocol, per-language testagent runtime, NativeTestEmitter, `codegen.TestLauncher` interface, bubbletea & fyne migration onto the new path, and `--opt test=true` wiring.

**Architecture:** Each test func emits to one of two destinations: a native Go test file (`*_test.go` calling `*testing.T`) for `--opt test=true`, or an agent-runtime entry point (linked against `pkg/go/testagent`) for `sngl test`. Both share the same lowering of the test body — only the wrapper differs. Under `sngl test` the driver spawns the compiled agent binary and exchanges JSON-RPC. The `pkg/go/testagent.T` type mirrors `*testing.T`'s API surface so lowering output is nearly identical between modes.

**Tech Stack:** Go, `encoding/json`, `bufio.Scanner` (line-delimited JSON), cobra, `internal/checker`, existing `codegen/lang/golang` translator.

**Spec ref:** `docs/superpowers/specs/2026-05-23-test-architecture-design.md`

**No worktree** — per project memory, work directly on `main`.

---

## File map

- Create `internal/testrpc/` — JSON-RPC 2.0 codec + standard message types. Used by both driver and agent.
- Create `pkg/go/testagent/` — Go testagent runtime: `T` type, intrinsics, registry, `Main`, abort.
- Modify `codegen/codegen.go` — add `TestLauncher` interface (sibling of `Builder`).
- Create `codegen/lang/golang/launcher.go` — Go's TestLauncher: build, spawn, talk RPC over stdin/stdout.
- Modify `codegen/lang/golang/testlower.go` — parameterise receiver-type (testing.T vs testagent.T); add agent-mode wrapper (`registerTest` init) and native-mode wrapper (file scaffold).
- Modify `codegen/lang/golang/golang.go` (or new file) — emit `testagent_main.go` under agent mode; emit `*_test.go` under native mode (`--opt test=true`).
- Create `codegen/testharness/snapshot/` — golden-file diff machinery (text + ANSI; PNG support arrives with fyne phase but the package is created here).
- Modify `cmd/sngl/test.go` — drop the per-platform-runners-by-type-assertion flow; resolve TestLauncher (platform → language fallback); aggregate RPC stream into existing `codegen.TestResult` shape.
- Delete `codegen/platform/bubbletea/runtests.go`, `codegen/platform/fyne/runtests.go` — their work moves into the Go TestLauncher + the testagent's RPC handling. Per-platform glue (e.g. promotion via `testharness.Promote`) stays but is invoked from a single place.
- Modify `cmd/sngl/pipeline.go` — when `optionBool(opts, "test")` is true, instruct codegen to emit native test files alongside the regular source.

---

## Sequencing within the plan

Tasks build incrementally. After each task, the project builds and the unrelated test suite stays green:

1. **Task 1**: RPC types + framing (no consumer yet).
2. **Task 2**: testagent runtime built on Task 1's RPC types.
3. **Task 3**: `TestLauncher` interface declaration.
4. **Task 4**: Go `TestLauncher` — uses Task 2's runtime, Task 1's framing.
5. **Task 5**: testlower parameterisation (split native vs agent wrapper).
6. **Task 6**: emit testagent_main.go in agent mode + native `_test.go` files in native mode.
7. **Task 7**: snapshot machinery (text+ANSI).
8. **Task 8**: `cmd/sngl/test.go` driver refactor onto TestLauncher.
9. **Task 9**: bubbletea cutover (delete its runtests.go).
10. **Task 10**: fyne cutover (delete its runtests.go).
11. **Task 11**: `--opt test=true` wiring in `cmd/sngl/pipeline.go`.
12. **Task 12**: end-to-end fixture covering both modes; final sweep.

---

### Task 1: RPC types + framing

**Files:**
- Create: `internal/testrpc/rpc.go`
- Create: `internal/testrpc/rpc_test.go`

Build a minimal JSON-RPC 2.0 codec scoped to what the test protocol needs: one direction at a time over `io.Reader`/`io.Writer`, line-delimited JSON (one message per line). No batching, no positional params.

- [ ] **Step 1: Write the failing test first**

Create `internal/testrpc/rpc_test.go`:

```go
package testrpc

import (
	"bytes"
	"testing"
)

func TestCodec_writeAndReadNotification(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Notify("log", map[string]any{"test": "T1", "msg": "hi"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	r := NewReader(&buf)
	msg, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Method != "log" {
		t.Errorf("method = %q, want log", msg.Method)
	}
	if msg.IsNotification() != true {
		t.Errorf("expected notification (no id), got request")
	}
}

func TestCodec_writeAndReadRequest_thenResponse(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	id, err := w.Request("list", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := w.Respond(id, map[string]any{"tests": []string{"a", "b"}}, nil); err != nil {
		t.Fatalf("respond: %v", err)
	}
	r := NewReader(&buf)
	req, err := r.Read()
	if err != nil {
		t.Fatalf("read req: %v", err)
	}
	if req.Method != "list" {
		t.Errorf("method = %q", req.Method)
	}
	if req.IsNotification() {
		t.Errorf("expected request (has id)")
	}
	resp, err := r.Read()
	if err != nil {
		t.Fatalf("read resp: %v", err)
	}
	if resp.Method != "" {
		t.Errorf("response should have empty Method")
	}
	if resp.ID == nil {
		t.Errorf("response missing id")
	}
}

func TestReader_malformedLineReturnsError(t *testing.T) {
	r := NewReader(bytes.NewBufferString("not json\n"))
	if _, err := r.Read(); err == nil {
		t.Error("expected error on malformed line")
	}
}
```

- [ ] **Step 2: Run the test, confirm it fails**

```bash
go test ./internal/testrpc/ -count=1
```

Expected: `no Go files` or undefined-symbol errors — package doesn't exist yet.

- [ ] **Step 3: Implement the codec**

Create `internal/testrpc/rpc.go`:

```go
// Package testrpc is the JSON-RPC 2.0 codec shared by the sngl test
// driver and per-language testagent runtimes. Messages are line-
// delimited JSON, one per line. Requests carry an id; notifications
// omit it. Responses carry the request id plus either Result or Error.
//
// The codec is deliberately minimal: no batching, no positional params,
// no string id format (ints only). Sufficient for our wire and trivial
// to reimplement in each agent language.
package testrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Message is the discriminated union of request, notification, and
// response. JSON tags use omitempty so a single struct round-trips
// all three forms.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`     // nil = notification when Method != ""
	Method  string          `json:"method,omitempty"` // empty on responses
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsNotification reports whether the message is a request without an
// id — i.e. the sender expects no response.
func (m *Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsResponse reports whether the message is a response (Method empty).
func (m *Message) IsResponse() bool { return m.Method == "" && m.ID != nil }

// RPCError mirrors JSON-RPC 2.0's error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Reader reads one Message per Read call.
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	// Snapshot frames can be hundreds of KB after base64. 8 MiB cap is
	// generous and prevents runaway memory on a bad sender.
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	return &Reader{sc: sc}
}

// Read returns the next message, or io.EOF when the stream ends.
func (r *Reader) Read() (*Message, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var m Message
	if err := json.Unmarshal(r.sc.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("testrpc: parse: %w", err)
	}
	return &m, nil
}

// Writer serialises messages. Safe for concurrent use.
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	seq atomic.Uint64
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Notify sends a notification (no id, no response expected).
func (w *Writer) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return w.send(Message{JSONRPC: "2.0", Method: method, Params: raw})
}

// Request sends a request and returns the allocated id. The caller is
// responsible for matching the eventual response by id (the reader
// loop on the other side typically dispatches by id).
func (w *Writer) Request(method string, params any) (uint64, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return 0, err
	}
	id := w.seq.Add(1)
	return id, w.send(Message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
}

// Respond sends a response to a previously received request. Either
// result or rerr should be non-nil, not both.
func (w *Writer) Respond(id uint64, result any, rerr *RPCError) error {
	if result != nil && rerr != nil {
		return errors.New("testrpc: result and error are mutually exclusive")
	}
	msg := Message{JSONRPC: "2.0", ID: &id}
	if rerr != nil {
		msg.Error = rerr
	} else {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		msg.Result = raw
	}
	return w.send(msg)
}

func (w *Writer) send(m Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	enc, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(enc); err != nil {
		return err
	}
	_, err = w.w.Write([]byte{'\n'})
	return err
}
```

- [ ] **Step 4: Run the test, confirm green**

```bash
go test ./internal/testrpc/ -count=1 -v
```

Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/testrpc/
git commit -m "$(cat <<'EOF'
internal/testrpc: JSON-RPC 2.0 codec for the sngl test wire

Line-delimited JSON, one message per line. Requests carry an id;
notifications omit it. Shared by the sngl test driver and per-
language testagent runtimes.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Go testagent runtime

**Files:**
- Create: `pkg/go/testagent/testagent.go`
- Create: `pkg/go/testagent/testagent_test.go`

`testagent.T` mirrors `*testing.T`'s API so generated code is mode-agnostic at the method-call level. Under the hood, calls turn into JSON-RPC notifications on a stdout writer.

- [ ] **Step 1: Write failing tests first**

`pkg/go/testagent/testagent_test.go`:

```go
package testagent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

func TestT_LogEmitsNotification(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Log("hello")
	r := testrpc.NewReader(&buf)
	m, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m.Method != "log" {
		t.Errorf("method = %q", m.Method)
	}
	if !m.IsNotification() {
		t.Errorf("expected notification")
	}
	var args struct {
		Test string `json:"test"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(m.Params, &args)
	if args.Test != "myTest" || args.Msg != "hello" {
		t.Errorf("got %+v", args)
	}
}

func TestT_FailRecordsButContinues(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Fail()
	if !at.failed {
		t.Errorf("Fail did not mark failed")
	}
}

func TestT_FailNowAborts(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("FailNow should panic with sentinel")
		}
		if !at.failed {
			t.Errorf("FailNow did not mark failed before abort")
		}
	}()
	at.FailNow()
}

func TestT_ErrorfDecomposes(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Errorf("bad: %d", 7)
	if !at.failed {
		t.Error("Errorf must mark failed")
	}
	r := testrpc.NewReader(&buf)
	m, err := r.Read() // expect log first
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != "log" {
		t.Errorf("first message should be log, got %q", m.Method)
	}
	var args struct{ Msg string }
	_ = json.Unmarshal(m.Params, &args)
	if !strings.Contains(args.Msg, "bad: 7") {
		t.Errorf("log msg = %q", args.Msg)
	}
	m, _ = r.Read() // then markFail
	if m.Method != "markFail" {
		t.Errorf("second message = %q, want markFail", m.Method)
	}
}

func TestRunRegisteredTest_passes(t *testing.T) {
	resetRegistry()
	RegisterTest("ok", func(t *T) { t.Log("ran") })
	var stdout bytes.Buffer
	w := testrpc.NewWriter(&stdout)
	runOne(w, "ok")
	// expect: testStart, log, testEnd(pass)
	r := testrpc.NewReader(&stdout)
	methods := []string{}
	for {
		m, err := r.Read()
		if err != nil {
			break
		}
		methods = append(methods, m.Method)
	}
	got := strings.Join(methods, ",")
	if got != "testStart,log,testEnd" {
		t.Errorf("methods = %q", got)
	}
}

func TestRunRegisteredTest_failNowReportsFailure(t *testing.T) {
	resetRegistry()
	RegisterTest("bad", func(t *T) { t.Errorf("nope"); t.FailNow() })
	var stdout bytes.Buffer
	w := testrpc.NewWriter(&stdout)
	runOne(w, "bad")
	r := testrpc.NewReader(&stdout)
	var lastEnd struct {
		Test       string `json:"test"`
		Status     string `json:"status"`
		DurationMs int    `json:"durationMs"`
	}
	for {
		m, err := r.Read()
		if err != nil {
			break
		}
		if m.Method == "testEnd" {
			_ = json.Unmarshal(m.Params, &lastEnd)
		}
	}
	if lastEnd.Status != "fail" {
		t.Errorf("status = %q, want fail", lastEnd.Status)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./pkg/go/testagent/ -count=1
```

Expected: package doesn't exist.

- [ ] **Step 3: Implement the runtime**

Create `pkg/go/testagent/testagent.go`:

```go
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
```

- [ ] **Step 4: Run tests, confirm pass**

```bash
go test ./pkg/go/testagent/ -count=1 -v
```

Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add pkg/go/testagent/
git commit -m "$(cat <<'EOF'
pkg/go/testagent: Go testagent runtime

T type mirrors *testing.T's API so generated code is identical between
agent and native modes. Main() drives the JSON-RPC loop over stdin/
stdout. Abort uses panic-recover with a sentinel type.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `codegen.TestLauncher` interface

**Files:**
- Modify: `codegen/codegen.go`

Declare a single interface implementable by either platforms or languages. Mirrors `codegen.Builder`'s pattern.

- [ ] **Step 1: Add the interface declaration**

In `codegen/codegen.go`, after the existing `Builder` interface, add:

```go
// TestLauncher launches a compiled SNGL program in agent mode and
// returns an RPC channel for the sngl test driver to use. Implementable
// by either a PlatformGenerator (custom build/launch lifecycle — e.g.
// android APK + adb forward) or a LangTranslator (default lifecycle —
// compile a binary and spawn it with stdin/stdout RPC). The driver
// tries the platform first and falls back to the language; mirrors
// codegen.Builder's resolution.
type TestLauncher interface {
	// LaunchTest compiles the package, spawns the agent-mode binary, and
	// returns a connected RPCChannel plus a Cleanup func the caller must
	// invoke when done. dir is the codegen output directory (already
	// populated with sources + the linked testagent runtime).
	LaunchTest(ctx context.Context, dir string, lang LangTranslator, opts *ir.StructLit) (RPCChannel, Cleanup, error)
}

// RPCChannel is a full-duplex byte stream over which the driver and a
// running testagent exchange JSON-RPC messages.
type RPCChannel interface {
	io.ReadWriteCloser
}

// Cleanup runs after the driver finishes with the channel. Idempotent.
type Cleanup func()
```

Required imports: `context`, `io`. Add if absent.

- [ ] **Step 2: Confirm build clean**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add codegen/codegen.go
git commit -m "$(cat <<'EOF'
codegen: add TestLauncher interface

Same shape as codegen.Builder — implementable by either a platform or
a language. The driver resolves platform first, then language. Empty
diff to consumers; binds the interface for the upcoming Go launcher.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Go `TestLauncher`

**Files:**
- Create: `codegen/lang/golang/launcher.go`
- Create: `codegen/lang/golang/launcher_test.go`

The default lifecycle: synth `go.mod`, run `go mod tidy`, `go build`, spawn the binary with piped stdin/stdout, hand the pipes back as an `RPCChannel`. Cleanup terminates the process.

- [ ] **Step 1: Write the failing test**

`codegen/lang/golang/launcher_test.go`:

```go
package golang

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

func TestLaunchTest_endToEnd(t *testing.T) {
	if _, err := os.Stat("/usr/bin/go"); err != nil {
		// Approximate "go on PATH" — full check is exec.LookPath; this
		// keeps the test fast in CI.
		if _, err := os.Stat("/usr/local/go/bin/go"); err != nil {
			t.Skip("go toolchain not at standard locations")
		}
	}
	dir := t.TempDir()
	// A minimal SNGL-emitted program: a main that calls testagent.Main
	// and registers one test that logs and passes.
	mustWrite(t, filepath.Join(dir, "main.go"), `package main

import "git.duckfam.us/jonathan/sngl/pkg/go/testagent"

func init() {
	testagent.RegisterTest("ok", func(t *testagent.T) { t.Log("ran") })
}

func main() { testagent.Main() }
`)
	tr := &Translator{}
	ch, cleanup, err := tr.LaunchTest(context.Background(), dir, tr, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer cleanup()

	w := testrpc.NewWriter(ch)
	r := testrpc.NewReader(ch)

	// list
	id, err := w.Request("list", map[string]any{})
	if err != nil {
		t.Fatalf("request list: %v", err)
	}
	resp := readUntilID(t, r, id)
	var listRes struct{ Tests []string }
	_ = json.Unmarshal(resp.Result, &listRes)
	if len(listRes.Tests) != 1 || listRes.Tests[0] != "ok" {
		t.Errorf("list tests = %v", listRes.Tests)
	}

	// run all
	id, _ = w.Request("run", map[string]any{})
	// drain notifications until we see runComplete or the response
	var sawTestEnd bool
	for {
		m, err := r.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.IsResponse() && m.ID != nil && *m.ID == id {
			break
		}
		if m.Method == "testEnd" {
			var args struct{ Status string }
			_ = json.Unmarshal(m.Params, &args)
			if args.Status == "pass" {
				sawTestEnd = true
			}
		}
	}
	if !sawTestEnd {
		t.Errorf("did not observe passing testEnd")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readUntilID(t *testing.T, r *testrpc.Reader, id uint64) *testrpc.Message {
	t.Helper()
	for {
		m, err := r.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.IsResponse() && m.ID != nil && *m.ID == id {
			return m
		}
		_ = strings.TrimSpace // appease imports
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./codegen/lang/golang/ -run TestLaunchTest -count=1
```

Expected: undefined `(*Translator).LaunchTest`.

- [ ] **Step 3: Implement `LaunchTest`**

Create `codegen/lang/golang/launcher.go`:

```go
package golang

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LaunchTest implements codegen.TestLauncher for the default Go path:
// synth go.mod, go mod tidy, go build, exec the binary with stdin/
// stdout connected for JSON-RPC. Platforms whose lifecycle differs
// (e.g. windowed GUI threads, mobile install/launch) implement their
// own LaunchTest and override this fallback.
func (t *Translator) LaunchTest(ctx context.Context, dir string, _ codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, nil, fmt.Errorf("go not found in PATH")
	}

	if err := writeGoMod(dir); err != nil {
		return nil, nil, err
	}

	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	tidy := exec.CommandContext(ctx, goPath, "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stderr
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, nil, fmt.Errorf("go mod tidy: %w", err)
	}

	binPath := filepath.Join(dir, "testagent_bin")
	slog.Info("exec", "cmd", "go build", "dir", dir, "out", binPath)
	bld := exec.CommandContext(ctx, goPath, "build", "-o", binPath, ".")
	bld.Dir = dir
	bld.Stdout = os.Stderr
	bld.Stderr = os.Stderr
	if err := bld.Run(); err != nil {
		return nil, nil, fmt.Errorf("go build: %w", err)
	}

	cmd := exec.CommandContext(ctx, binPath)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}

	ch := &pipeChannel{in: stdout, out: stdin, cmd: cmd}
	cleanup := func() {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return ch, cleanup, nil
}

// writeGoMod synthesises go.mod for the temp build dir. Mirrors the
// existing build.go path so behaviour is consistent.
func writeGoMod(dir string) error {
	goVersion, goModExtra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
	if goModExtra != "" {
		mod += "\n" + goModExtra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
}

// pipeChannel adapts an exec.Cmd's stdout/stdin to RPCChannel.
type pipeChannel struct {
	in  io.ReadCloser
	out io.WriteCloser
	cmd *exec.Cmd
}

func (p *pipeChannel) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p *pipeChannel) Write(b []byte) (int, error) { return p.out.Write(b) }
func (p *pipeChannel) Close() error {
	_ = p.out.Close()
	return p.in.Close()
}
```

- [ ] **Step 4: Run the test**

```bash
go test ./codegen/lang/golang/ -run TestLaunchTest -count=1 -v
```

Expected: pass. (Test is slow — runs a real `go build`. ~5–10s.)

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/golang/launcher.go codegen/lang/golang/launcher_test.go
git commit -m "$(cat <<'EOF'
codegen/lang/golang: implement TestLauncher

Default launcher for any Go-emitting platform: synth go.mod, go mod
tidy, go build, spawn the binary, hand pipes back as RPCChannel.
Platforms with custom lifecycles override this fallback.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: testlower mode parameterisation

**Files:**
- Modify: `codegen/lang/golang/testlower.go`
- Modify: `codegen/lang/golang/testlower_test.go`

Goal: `LowerTestFunc` should remain semantically a *body* lowerer. Add `TestEmitMode` (`Native` | `Agent`) selecting the *wrapper* shape and the param type. The body content is identical between modes because `testing.T` and `testagent.T` share the same method set.

- [ ] **Step 1: Add the mode enum and a wrapper helper**

Edit `codegen/lang/golang/testlower.go`. Append after `LowerTestFunc`:

```go
// TestEmitMode selects how LowerTestFile wraps the per-test bodies.
type TestEmitMode int

const (
	// TestEmitNative produces *_test.go-style funcs taking *testing.T;
	// the resulting file is consumed by `go test`.
	TestEmitNative TestEmitMode = iota
	// TestEmitAgent produces test funcs taking *testagent.T plus an
	// init() that RegisterTests each one. The resulting file is
	// linked alongside main.go in the agent-mode binary.
	TestEmitAgent
)

// LowerTestFile produces the entire source of a generated test file.
// Each function in `fns` is rendered through LowerTestFunc (its body
// shape is identical across modes). The wrapper differs:
//
//   - Native: `package <pkg>` + import "testing" + funcs `func Test<X>(t *testing.T)`.
//   - Agent:  `package <pkg>` + import "git.duckfam.us/jonathan/sngl/pkg/go/testagent" + funcs `func test<X>(t *testagent.T)` + an init() that RegisterTests them.
//
// The body of each test func is byte-identical between modes because
// testagent.T mirrors *testing.T's API.
func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string, methodFields map[string]bool, mode TestEmitMode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	switch mode {
	case TestEmitNative:
		b.WriteString("import \"testing\"\n\n")
	case TestEmitAgent:
		b.WriteString("import \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\n")
	}

	for i, fn := range fns {
		suffix := suffixes[i]
		funcName, paramType := wrapperHeader(suffix, mode)
		fmt.Fprintf(&b, "func %s(t *%s) {\n", funcName, paramType)
		b.WriteString("\tc := newTestComponent()\n")
		b.WriteString("\t_ = c\n")
		scope := scopeFor(fn, methodFields)
		for _, s := range fn.Block {
			for _, line := range lowerTestStmt(s, scope) {
				fmt.Fprintf(&b, "\t%s\n", line)
			}
		}
		b.WriteString("}\n\n")
	}

	if mode == TestEmitAgent {
		b.WriteString("func init() {\n")
		for i := range fns {
			suffix := suffixes[i]
			fmt.Fprintf(&b, "\ttestagent.RegisterTest(%q, test%s)\n", suffix, suffix)
		}
		b.WriteString("}\n")
	}

	return b.String()
}

func wrapperHeader(suffix string, mode TestEmitMode) (funcName, paramType string) {
	switch mode {
	case TestEmitNative:
		return "Test" + suffix, "testing.T"
	case TestEmitAgent:
		return "test" + suffix, "testagent.T"
	}
	return "Test" + suffix, "testing.T"
}

// scopeFor mirrors LowerTestFunc's scope construction so LowerTestFile
// shares identical state shape.
func scopeFor(fn *ir.Func, methodFields map[string]bool) *codegen.ExprScope {
	scope := &codegen.ExprScope{
		LocalVars:      map[string]bool{},
		RawFieldAccess: map[string]bool{},
		MethodFields:   methodFields,
	}
	for _, p := range fn.Params {
		scope.LocalVars[p.Name] = true
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			scope.RawFieldAccess[p.Name] = true
		}
	}
	return scope
}
```

`LowerTestFunc` stays for now — callers (bubbletea/fyne/gtk4/android `runtests.go`) still depend on its exact shape. Tasks 9 & 10 cut those over to `LowerTestFile`; once nothing else calls `LowerTestFunc`, it can be removed.

- [ ] **Step 2: Add tests for the new wrapper**

Append to `codegen/lang/golang/testlower_test.go`:

```go
func TestLowerTestFile_agentModeEmitsRegisterInit(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no test func in package")
	}
	out := LowerTestFile("ui", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitAgent)
	if !strings.Contains(out, "import \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"") {
		t.Errorf("agent mode missing testagent import; got:\n%s", out)
	}
	if !strings.Contains(out, "func testFoo(t *testagent.T)") {
		t.Errorf("agent func signature missing; got:\n%s", out)
	}
	if !strings.Contains(out, "testagent.RegisterTest(\"Foo\", testFoo)") {
		t.Errorf("agent mode missing RegisterTest init; got:\n%s", out)
	}
}

func TestLowerTestFile_nativeModeEmitsTestingImport(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	out := LowerTestFile("ui", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitNative)
	if !strings.Contains(out, "import \"testing\"") {
		t.Errorf("native mode missing testing import; got:\n%s", out)
	}
	if !strings.Contains(out, "func TestFoo(t *testing.T)") {
		t.Errorf("native func signature missing; got:\n%s", out)
	}
	if strings.Contains(out, "RegisterTest") {
		t.Errorf("native mode should not RegisterTest; got:\n%s", out)
	}
}
```

- [ ] **Step 3: Run the tests**

```bash
go test ./codegen/lang/golang/ -run TestLowerTestFile -count=1 -v
```

Expected: both pass.

- [ ] **Step 4: Confirm nothing else regressed**

```bash
go test ./codegen/lang/golang/ -count=1 2>&1 | tail -5
go build ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/golang/testlower.go codegen/lang/golang/testlower_test.go
git commit -m "$(cat <<'EOF'
codegen/lang/golang: add LowerTestFile with native/agent modes

Native mode emits *_test.go shape (import testing, *testing.T params).
Agent mode emits agent-runtime entries (import testagent, *testagent.T
params, RegisterTest init). Body lowering is identical across modes
since testagent.T mirrors *testing.T's API. LowerTestFunc remains for
existing callers; tasks 9-10 migrate them.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: snapshot golden machinery (text/ANSI)

**Files:**
- Create: `codegen/testharness/snapshot/snapshot.go`
- Create: `codegen/testharness/snapshot/snapshot_test.go`

A small package the driver uses to diff incoming snapshot bytes against goldens, with mime-driven storage extension and diff strategy. Text + ANSI now; PNG arrives with the fyne fixture work but the package's mime-dispatch is open from day one.

- [ ] **Step 1: Write the failing test**

`codegen/testharness/snapshot/snapshot_test.go`:

```go
package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_textWritesGoldenOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Update: true}
	res, err := s.Assert("myFix", "view", "text/plain", []byte("hello"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if !res.Pass {
		t.Error("update mode should always pass")
	}
	want := filepath.Join(dir, "myFix.snapshots", "view.txt")
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("golden = %q", b)
	}
}

func TestStore_textPassesOnMatch(t *testing.T) {
	dir := t.TempDir()
	gd := filepath.Join(dir, "fix.snapshots")
	_ = os.MkdirAll(gd, 0o755)
	_ = os.WriteFile(filepath.Join(gd, "v.txt"), []byte("same"), 0o644)
	s := &Store{Dir: dir}
	res, err := s.Assert("fix", "v", "text/plain", []byte("same"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if !res.Pass {
		t.Errorf("expected pass, got fail: %s", res.Diff)
	}
}

func TestStore_textFailsOnMismatchWithDiff(t *testing.T) {
	dir := t.TempDir()
	gd := filepath.Join(dir, "fix.snapshots")
	_ = os.MkdirAll(gd, 0o755)
	_ = os.WriteFile(filepath.Join(gd, "v.txt"), []byte("alpha"), 0o644)
	s := &Store{Dir: dir}
	res, err := s.Assert("fix", "v", "text/plain", []byte("beta"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if res.Pass {
		t.Error("expected fail on text mismatch")
	}
	if res.Diff == "" {
		t.Error("expected diff string on fail")
	}
}

func TestStore_unknownMimeReturnsError(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Update: true}
	if _, err := s.Assert("f", "v", "application/x-weird", []byte{0}); err == nil {
		t.Error("expected error on unknown mime")
	}
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./codegen/testharness/snapshot/ -count=1
```

Expected: package doesn't exist.

- [ ] **Step 3: Implement the store**

Create `codegen/testharness/snapshot/snapshot.go`:

```go
// Package snapshot owns the golden-file machinery used by the sngl
// test driver. It stores rendered snapshots from agents and diffs
// against committed goldens. The store is mime-driven: extension and
// diff strategy come from the mime type the agent reported.
package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store persists and diffs per-fixture snapshots. Dir is the directory
// containing the fixture file; each fixture <name>.sngl owns a sibling
// <name>.snapshots/ directory of goldens.
type Store struct {
	Dir    string
	Update bool // when true, every Assert overwrites the golden and passes.
}

// Result reports the outcome of a single Assert call.
type Result struct {
	Pass bool
	Diff string
}

// Assert reads or creates the golden for (fixture, name, mime), compares
// against actual bytes, and returns Pass/Diff. Returns an error only on
// I/O or unsupported mime.
func (s *Store) Assert(fixture, name, mime string, actual []byte) (Result, error) {
	ext, ok := mimeExt(mime)
	if !ok {
		return Result{}, fmt.Errorf("snapshot: unsupported mime %q", mime)
	}
	goldenDir := filepath.Join(s.Dir, fixture+".snapshots")
	goldenPath := filepath.Join(goldenDir, name+ext)

	if s.Update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
			return Result{}, err
		}
		return Result{Pass: true}, nil
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Pass: false, Diff: "no golden — run with SNGL_UPDATE_SNAPSHOTS=1"}, nil
		}
		return Result{}, err
	}
	if bytes.Equal(golden, actual) {
		return Result{Pass: true}, nil
	}
	return Result{Pass: false, Diff: textDiff(golden, actual)}, nil
}

// mimeExt maps mime to file extension; ok=false for unsupported mimes.
func mimeExt(mime string) (string, bool) {
	switch mime {
	case "text/plain":
		return ".txt", true
	case "text/ansi":
		return ".ansi", true
	case "application/json":
		return ".json", true
	case "image/png":
		return ".png", true
	}
	return "", false
}

// textDiff returns a small unified-style diff. We avoid pulling a full
// diff library in for this minimal need; a future caller can swap in
// internal/diff if richer output is needed.
func textDiff(want, got []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- want\n+++ got\n")
	w := bytes.Split(want, []byte("\n"))
	g := bytes.Split(got, []byte("\n"))
	max := len(w)
	if len(g) > max {
		max = len(g)
	}
	for i := 0; i < max; i++ {
		var ww, gg []byte
		if i < len(w) {
			ww = w[i]
		}
		if i < len(g) {
			gg = g[i]
		}
		if bytes.Equal(ww, gg) {
			fmt.Fprintf(&b, " %s\n", ww)
		} else {
			fmt.Fprintf(&b, "-%s\n+%s\n", ww, gg)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run, confirm green**

```bash
go test ./codegen/testharness/snapshot/ -count=1 -v
```

Expected: 4/4 pass.

- [ ] **Step 5: Commit**

```bash
git add codegen/testharness/snapshot/
git commit -m "$(cat <<'EOF'
codegen/testharness/snapshot: golden-file machinery

Mime-driven extension and diff strategy. text/plain, text/ansi,
application/json, image/png recognised. PNG diff will arrive with
internal/imgdiff integration; for now image/png is accepted shape-wise
and stored verbatim.

SNGL_UPDATE_SNAPSHOTS=1 puts the store in update mode (always pass,
overwrite golden).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Driver — refactor `cmd/sngl/test.go` onto TestLauncher

**Files:**
- Modify: `cmd/sngl/test.go`
- Create: `cmd/sngl/testdriver.go`

`runOnPlatform` currently calls `runner.RunTests(pkg, lang, opts)`. The new shape: resolve `TestLauncher` (platform first, language fallback), generate sources + linked testagent main into a tmpdir, call `LaunchTest`, drive the RPC, aggregate notifications into `codegen.TestResult` rows.

The `none` platform (interp) is the exception: it has no compiled binary. Keep its existing `TestRunner.RunTests` path as a special-case branch — Task is named for it later in plan 2. This task does NOT touch the interpreter path.

- [ ] **Step 1: Add `cmd/sngl/testdriver.go`**

```go
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/testharness/snapshot"
	"git.duckfam.us/jonathan/sngl/internal/testrpc"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resolveLauncher returns the TestLauncher for a target. Platform takes
// precedence; language is the fallback. Returns nil if neither
// implements it.
func resolveLauncher(plat codegen.PlatformGenerator, lang codegen.LangTranslator) codegen.TestLauncher {
	if l, ok := plat.(codegen.TestLauncher); ok {
		return l
	}
	if l, ok := lang.(codegen.TestLauncher); ok {
		return l
	}
	return nil
}

// runViaLauncher generates the target's sources + testagent main into a
// tmpdir, invokes Launch, and drives the RPC stream until runComplete
// or the agent closes. Returns one TestResult per testEnd notification.
func runViaLauncher(ctx context.Context, plat codegen.PlatformGenerator, lang codegen.LangTranslator, pkg *ir.Package, opts *ir.StructLit, fixtureDir string) ([]*codegen.TestResult, error) {
	launcher := resolveLauncher(plat, lang)
	if launcher == nil {
		return nil, fmt.Errorf("platform %q with language %q has no TestLauncher", plat.PlatformIdentifier(), lang.Identifier())
	}

	tmpDir, err := os.MkdirTemp("", "sngl-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(tmpDir)
	}

	// Codegen runs in agent mode: it must emit testagent_main.go alongside
	// the regular sources. The req.Options carries "test": true to signal
	// the language emitter to include test files.
	if opts == nil {
		opts = &ir.StructLit{}
	}
	codegen.SetOptionField(opts, "test", true)
	codegen.SetOptionField(opts, "testMode", "agent")
	req := &codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: opts,
		Source:  "",
	}
	if err := plat.Generate(req, codegen.NewDirSink(tmpDir)); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	ch, cleanup, err := launcher.LaunchTest(ctx, tmpDir, lang, opts)
	if err != nil {
		return nil, fmt.Errorf("launch: %w", err)
	}
	defer cleanup()

	return driveRPC(ch, fixtureDir)
}

// driveRPC sends list+run, then collects testStart/log/testEnd/markFail
// notifications into TestResults. snapshotAssert requests are answered
// via the snapshot.Store backed by fixtureDir.
func driveRPC(ch codegen.RPCChannel, fixtureDir string) ([]*codegen.TestResult, error) {
	r := testrpc.NewReader(ch)
	w := testrpc.NewWriter(ch)
	store := &snapshot.Store{Dir: fixtureDir, Update: os.Getenv("SNGL_UPDATE_SNAPSHOTS") == "1"}

	// Request the run.
	runID, err := w.Request("run", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("request run: %w", err)
	}

	current := map[string]*codegen.TestResult{}
	starts := map[string]time.Time{}
	var results []*codegen.TestResult

	for {
		m, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return results, err
		}
		if m.IsResponse() && m.ID != nil && *m.ID == runID {
			break
		}
		switch m.Method {
		case "testStart":
			var p struct{ Test string }
			_ = json.Unmarshal(m.Params, &p)
			current[p.Test] = &codegen.TestResult{Desc: p.Test, Passed: true}
			starts[p.Test] = time.Now()
		case "log":
			var p struct{ Test, Msg string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Log = append(cur.Log, p.Msg)
			}
		case "markFail":
			var p struct{ Test string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Passed = false
				cur.Failures = append(cur.Failures, codegen.TestFailure{Message: "fail recorded"})
			}
		case "markSkip":
			var p struct{ Test, Reason string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Log = append(cur.Log, "SKIP: "+p.Reason)
			}
		case "testEnd":
			var p struct {
				Test       string
				Status     string
				DurationMs int
			}
			_ = json.Unmarshal(m.Params, &p)
			cur := current[p.Test]
			if cur == nil {
				cur = &codegen.TestResult{Desc: p.Test}
			}
			cur.Passed = p.Status == "pass"
			cur.Duration = time.Duration(p.DurationMs) * time.Millisecond
			results = append(results, cur)
			delete(current, p.Test)
		case "":
			// Could be a snapshotAssert request. Distinguish by ID
			// presence + method on the *params* path — but the codec
			// classifies these as request because Method is non-empty.
			// snapshotAssert falls through here only if Method was
			// unexpected; treat as no-op.
		default:
			// Inbound request from the agent (e.g. snapshotAssert).
			if m.ID != nil && m.Method == "snapshotAssert" {
				handleSnapshotAssert(w, store, m, filepath.Base(fixtureDir))
			}
		}
	}
	return results, nil
}

func handleSnapshotAssert(w *testrpc.Writer, store *snapshot.Store, m *testrpc.Message, fixtureBase string) {
	var p struct {
		Test  string `json:"test"`
		Name  string `json:"name"`
		Mime  string `json:"mime"`
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(p.Bytes)
	if err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32602, Message: "bad base64"})
		return
	}
	res, err := store.Assert(fixtureBase, p.Name, p.Mime, raw)
	if err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32603, Message: err.Error()})
		return
	}
	_ = w.Respond(*m.ID, map[string]any{"pass": res.Pass, "diff": res.Diff}, nil)
}
```

- [ ] **Step 2: Switch `cmd/sngl/test.go` to use the new path**

Inside `runOnPlatform` in `cmd/sngl/test.go`, find the call to `safeRunTests`. The new flow:

1. If the resolved platform/language pair has a `TestLauncher`, use `runViaLauncher`.
2. Otherwise fall back to the existing `runner.RunTests` path (this keeps `none`/interp working).

Edit `runOnPlatform` (and rename helper if useful). At the call site of `safeRunTests`:

```go
			var results []*codegen.TestResult
			plat := codegen.LookupPlatform(plat.PlatformIdentifier()) // alias; keep existing variable
			if resolveLauncher(plat, lang) != nil {
				results, err = runViaLauncher(cmd.Context(), plat, lang, pkg, opts, filepath.Dir(filename))
			} else {
				results, err = safeRunTests(runner, pkg, lang, opts)
			}
			if err != nil { ... existing handling ... }
```

The exact stitch is local; the principle is: pick `runViaLauncher` when a TestLauncher is available, fall through to `safeRunTests` otherwise.

If `cmd.Context()` is not yet available, pass `context.Background()`.

- [ ] **Step 3: Confirm build clean**

```bash
go build ./...
```

Expected: clean. Tests in this task can't fully run end-to-end until Task 8 wires bubbletea — but the build proves the wiring compiles.

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/test.go cmd/sngl/testdriver.go
git commit -m "$(cat <<'EOF'
cmd/sngl: drive TestLauncher path when a launcher is registered

When a target's platform or language implements codegen.TestLauncher,
sngl test generates into a tmpdir, launches the binary, and drives the
JSON-RPC stream into TestResults. Snapshots are diffed via the new
snapshot.Store. The legacy TestRunner path stays as fallback so 'none'
(interp) keeps working.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: bubbletea cutover

**Files:**
- Delete: `codegen/platform/bubbletea/runtests.go`
- Modify: `codegen/platform/bubbletea/bubbletea.go` (or `compiler_ir.go`) — emit `testagent_main.go` when `Options.test == true` and `testMode == "agent"`.

The Go testagent inherits from `codegen.lang.golang`'s LaunchTest (Task 4) — bubbletea itself implements nothing extra. The single change is in codegen: produce a testagent_main.go alongside model.go that imports bubbletea's Model and registers each user test through `LowerTestFile` agent mode.

- [ ] **Step 1: Locate the existing emit path**

```bash
grep -n 'func .*Generator. Generate\|func .*compilation. Compile' codegen/platform/bubbletea/*.go | head
```

You're looking for the function that's invoked during `plat.Generate(req, sink)`. It builds a `Model` struct and emits `model.go`. Read it carefully so the test-file emission lands in the same place and follows the same scaffolding patterns.

- [ ] **Step 2: Add the test-file emission**

Inside bubbletea's compile/Generate path, after the regular model.go is written, append:

```go
if optionBoolFromOpts(req.Options, "test") {
	testFns, suffixes, methodFields := collectTestFuncs(req.Pkg)
	if len(testFns) > 0 {
		mode := golang.TestEmitNative
		if optionStringFromOpts(req.Options, "testMode") == "agent" {
			mode = golang.TestEmitAgent
		}
		src := golang.LowerTestFile("ui", testFns, suffixes, methodFields, mode)
		fname := "testagent_main.go"
		if mode == golang.TestEmitNative {
			fname = "model_test.go"
		}
		if err := sink.Write(fname, []byte(src)); err != nil {
			return err
		}
		// Agent mode also needs a main() entry point that calls testagent.Main.
		if mode == golang.TestEmitAgent {
			mainSrc := []byte(`package ui

import "git.duckfam.us/jonathan/sngl/pkg/go/testagent"

func main() { testagent.Main() }
`)
			if err := sink.Write("agent_main.go", mainSrc); err != nil {
				return err
			}
		}
	}
}
```

Implement `optionBoolFromOpts`, `optionStringFromOpts`, and `collectTestFuncs` as small helpers next to the call site (or in `codegen/codegen.go` if a shared home makes sense — match existing helper style):

```go
func optionBoolFromOpts(opts *ir.StructLit, name string) bool {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return false
	}
	lit, _ := v.(*ir.Literal)
	return lit != nil && lit.Raw == "true"
}

func optionStringFromOpts(opts *ir.StructLit, name string) string {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return ""
	}
	lit, _ := v.(*ir.Literal)
	if lit == nil {
		return ""
	}
	raw := lit.Raw
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return raw
}

func collectTestFuncs(pkg *ir.Package) (fns []*ir.Func, suffixes []string, methodFields map[string]bool) {
	methodFields = map[string]bool{}
	for _, comp := range pkg.Components {
		for _, f := range comp.Funcs {
			methodFields[f.Name] = true
		}
	}
	for _, f := range pkg.Funcs {
		if codegen.IsComputed(f) {
			methodFields[f.Name] = true
		}
		if f.IsTest {
			fns = append(fns, f)
			suffixes = append(suffixes, strings.TrimPrefix(f.Name, "test"))
		}
	}
	return
}
```

- [ ] **Step 3: Delete `codegen/platform/bubbletea/runtests.go`**

```bash
git rm codegen/platform/bubbletea/runtests.go
```

The interface `TestRunner` is now unused by bubbletea; `cmd/sngl/test.go`'s resolveLauncher path will catch this platform via Go's lang TestLauncher.

- [ ] **Step 4: Smoke test**

Pick an existing testdata fixture that targets bubbletea — e.g. one referenced by a `cmd/sngl/testdata/generate_bubbletea_*.txt`. From the project root:

```bash
sngl test --platform=bubbletea testdata/test_counter.sngl 2>&1 | tail -20
```

If no `test_counter.sngl` exists, find any `testdata/*.sngl` with a `func test...(t Test, c counter)` body. Expected: PASS (or specific failure rows from the new RPC-driven pipeline, identical to before).

- [ ] **Step 5: Confirm full test suite**

```bash
go test ./... -count=1 2>&1 | tail -30
```

Expected: green. Any failure here is likely a stale reference to bubbletea's old `RunTests` in tests — fix by removing the call or by checking for `TestLauncher` instead.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
codegen/platform/bubbletea: cut over to TestLauncher path

Delete bubbletea/runtests.go. Codegen now emits testagent_main.go and
agent_main.go when --opt test=true with testMode=agent; sngl test
launches the resulting binary via the Go lang's TestLauncher and
collects results over JSON-RPC.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: fyne cutover

**Files:**
- Delete: `codegen/platform/fyne/runtests.go`
- Modify: fyne's Generate path — same emission as bubbletea.

Identical shape to Task 8. The fyne Generator's emit path already mirrors bubbletea's; share helpers where natural (move `optionBoolFromOpts` etc. into a common file under `codegen/` if both bubbletea and fyne reach for them).

- [ ] **Step 1: Move shared helpers if duplicated**

If Task 8 placed `optionBoolFromOpts`/`optionStringFromOpts`/`collectTestFuncs` inside `codegen/platform/bubbletea/`, move them now to `codegen/codegen.go` (or a new `codegen/test_emit.go`) so fyne can call them too. Update bubbletea's imports.

- [ ] **Step 2: Mirror the test-file emission in fyne**

Add the same block to fyne's Generate path that bubbletea grew in Task 8. The package name in the emitted file may differ (`"ui"` or whatever fyne uses today — match its existing package name; look at one of fyne's emitted files to confirm).

- [ ] **Step 3: Delete `codegen/platform/fyne/runtests.go`**

```bash
git rm codegen/platform/fyne/runtests.go
```

- [ ] **Step 4: Smoke test**

```bash
sngl test --platform=fyne <path-to-any-fyne-fixture> 2>&1 | tail -20
```

Expected: PASS or platform-specific failure (fyne builds need CGo + display libs; if the host lacks them, document the skip locally — the `none` platform run is the cross-platform smoke).

- [ ] **Step 5: Full suite**

```bash
go test ./... -count=1 2>&1 | tail -30
```

Expected: green.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
codegen/platform/fyne: cut over to TestLauncher path

Delete fyne/runtests.go. Same shape as bubbletea — Go testagent
inherited from lang.golang.LaunchTest.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Wire `--opt test=true` in `cmd/sngl/pipeline.go`

**Files:**
- Modify: `cmd/sngl/pipeline.go`

Today the option is accepted but does nothing. After Tasks 8/9, codegen knows how to emit native test files when `Options.test==true && testMode!="agent"`. The pipeline just needs to ensure this happens during `sngl generate`.

- [ ] **Step 1: Confirm the codegen path runs when `Options.test==true`**

Read `cmd/sngl/pipeline.go`'s per-target loop. The current code passes `target.Options` directly to `generateTarget`. If `Options.test` is set, the bubbletea/fyne Generate paths from Tasks 8/9 will see it and emit `model_test.go` (native mode) into `--out`. So in principle nothing more is required here — but verify by reading the code path.

- [ ] **Step 2: Add a fixture asserting native-mode emission**

`cmd/sngl/testdata/generate_test_opt_emits.txt`:

```
# --opt test=true with a Go-emitting platform produces a *_test.go file
# alongside the regular source.

[!exec:go] skip 'go toolchain required for go.mod resolution in checker'

sngl generate --lang=go --platform=bubbletea --out=out --opt test=true app.sngl
exists out/model.go
exists out/model_test.go

-- app.sngl --
output { go { bubbletea } }

component main {
    var count = 0
    button(text="+", @click { count += 1 })
    text(value="x")
}

func testInitialCountIsZero(t Test, c main) {
    t.assert(c.count == 0)
}
```

- [ ] **Step 3: Run the fixture**

```bash
go test ./cmd/sngl/ -run TestScript/generate_test_opt_emits -count=1 -v
```

If it fails because no test func exists in the package, fix the fixture (the example above includes one). If it fails because emission isn't triggered, check that bubbletea's Generate is reading `Options.test` correctly.

- [ ] **Step 4: Confirm full suite**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```

Expected: green.

- [ ] **Step 5: Commit**

```bash
git add cmd/sngl/testdata/generate_test_opt_emits.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: assert --opt test=true emits *_test.go for bubbletea

End-to-end: sngl generate --opt test=true on a bubbletea/go target
produces both model.go and model_test.go in --out, ready for the
user's go test to consume.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: End-to-end fixture + sweep

**Files:**
- Create: `cmd/sngl/testdata/test_bubbletea_end_to_end.txt`

A txtar script that runs `sngl test --platform=bubbletea` against a fixture and asserts a pass count via the new JSON output mode.

- [ ] **Step 1: Add the fixture**

```
# sngl test drives the testagent over JSON-RPC and reports PASS.

[!exec:go] skip 'go toolchain required'

sngl test --platform=bubbletea app.sngl
stdout 'PASS'

-- app.sngl --
output { go { bubbletea } }

component counter {
    var count = 0
    button(text="+", @click { count += 1 })
    text(value=string(count))
}

func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}

func testIncrementWorks(t Test, c counter) {
    c.count = 5
    t.assert(c.count == 5)
}
```

- [ ] **Step 2: Run it**

```bash
go test ./cmd/sngl/ -run TestScript/test_bubbletea_end_to_end -count=1 -v 2>&1 | tail -20
```

Expected: green. If it isn't, walk the failure: it likely traces back to either the agent's run loop, the driver's notification handling, or the codegen emission. The reproducible repro is `SNGL_KEEP_TEST_DIR=1 sngl test --platform=bubbletea <fixture>` then inspecting the temp dir's generated files manually.

- [ ] **Step 3: Final cross-build**

```bash
go tool verify 2>&1 | tail -40
```

Expected: clean, full suite green.

- [ ] **Step 4: Grep for leftover references to the deleted runners**

```bash
grep -rn 'runtests.go\|RunTests\b' codegen/platform/bubbletea/ codegen/platform/fyne/ 2>/dev/null | grep -v _test
```

Expected: empty.

- [ ] **Step 5: Commit any final cleanup**

```bash
git add -A
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: end-to-end sngl test fixture against bubbletea

Drives the full Go vertical slice — agent build, RPC stream, test
discovery, pass count.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

(Skip the commit if `git diff` is empty.)

---

## Self-review notes (for the implementer)

- **Solo repo; work on `main` directly.** No worktree.
- **`testagent.T` mirrors `*testing.T`.** Keep methods byte-for-byte compatible with the subset of testing.T that `LowerTestFunc`'s output uses. If a future intrinsic needs something testing.T doesn't have, add it to testagent.T and leave native mode emitting the decomposed form via testing.T's primitives.
- **`Options.test` and `Options.testMode`.** The first is the user-facing schema field added by the CLI plan; the second is a codegen-internal hint set by `runViaLauncher` so platforms can branch between native and agent emission. Don't expose `testMode` on the CLI — it's not in `lib/options.sngl`.
- **The `none` platform stays on the old path.** It has no compiled binary; the interp drives intrinsics by direct call. Plan 2 handles its adapter onto the unified shape.
- **Snapshot/PNG support.** Wired through the wire protocol and the store. Actual PNG snapshots only fire from fyne (and android in later plans); for now the bubbletea path produces text/ansi snapshots.
- **`runtests.go` deletion order.** Bubbletea first (Task 8), then fyne (Task 9). Each cutover is independently shippable; the suite stays green at each step.
- **TestProber / probe behaviour.** Currently a couple of platforms implement `TestProber` to report "host lacks toolchain." The new path doesn't have an equivalent — the launcher reports the error at LaunchTest time. `sngl test --platform=all` should treat a launcher's "tool not found" return as a skip, mirroring today's prober skip. Wire this into `runTestAll` as part of Task 7 or Task 11; the simplest version detects `errors.Is(err, exec.ErrNotFound)` and prints SKIP rather than FAIL.
