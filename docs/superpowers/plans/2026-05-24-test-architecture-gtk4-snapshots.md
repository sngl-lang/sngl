# Test architecture — gtk4 cutover + snapshot backfill — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete GUI-platform test coverage: cut over gtk4 from its legacy `runtests.go` to the IR-native `TestLauncher` path established in Plan 1, and make `t.snapshot(name)` produce real rendered bytes on every Go-emitting GUI platform (bubbletea, fyne, gtk4).

**Architecture:** Codegen emits a per-platform `snapshotBytes(m *Model) (mime string, data []byte, err error)` function alongside the testagent main. The platform's `agent_main.go` registers it via `testagent.RegisterSnapshot` during `init`. When a test calls `t.snapshot(name)`, the testagent runtime invokes the registered fn, base64-encodes the bytes, and submits a `snapshotAssert` RPC request the driver answers via the existing `codegen/testharness/snapshot.Store`.

**Tech Stack:** Go, `pkg/go/testagent`, `codegen/platform/{bubbletea,fyne,gtk4}`, `codegen/testharness/snapshot`, `internal/testrpc`. Gtk4 path uses cgo + `pkg-config: gtk4` (already a dependency for non-test gtk4 builds).

**Spec ref:** `docs/superpowers/specs/2026-05-24-test-architecture-gtk4-snapshots-design.md`

**No worktree** — per project memory, work directly on `main`.

---

## File map

- Modify `pkg/go/testagent/testagent.go` — add `RegisterSnapshot`, `(*T).Snapshot`, RPC request roundtrip.
- Modify `pkg/go/testagent/testagent_test.go` — unit tests for the new intrinsic.
- Modify `codegen/platform/bubbletea/bubbletea.go` — emit `snapshotBytes` + `RegisterSnapshot` call in `init` under agent mode; same under native mode (--opt test=true) for the user's project tests.
- Modify `codegen/platform/fyne/fyne.go` — same.
- Modify `codegen/platform/gtk4/gtk4.go` (or `compiler_ir.go`) — agent-mode emission block + `snapshotBytes` + cgo preamble + activate callback shim.
- Delete `codegen/platform/gtk4/runtests.go` and `codegen/platform/gtk4/runtests_js.go`.
- Create `cmd/sngl/testdata/test_bubbletea_snapshot.txt` — golden + diff fixture.
- Create `cmd/sngl/testdata/test_fyne_snapshot.txt` — same.
- Create `cmd/sngl/testdata/test_gtk4_snapshot.txt` — same; skips when gtk4 libs missing.
- Modify `codegen/lang/golang/launcher.go` — surface a clean skip signal when probing for missing platform libs (gtk4 specifically).

---

## Sequencing

Each task ends green; the suite stays shippable.

1. **Task 1**: `testagent.RegisterSnapshot` + `(*T).Snapshot` + RPC roundtrip. No platform impl yet; intrinsic returns "no capture registered" if called without a register.
2. **Task 2**: Bubbletea `snapshotBytes` (calls `m.View()`) + fixture.
3. **Task 3**: Fyne `snapshotBytes` (offscreen test app → PNG) + fixture.
4. **Task 4**: Gtk4 cutover — delete `runtests.go`, emit `testagent_main.go` + `agent_main.go`. No snapshot yet; gtk4 state-only tests pass.
5. **Task 5**: Gtk4 `snapshotBytes` (inline cgo harness) + fixture.
6. **Task 6**: Gtk4 probe — clean skip when `pkg-config gtk4` fails or `$DISPLAY`/Wayland not present.

---

### Task 1: testagent snapshot intrinsic + RPC roundtrip

**Files:**
- Modify: `pkg/go/testagent/testagent.go`
- Modify: `pkg/go/testagent/testagent_test.go`

The driver already understands `snapshotAssert` (Plan 1 — see `cmd/sngl/testdriver.go` `handleSnapshotAssert`). Task 1 adds the agent end of that exchange.

- [ ] **Step 1: Write failing test**

Append to `pkg/go/testagent/testagent_test.go`:

```go
func TestT_SnapshotErrorsWhenNoCaptureRegistered(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Snapshot("first")
	if !at.failed {
		t.Error("Snapshot with no capture should fail the test")
	}
}

func TestT_SnapshotSubmitsRequest(t *testing.T) {
	resetRegistry()
	resetSnapshot()
	RegisterSnapshot(func() (string, []byte, error) {
		return "text/plain", []byte("hello"), nil
	})
	// Roundtrip needs a mock driver that responds to snapshotAssert.
	// Use a piped RPC pair to simulate.
	cr, dw := io.Pipe()  // driver → agent (responses)
	ar, dwOut := io.Pipe() // agent → driver (requests)
	_ = dw
	// Drive agent in goroutine
	at := &T{name: "T1", w: testrpc.NewWriter(dwOut), rpcResponses: make(chan *testrpc.Message, 4)}
	startReadLoop(at, cr)

	go func() {
		// Simulate driver: read one request, send back pass.
		r := testrpc.NewReader(ar)
		msg, _ := r.Read()
		if msg.Method != "snapshotAssert" {
			t.Errorf("driver got method = %q, want snapshotAssert", msg.Method)
		}
		// reply pass
		w := testrpc.NewWriter(dw)
		_ = w.Respond(*msg.ID, map[string]any{"pass": true}, nil)
	}()

	at.Snapshot("first")
	if at.failed {
		t.Errorf("expected pass, got fail")
	}
}
```

If the existing test file doesn't import `io`, add it. The two tests verify (a) the "no capture" failure path and (b) a full request-response roundtrip.

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./pkg/go/testagent/ -count=1
```
Expected: undefined `RegisterSnapshot`, `resetSnapshot`, `Snapshot`, `rpcResponses`, `startReadLoop`.

- [ ] **Step 3: Implement**

Edit `pkg/go/testagent/testagent.go`. The current `T` only writes notifications; it never reads anything back. Snapshots need a bidirectional channel — the agent writes a request and waits for the driver's response on the same stream.

The existing agent already runs the read loop in `Main()` (driver→agent for `list`/`run`/`cancel` commands). We extend that read loop to also dispatch *responses* matching outstanding requests, by id.

Add the following to `pkg/go/testagent/testagent.go`. First, an outstanding-requests table at package level:

```go
var (
	pendingMu sync.Mutex
	pending   = map[uint64]chan *testrpc.Message{}
)

// awaitResponse parks the caller until the driver replies to id, or the
// connection closes. Times out after 30s.
func awaitResponse(id uint64) (*testrpc.Message, error) {
	ch := make(chan *testrpc.Message, 1)
	pendingMu.Lock()
	pending[id] = ch
	pendingMu.Unlock()
	defer func() {
		pendingMu.Lock()
		delete(pending, id)
		pendingMu.Unlock()
	}()
	select {
	case m := <-ch:
		return m, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("snapshotAssert timeout")
	}
}

// deliverResponse is called by the read loop when it sees a response
// message. Routes by id to the channel awaitResponse parked on.
func deliverResponse(m *testrpc.Message) {
	if m.ID == nil {
		return
	}
	pendingMu.Lock()
	ch := pending[*m.ID]
	pendingMu.Unlock()
	if ch != nil {
		ch <- m
	}
}
```

Then the snapshot registry + `(*T).Snapshot`:

```go
var snapshotFn func() (string, []byte, error)

// RegisterSnapshot registers the per-platform capture function. The
// emitted agent_main.go calls this during init.
func RegisterSnapshot(fn func() (string, []byte, error)) {
	snapshotFn = fn
}

func resetSnapshot() { snapshotFn = nil } // for tests

func (t *T) Snapshot(name string) {
	if snapshotFn == nil {
		t.Errorf("snapshot %q: no capture registered for this platform", name)
		return
	}
	mime, raw, err := snapshotFn()
	if err != nil {
		t.Errorf("snapshot %q: capture: %v", name, err)
		return
	}
	id, err := t.w.Request("snapshotAssert", map[string]any{
		"test":  t.name,
		"name":  name,
		"mime":  mime,
		"bytes": base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		t.Errorf("snapshot %q: rpc send: %v", name, err)
		return
	}
	resp, err := awaitResponse(id)
	if err != nil {
		t.Errorf("snapshot %q: %v", name, err)
		return
	}
	if resp.Error != nil {
		t.Errorf("snapshot %q: driver: %s", name, resp.Error.Message)
		return
	}
	var res struct {
		Pass bool   `json:"pass"`
		Diff string `json:"diff"`
	}
	_ = json.Unmarshal(resp.Result, &res)
	if !res.Pass {
		t.Errorf("snapshot %q mismatch:\n%s", name, res.Diff)
	}
}
```

Update `Main()`'s read loop in the same file. Current shape (approx):

```go
for {
	msg, err := in.Read()
	...
	switch msg.Method {
	case "list": ...
	case "run":  ...
	case "cancel": ...
	default: ... method-not-found
	}
}
```

Adjust the dispatcher so responses (messages with empty `Method` and non-nil `ID`) route through `deliverResponse`:

```go
for {
	msg, err := in.Read()
	if err == io.EOF { return }
	if err != nil {
		_ = out.Respond(0, nil, &testrpc.RPCError{Code: -32700, Message: err.Error()})
		continue
	}
	if msg.IsResponse() {
		deliverResponse(msg)
		continue
	}
	if msg.ID == nil {
		continue // stray notification driver→agent
	}
	id := *msg.ID
	switch msg.Method { ... existing list/run/cancel ... }
}
```

Required imports: `base64` (encoding/base64), `json`, `sync`, `time`, `io`. Add to the file's import block if not present.

Helper for the test only: `startReadLoop(at, r)` should kick off a goroutine running the read loop against a `*testrpc.Reader`. Add as a package-level func:

```go
// startReadLoop runs the message-routing loop against r in a goroutine.
// Test-only helper; production code goes through Main.
func startReadLoop(t *T, r io.Reader) {
	go func() {
		rd := testrpc.NewReader(r)
		for {
			m, err := rd.Read()
			if err != nil {
				return
			}
			if m.IsResponse() {
				deliverResponse(m)
			}
		}
	}()
}
```

The struct field `rpcResponses` referenced in the test is unused now (we route through the package-level `pending` table). Drop it from the test:

```go
at := &T{name: "T1", w: testrpc.NewWriter(dwOut)}
```

- [ ] **Step 4: Re-run the tests**

```bash
go test ./pkg/go/testagent/ -count=1 -v
```
Expected: all tests pass, including the two new ones.

- [ ] **Step 5: Confirm driver-side is unchanged**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```
Expected: green. `cmd/sngl/testdriver.go`'s `handleSnapshotAssert` already does the right thing for the new agent path.

- [ ] **Step 6: Commit**

```bash
git add pkg/go/testagent/
git commit -m "$(cat <<'EOF'
pkg/go/testagent: Snapshot intrinsic + RegisterSnapshot

Per-platform snapshot capture is registered during init by the
generated agent_main.go. Snapshot intrinsic encodes the captured bytes
as base64 and roundtrips a snapshotAssert request against the driver,
which already speaks the protocol from Plan 1.

A pending-requests table routes responses by id; the existing read
loop in Main() now dispatches both incoming driver commands and
responses to outstanding requests.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Bubbletea snapshotBytes emission + fixture

**Files:**
- Modify: `codegen/platform/bubbletea/bubbletea.go`
- Create: `cmd/sngl/testdata/test_bubbletea_snapshot.txt`

Bubbletea snapshot is trivial: `Model.View() string` is pure. Codegen emits `snapshotBytes` returning `("text/ansi", []byte(m.View()), nil)`.

- [ ] **Step 1: Add snapshotBytes emission**

Open `codegen/platform/bubbletea/bubbletea.go`. Find the agent-mode emission block from Plan 1 — search for `testagent_main.go`. Just *after* that block writes `testagent_main.go`, append emission of `snapshot.go` containing:

```go
// generated alongside testagent_main.go under --test
src := `package main

import "git.duckfam.us/jonathan/sngl/pkg/go/testagent"

func snapshotBytes(m Model) (string, []byte, error) {
	return "text/ansi", []byte(m.View()), nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentTestModel())
	})
}
`
if err := writeRawFile(sink, "snapshot.go", []byte(src)); err != nil {
	return err
}
```

The `currentTestModel()` helper must also be emitted — it returns the Model the active test holds. Plan 1's `testagent_main.go` template constructs `c := newTestComponent()` per test. Refactor that to assign to a package-level `var current Model` that `currentTestModel()` reads:

```go
// generated in testagent_main.go (modify Plan 1's emission)
var current Model
func currentTestModel() Model { return current }

func testFoo(t *testagent.T) {
	c := newTestComponent()
	current = c
	// ... lowered test body ...
}
```

The change is: each lowered test function assigns `current = c` immediately after `newTestComponent()`. Update `codegen/lang/golang/testlower.go`'s `LowerTestFile` emission so the agent-mode wrapper inserts `current = c` after the `c := newTestComponent()` line.

For native mode (`--opt test=true` → `*_test.go`), the same `snapshot.go` ships alongside but its `RegisterSnapshot` is not used (no testagent in native mode); the file's `snapshotBytes` function is exposed so user-written test code can call it directly if needed. For now, skip emitting `snapshot.go` in native mode entirely — the user's native tests don't have `t.snapshot` routed through the agent anyway.

Gate on `testMode == "agent"`:

```go
if codegen.OptionString(req.Options, "testMode") == "agent" {
	... emit snapshot.go ...
}
```

- [ ] **Step 2: Update testlower agent-mode wrapper**

In `codegen/lang/golang/testlower.go`, find the `LowerTestFile` function (added by Plan 1's Task 5). The agent-mode wrapper emits each test body. Modify the body prelude to:

```go
fmt.Fprintf(&b, "func %s(t *%s) {\n", funcName, paramType)
b.WriteString("\tc := newTestComponent()\n")
if mode == TestEmitAgent {
	b.WriteString("\tcurrent = c\n")
}
b.WriteString("\t_ = c\n")
```

The `current` variable is declared in the emitted snapshot.go (Step 1). Make sure they share the same package.

- [ ] **Step 3: Write the fixture**

Create `cmd/sngl/testdata/test_bubbletea_snapshot.txt`:

```
# t.snapshot(name) captures Model.View() output and diffs against a
# golden under <fixture>.snapshots/<name>.ansi.

[!exec:go] skip 'go toolchain required'

# First run with SNGL_UPDATE_SNAPSHOTS=1 creates the golden.
env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=bubbletea app.sngl
stdout 'PASS'
exists app.sngl.snapshots/initial.ansi

# Second run without the env var passes against the golden.
env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=bubbletea app.sngl
stdout 'PASS'

-- app.sngl --
output { go { bubbletea } }

component counter {
    var count = 0
    text(value=string(count))
}

func testInitialSnapshotMatches(t Test, c counter) {
    t.snapshot("initial")
}
```

- [ ] **Step 4: Run**

```bash
go install ./cmd/sngl/
go test ./cmd/sngl/ -run TestScript/test_bubbletea_snapshot -count=1 -v 2>&1 | tail -20
```

Expected: PASS. If the first run fails because the script-test sandbox doesn't carry `SNGL_UPDATE_SNAPSHOTS` through, the `env` directive needs to be `env SNGL_UPDATE_SNAPSHOTS=1` placed before each `sngl` invocation — adjust per rsc.io/script syntax.

- [ ] **Step 5: Confirm broader suite still green**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/bubbletea/bubbletea.go codegen/lang/golang/testlower.go cmd/sngl/testdata/test_bubbletea_snapshot.txt
git commit -m "$(cat <<'EOF'
codegen/platform/bubbletea: emit snapshotBytes for t.snapshot

snapshot.go is generated alongside testagent_main.go under
testMode=agent. RegisterSnapshot in init() points at a closure that
reads the package-level current Model the active test assigns. Each
emitted test body's prelude now stores its Model into current so
snapshot captures fresh state.

Regression: cmd/sngl/testdata/test_bubbletea_snapshot.txt creates the
golden under SNGL_UPDATE_SNAPSHOTS=1 then diffs on the second run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Fyne snapshotBytes emission + fixture

**Files:**
- Modify: `codegen/platform/fyne/fyne.go`
- Create: `cmd/sngl/testdata/test_fyne_snapshot.txt`

Fyne's render path needs a live app. `fyne.io/fyne/v2/test`'s `WindowToImage` runs an offscreen layout pass and returns an `image.Image` without any user-visible window. We re-use fyne's existing view-builder (already emitted for production code) and call it from `snapshotBytes`.

- [ ] **Step 1: Find fyne's view-builder entry point**

```bash
grep -n 'func.*BuildView\|buildView\|newView' codegen/platform/fyne/fyne.go codegen/platform/fyne/view_ir.go 2>/dev/null | head
```

Identify the function fyne's generated code uses to construct the widget tree from a Model. Likely a method on `*Model` named `BuildView`, `View`, or similar. Confirm the receiver type matches the Model that `newTestComponent()` returns.

- [ ] **Step 2: Add snapshotBytes emission**

In `codegen/platform/fyne/fyne.go`, find the agent-mode emission block (mirrors bubbletea's). Just after it writes `testagent_main.go`, append emission of `snapshot.go`:

```go
if codegen.OptionString(req.Options, "testMode") == "agent" {
	src := `package main

import (
	"bytes"
	"image/png"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

func snapshotBytes(m *Model) (string, []byte, error) {
	a := test.NewApp()
	defer a.Quit()
	win := a.NewWindow("test")
	win.SetContent(m.BuildView())  // adjust to actual emitted method name
	win.Resize(fyne.NewSize(800, 600))
	img := test.WindowToImage(win)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", nil, err
	}
	return "image/png", buf.Bytes(), nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentTestModel())
	})
}
`
	if err := writeRawFile(sink, "snapshot.go", []byte(src)); err != nil {
		return err
	}
}
```

Replace `m.BuildView()` with whatever the actual entry point is per Step 1.

Same `current Model` pattern as bubbletea: ensure `currentTestModel()` is emitted in `testagent_main.go`. The change Plan 1 / this task's Step 2 in Task 2 made to `LowerTestFile` already inserts `current = c` for agent mode — verify by re-reading the modified `testlower.go` after Task 2 lands.

If fyne's Model is a pointer (`*Model`) rather than a value, adjust the closure: `return snapshotBytes(&current)` or pass by reference consistently.

- [ ] **Step 3: Write the fixture**

Create `cmd/sngl/testdata/test_fyne_snapshot.txt`:

```
# Fyne snapshot via WindowToImage from fyne.io/fyne/v2/test.

[!exec:go] skip 'go toolchain required'

env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=fyne app.sngl
stdout 'PASS'
exists app.sngl.snapshots/initial.png

env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=fyne app.sngl
stdout 'PASS'

-- app.sngl --
output { go { fyne } }

component greeter {
    var name = "world"
    text(value=name)
}

func testGreeterSnapshot(t Test, c greeter) {
    t.snapshot("initial")
}
```

- [ ] **Step 4: Run + confirm**

```bash
go test ./cmd/sngl/ -run TestScript/test_fyne_snapshot -count=1 -v 2>&1 | tail -30
```

Fyne's `test.NewApp` is supposed to be host-toolkit-independent. If the test fails on the host with a missing OpenGL / X11 error, document as a known limitation: the fixture requires `fyne.io/fyne/v2/test`'s WindowToImage to work without a display server. If that's a problem on the dev host, move to DONE_WITH_CONCERNS with the specific error captured.

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```
Expected: green (or fyne-specific failure noted above).

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/fyne.go cmd/sngl/testdata/test_fyne_snapshot.txt
git commit -m "$(cat <<'EOF'
codegen/platform/fyne: emit snapshotBytes via test.WindowToImage

snapshot.go is generated under testMode=agent. RegisterSnapshot
points at a closure that builds the widget tree from the current
Model and rasterises via fyne.io/fyne/v2/test's WindowToImage helper,
encoded as PNG.

Regression: test_fyne_snapshot.txt creates the golden under
SNGL_UPDATE_SNAPSHOTS=1 then diffs on second run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Gtk4 cutover (no snapshot yet)

**Files:**
- Delete: `codegen/platform/gtk4/runtests.go`
- Delete: `codegen/platform/gtk4/runtests_js.go`
- Modify: `codegen/platform/gtk4/gtk4.go` (or wherever its Generate lives)

Mechanical cutover — mirrors Plan 1's bubbletea/fyne pattern. No `snapshotBytes` in this task; Task 5 adds it.

- [ ] **Step 1: Find gtk4's Generate entry**

```bash
grep -n 'func.*Generator. Generate\|func.*compilation. Compile' codegen/platform/gtk4/*.go | head
```

Read that function. The new test-file emission lands at the end of Generate, in the same shape bubbletea and fyne use today.

- [ ] **Step 2: Add the emission block**

Add this block at the end of gtk4's Generate, just before the final `return nil` (mirror what bubbletea does):

```go
if codegen.OptionString(req.Options, "testMode") == "agent" {
	codegen.SetOptionField(req.Options, "main", false)
}

if codegen.OptionBool(req.Options, "test") {
	testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
	if len(testFns) > 0 {
		mode := golang.TestEmitNative
		if codegen.OptionString(req.Options, "testMode") == "agent" {
			mode = golang.TestEmitAgent
		}
		src := golang.LowerTestFile("main", testFns, suffixes, methodFields, mode)
		fname := "testagent_main.go"
		if mode == golang.TestEmitNative {
			fname = "model_test.go"
		}
		if err := writeRawFile(sink, fname, []byte(src)); err != nil {
			return err
		}
		if mode == golang.TestEmitAgent {
			mainSrc := []byte("package main\n\nimport \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\nfunc main() { testagent.Main() }\n")
			if err := writeRawFile(sink, "agent_main.go", mainSrc); err != nil {
				return err
			}
		}
	}
}
```

If `writeRawFile` doesn't exist in gtk4's package yet (bubbletea/fyne have it locally), add it:

```go
func writeRawFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}
```

The package name for gtk4-generated test code should match what `main` and `model.go` use — read an existing gtk4 emission to confirm (likely `main`). Replace `"main"` above accordingly.

- [ ] **Step 3: Delete legacy runners**

```bash
git rm codegen/platform/gtk4/runtests.go
git rm codegen/platform/gtk4/runtests_js.go
```

- [ ] **Step 4: Build + suite**

```bash
go build ./... 2>&1 | head -10
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```

Expected: clean. If something references the deleted runners (an `init()` registering a TestRunner, etc.), fix as you go.

- [ ] **Step 5: Smoke a non-snapshot gtk4 test**

Only if `pkg-config --exists gtk4` returns 0:

```bash
TMPDIR=$(mktemp -d); cd "$TMPDIR"
cat > app.sngl <<'EOF'
output { go { gtk4 } }

component counter {
    var count = 0
    text(value="x")
}

func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}
EOF
sngl test --platform=gtk4 .
```

Expected: PASS. If `pkg-config --exists gtk4` fails on the host, note as DONE_WITH_CONCERNS — the cutover is correct, just unverifiable from the dev host. Task 6's probe addresses the skip-cleanly behaviour.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
codegen/platform/gtk4: cut over to TestLauncher path

Delete gtk4/runtests.go and runtests_js.go. Generate now emits
testagent_main.go + agent_main.go under testMode=agent. sngl test
launches the resulting binary via the Go lang's TestLauncher and
collects results over JSON-RPC.

Snapshot support arrives in a follow-up commit; this cutover handles
state-only tests.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Gtk4 snapshotBytes (inline cgo)

**Files:**
- Modify: `codegen/platform/gtk4/gtk4.go` (the same Generate as Task 4)
- Create: `cmd/sngl/testdata/test_gtk4_snapshot.txt`

Gtk4's snapshot needs cgo. We reuse the existing `gtk4SnapshotCgo` preamble and the `sngl_snapshot`/`sngl_pump_idle` C functions defined in `codegen/platform/gtk4/snapshot.go`. The emitted `snapshot.go` in the test binary calls into them via cgo from a `snapshotBytes` Go function.

- [ ] **Step 1: Read the existing snapshot harness**

```bash
sed -n '156,260p' codegen/platform/gtk4/snapshot.go
```

Understand: `gtk4SnapshotCgo` is a string constant containing the `// #cgo pkg-config: gtk4` preamble plus `sngl_snapshot` and `sngl_pump_idle` C functions. The existing `writeGtk4SnapshotHarness` writes a *standalone main* harness for the docsgen snapshot path. We need a *library*-style emission for the test agent: a `snapshotBytes` function callable from the agent, inside the agent binary.

- [ ] **Step 2: Add snapshotBytes emission**

In gtk4's Generate, after the `testagent_main.go`/`agent_main.go` block from Task 4, append (under the same `testMode == "agent"` guard):

```go
src := "package main\n\n" + gtk4SnapshotCgo + `
import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

// activatePayload carries the active Model into the activate callback,
// since cgo can't pass a Go closure across the C boundary.
var activatePayload struct {
	model    *Model
	outPath  string
	err      error
}

//export sngl_test_activate
func sngl_test_activate(app *C.GtkApplication, _ C.gpointer) {
	widget := activatePayload.model.BuildUI(app)
	if widget == nil {
		activatePayload.err = fmt.Errorf("BuildUI returned nil")
		C.g_application_quit(C.G_APPLICATION(app))
		return
	}
	// Realize + show so layout settles.
	C.gtk_widget_set_visible(widget, C.TRUE)
	C.sngl_pump_idle(64)
	cPath := C.CString(activatePayload.outPath)
	defer C.free(unsafe.Pointer(cPath))
	if rc := C.sngl_snapshot(widget, 800, 600, cPath); rc != 0 {
		activatePayload.err = fmt.Errorf("sngl_snapshot rc=%d", int(rc))
	}
	C.g_application_quit(C.G_APPLICATION(app))
}

func snapshotBytes(m *Model) (string, []byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	f, err := os.CreateTemp("", "sngl-snap-*.png")
	if err != nil {
		return "", nil, err
	}
	f.Close()
	defer os.Remove(f.Name())

	activatePayload.model = m
	activatePayload.outPath = f.Name()
	activatePayload.err = nil

	app := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	defer C.g_object_unref(C.gpointer(unsafe.Pointer(app)))
	C.g_signal_connect_data(
		C.gpointer(unsafe.Pointer(app)),
		C.CString("activate"),
		C.GCallback(C.sngl_test_activate),
		nil, nil, 0,
	)
	C.g_application_run(C.G_APPLICATION(app), 0, nil)

	if activatePayload.err != nil {
		return "", nil, activatePayload.err
	}
	data, err := os.ReadFile(activatePayload.outPath)
	if err != nil {
		return "", nil, err
	}
	return "image/png", data, nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentTestModel())
	})
}
`
if err := writeRawFile(sink, "snapshot.go", []byte(src)); err != nil {
	return err
}
```

The exact cgo function names (`g_signal_connect_data`, `C.GCallback`, `C.g_object_unref`) may need small adjustments to match the GTK4 cgo conventions in gtk4SnapshotCgo. Read `gtk4SnapshotCgo` once more to confirm helper signatures and string-to-CString idioms; mirror them.

Also: the `currentTestModel()` helper needs to return `*Model` (pointer) for gtk4 since BuildUI is on `*Model`. Update `testlower.go`'s agent-mode wrapper to declare `current *Model` for gtk4 specifically — wait, that's per-platform. Cleaner: have each platform's emitted `testagent_main.go` carry its own `current` declaration and `currentTestModel` accessor, generated by codegen rather than by `LowerTestFile`.

To keep `LowerTestFile` platform-agnostic, modify it: agent-mode emits `setCurrentTestModel(c)` in the per-test prelude (instead of `current = c`). Each platform's emitted `snapshot.go` (or `testagent_main.go`) declares:

```go
var currentModel *Model // or Model (value), depending on platform
func setCurrentTestModel(m Model) { currentModel = &m }    // for value-Model platforms
// or
func setCurrentTestModel(m *Model) { currentModel = m }   // for pointer-Model platforms
func currentTestModel() *Model { return currentModel }     // or returns Model directly
```

Now `LowerTestFile`'s emission is uniform (`setCurrentTestModel(c)`), and each platform implements the accessor with the right type. Bubbletea (Model value), fyne (Model pointer), gtk4 (Model pointer).

Revise Tasks 2 and 3's edits accordingly: instead of emitting `current = c` directly via `LowerTestFile`, emit `setCurrentTestModel(c)`. The per-platform `snapshot.go` declares `setCurrentTestModel` + `currentTestModel`.

- [ ] **Step 3: Update `LowerTestFile` to emit the setter**

Edit `codegen/lang/golang/testlower.go`. Replace the `current = c` injection (added in Task 2 Step 2) with:

```go
if mode == TestEmitAgent {
	b.WriteString("\tsetCurrentTestModel(c)\n")
}
```

Bubbletea's and fyne's `snapshot.go` must declare `setCurrentTestModel` and `currentTestModel` with their appropriate types. Go back and update Task 2 / Task 3's emitted `snapshot.go` to include those declarations. (Tasks 2 and 3 used `current Model` directly; replace with the accessor pair.)

For example, bubbletea's snapshot.go becomes:

```go
package main

import "git.duckfam.us/jonathan/sngl/pkg/go/testagent"

var currentModel Model

func setCurrentTestModel(m Model) { currentModel = m }
func currentTestModel() Model     { return currentModel }

func snapshotBytes(m Model) (string, []byte, error) {
	return "text/ansi", []byte(m.View()), nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentTestModel())
	})
}
```

Fyne's:

```go
var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }
func currentTestModel() *Model     { return currentModel }
```

Gtk4's (in its snapshot.go, alongside the cgo preamble):

```go
var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }
func currentTestModel() *Model     { return currentModel }
```

`LowerTestFile`'s uniform call `setCurrentTestModel(c)` then dispatches correctly because Go's type inference picks the per-platform receiver.

(Note: the variable type — value vs pointer Model — must match what `newTestComponent()` returns on that platform. Bubbletea returns Model by value; fyne and gtk4 likely return *Model. Confirm by reading the generated files in a tmpdir under `SNGL_KEEP_TEST_DIR=1`.)

- [ ] **Step 4: Write gtk4 fixture**

Create `cmd/sngl/testdata/test_gtk4_snapshot.txt`:

```
# Gtk4 snapshot via cgo + GskCairoRenderer.

[!exec:go] skip 'go toolchain required'
[!exec:pkg-config] skip 'pkg-config not available'
[!exec:gtk4-check] skip 'gtk4 dev libs not present'

env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=gtk4 app.sngl
stdout 'PASS'
exists app.sngl.snapshots/initial.png

env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=gtk4 app.sngl
stdout 'PASS'

-- app.sngl --
output { go { gtk4 } }

component greeter {
    var name = "world"
    text(value=name)
}

func testGreeterSnapshot(t Test, c greeter) {
    t.snapshot("initial")
}
```

The `[!exec:gtk4-check]` condition won't exist out of the box — rsc.io/script's `exec` condition checks PATH for a binary. There's no `gtk4-check` binary; we'd need a custom condition. Two options:

- **A. Use `[exec:pkg-config]` to gate**, then in a `sngl test` step the launcher's probe (Task 6) handles the actual gtk4 lib check at runtime, emitting a skip-style result.
- **B. Add a one-off shell command that pkg-configures gtk4 and write a file; gate the rest on the file's presence.**

Simpler is **A** — the txtar gates on `pkg-config` being on PATH; if pkg-config is present but gtk4 dev libs aren't, the launcher emits a clean skip and the `stdout 'PASS'` assertion fails. That's a real failure. We need Task 6 to make the launcher return a no-tests-collected-but-no-error response. Skip the gtk4-check guard for now; Task 6 will add it cleanly.

For this task, use:

```
[!exec:go] skip 'go toolchain required'
[!exec:pkg-config] skip 'pkg-config not available'
```

and accept that on a host without gtk4 libs, the test will fail (real failure mode). Task 6 closes the gap.

- [ ] **Step 5: Build + suite**

```bash
go build ./... 2>&1 | head -10
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```

Expected: green on hosts with gtk4; fails on hosts without it (acceptable until Task 6).

If the build itself fails (e.g. cgo can't find gtk4 headers) on the dev host, the entire `cmd/sngl/` package would fail to compile — that's a regression. The cgo preamble in `snapshot.go` is only compiled when the testagent binary is BUILT (inside `sngl test` invocations against a fixture), not when the SNGL compiler itself is built. So `go build ./...` for the compiler should be unaffected. If it isn't, you've accidentally pulled cgo into the compiler tree — investigate.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
codegen/platform/gtk4: emit snapshotBytes via inline cgo

Test binaries generated under testMode=agent now carry a snapshot.go
with the gtk4SnapshotCgo preamble + a snapshotBytes function that:
runs gtk_application on the locked OS thread, builds the widget tree
via the existing Model.BuildUI in an activate handler, pumps idle to
settle layout, captures via sngl_snapshot (GskCairoRenderer → PNG),
and returns the bytes.

LowerTestFile's agent-mode wrapper emits setCurrentTestModel(c) so
each per-platform snapshot.go can declare the accessor with the right
Model receiver type. Bubbletea/fyne updated to match.

Regression: cmd/sngl/testdata/test_gtk4_snapshot.txt creates the
golden under SNGL_UPDATE_SNAPSHOTS=1 then diffs on second run, gated
on pkg-config presence.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Clean skip when gtk4 host libs missing

**Files:**
- Modify: `codegen/lang/golang/launcher.go`
- Modify: `cmd/sngl/test.go` (skip-handling)

Gtk4 test binaries fail to build when host lacks gtk4 dev libs. Today that surfaces as a `go build` exit-1 with the linker's error in stderr. Convert to a clean skip in `sngl test --platform=all`.

- [ ] **Step 1: Detect missing pkg-config dependency**

In `codegen/lang/golang/launcher.go`'s `LaunchTest`, the `go build` step captures stderr to `os.Stderr`. Capture it to a buffer instead and pattern-match for the missing-pkg-config signature:

```go
var buildOut bytes.Buffer
bld := exec.CommandContext(ctx, goPath, "build", "-o", binPath, ".")
bld.Dir = dir
bld.Stdout = &buildOut
bld.Stderr = &buildOut
if err := bld.Run(); err != nil {
	out := buildOut.String()
	if strings.Contains(out, "Package gtk4 was not found") ||
		strings.Contains(out, "pkg-config: exit status") ||
		strings.Contains(out, "No package 'gtk4' found") {
		return nil, nil, &skipError{Reason: "gtk4 dev libs not installed (pkg-config)"}
	}
	// Other failures: surface stderr to the user.
	fmt.Fprint(os.Stderr, out)
	return nil, nil, fmt.Errorf("go build: %w", err)
}
```

Define `skipError` at the same file or in `codegen/codegen.go`:

```go
// SkipError signals a host capability is missing; sngl test should
// emit a SKIP line rather than fail. Detected via errors.As.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return "skip: " + e.Reason }
```

Export it (use exported name `codegen.SkipError`) so consumers can match it.

- [ ] **Step 2: Handle skip in the driver**

In `cmd/sngl/testdriver.go` `runViaLauncher`, just after the `LaunchTest` call:

```go
ch, cleanup, err := launcher.LaunchTest(ctx, tmpDir, lang, opts)
if err != nil {
	var skip *codegen.SkipError
	if errors.As(err, &skip) {
		// Surface as a single TestResult marked skipped.
		return []*codegen.TestResult{{Desc: "<launcher>", Passed: true, Log: []string{"SKIP: " + skip.Reason}}}, nil
	}
	return nil, fmt.Errorf("launch: %w", err)
}
```

Required import: `errors`. Add if not present.

This means a launcher-skip for a fixture group produces a passing test result with a SKIP log line. The fixture's `stdout 'PASS'` assertion still matches. `--platform=all` doesn't fail.

- [ ] **Step 3: Verify**

```bash
go build ./...
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```

Expected: green on every host (gtk4 fixture skips cleanly when libs missing; passes when present).

If the host *does* have gtk4 libs, the fixture exercises the real path. If not, it skips. Both are correct.

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/golang/launcher.go cmd/sngl/testdriver.go codegen/codegen.go
git commit -m "$(cat <<'EOF'
codegen/lang/golang: clean skip when test build dependencies missing

LaunchTest pattern-matches pkg-config failure signatures (specifically
"Package gtk4 was not found" and friends) and returns codegen.SkipError.
cmd/sngl/testdriver.go surfaces SkipError as a passing TestResult with
a SKIP log line so --platform=all doesn't fail on hosts lacking gtk4
dev libs.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review notes (for the implementer)

- **`current Model` accessor convention.** Tasks 2/3/5 each emit `setCurrentTestModel(c)` from `LowerTestFile` and `currentTestModel()` from per-platform `snapshot.go`. Type can be value-Model or pointer-Model per platform — Go's type inference threads through. Verify by reading the generated files via `SNGL_KEEP_TEST_DIR=1` after Task 2 lands.
- **Cgo build cost on hosts with gtk4 installed.** Each `sngl test --platform=gtk4` invocation runs a `go build` against the cgo'd test binary. ~5–15 seconds per group on a warm cache. Tolerable.
- **Snapshot diff strategy is bytes-equal for PNGs.** Different gtk versions may produce sub-pixel-different PNGs even with identical inputs; goldens may need updating across host migrations. Pixel-tolerance diff is a follow-up (spec out-of-scope).
- **No worktree.** Project memory says solo repo; work directly on `main`.
- **Don't restructure unrelated files.** Plan 1 left some session drift in the tree. Don't fold it into these commits; only stage files explicitly touched per task.
