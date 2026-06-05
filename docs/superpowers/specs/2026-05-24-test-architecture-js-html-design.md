# Test architecture — JS testagent + html WebSocket transport

Date: 2026-05-24

## Goal

Bring html into the unified test architecture established by Plans 1-3.
The browser is the runtime, but its lifecycle and transport differ
enough from Go/Kotlin agents to warrant their own design.

Replace today's `codegen/platform/html/testing.go` (which walks test
bodies at codegen time and emits sequences of CDP commands) with:

1. A **JS testagent runtime** under `pkg/js/testagent/` that mirrors
   Go/Kotlin testagents structurally.
2. An **html `TestLauncher`** that uses rod for browser lifecycle and
   a WebSocket for results — JSON-RPC over WebSocket.
3. Test bodies that lower to ordinary JS the browser executes. Real
   DOM events, real reactivity, real layout — no per-statement CDP
   emission.

## Non-goals

- Native-mode emission (`sngl generate --opt test=true --platform= html`). For html, the user's existing test cycle is typically E2E
  browser tests (Playwright/Cypress) or unit tests under
  Vitest/Jest/QUnit — too configuration-heavy to autogenerate
  sensibly. `sngl test --platform=html` covers SNGL's testing needs.
  Documented future option: emit QUnit/Vitest tests under a flag, if a
  concrete user scenario emerges. See "Future work" below.
- `testRunner` option on html. Unlike android (Robolectric default,
  device opt-in), html is browser-only. jsdom would be faster but
  catches a different class of bug than the real browser; rod's
  Chromium startup overhead is amortised across the test session and
  per-test cost is in the milliseconds. Single backend.
- `image/png` snapshots via driver-side `Page.captureScreenshot`. The
  existing rod connection makes this trivial to add later; today's
  snapshot is browser-side DOM-string serialization (text/html).

## High-level architecture

```
sngl test --platform=html
                │
                ▼
  html TestLauncher
                │
   ┌────────────┴────────────┐
   │                         │
http.Server                rod browser
(serve page)           (lifecycle + nav)
   │                         │
   └────────┬────────────────┘
            │
            ▼
   Page loads, JS testagent
   reads ?sngl_port=N from
   window.location, opens
   WebSocket to localhost:N
            │
            ▼
   WebSocket carries JSON-RPC 2.0 — same
   protocol as Go/Kotlin testagents.
   Browser dispatches tests via normal
   in-page JS. Snapshot intrinsic
   returns DOM-string bytes.
            │
            ▼
   runComplete → cleanup
   (rod browser close + servers shut down)
```

Distinct from the Go/Kotlin path (stdin/stdout or TCP socket), but the
**wire protocol is identical** — same JSON-RPC 2.0 message types,
same notifications (testStart/testEnd/log/markFail/markSkip), same
snapshotAssert request-response. The driver's `driveRPC` (Plan 1)
treats an html agent's WebSocket no differently from a bubbletea
agent's stdin.

## JS testagent runtime

Lives at `pkg/js/testagent/`. ~300 LOC across:

- `rpc.js` — JSON-RPC 2.0 codec built on browser-native `WebSocket`.
  Frames are JSON strings sent as text frames, one message per frame.
- `t.js` — `T` class. Surface mirrors `pkg/go/testagent.T` and
  `pkg/kotlin/testagent.T`:
  - `t.log(msg)`, `t.fail()`, `t.failNow()`, `t.skip(reason)`,
    `t.error(msg)`, `t.fatal(msg)`, `t.assertTrue(b, msg)`,
    `t.wait(ms)`, `t.snapshot(name)`.
  - `failNow`/`skip`/`fatal` throw an `AbortSentinel`; the test
    dispatcher catches it as normal control-flow exit.
- `registry.js` — `Registry` object with `register(name, fn)`,
  `list()`, `get(name)`. Codegen-emitted init calls populate it.
- `snapshot.js` — `Snapshots` singleton with `register(prefix, fn)`
  and `namePrefix` property. html's emitted snapshot capture returns
  `["text/html", new TextEncoder().encode(document.documentElement.outerHTML)]`.
  The `prefix` arg is unused for html (single-runner platform), passed
  as `""`.
- `testagent.js` — entry point. Reads `sngl_port` from
  `window.location.search`, opens a WebSocket to
  `ws://localhost:<port>/agent`, runs the dispatcher loop on incoming
  messages. No accept-side entry point (`startTcp`-equivalent) needed
  — browsers only connect out.

### Per-test isolation

Each test resets the page state by calling `newTestComponent()`
(emitted by codegen, analogous to Go/Kotlin). Since the page can hold
only one root component at a time, the page is *not* reloaded between
tests — instead, the per-test prelude resets the SNGL state and
re-renders. The DOM is wiped via `document.body.innerHTML = ''` and
the SNGL renderer re-attaches.

If isolation gaps emerge (e.g. event listeners leaking across tests),
the dispatcher can reload the page with `window.location.reload()`
between tests. Defer until measured.

## JS LowerTestFile

`codegen/lang/javascript/testlower.go` (new file). Add `TestEmitMode`
enum (mirroring Plan 1/3) and `LowerTestFile`:

```go
type TestEmitMode int

const (
	TestEmitAgent TestEmitMode = iota
	// TestEmitNative reserved for symmetry with go/kotlin lowerers.
	// Today html's native mode is a no-op; calling LowerTestFile with
	// TestEmitNative returns a doc comment explaining the limitation.
	TestEmitNative
)

func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string,
	methodFields map[string]bool, mode TestEmitMode) string
```

Agent-mode output:

```javascript
import { T } from './testagent/t.js';
import { Registry } from './testagent/registry.js';

async function testFoo(t) {
    const c = newTestComponent();
    setCurrentTestModel(c);
    // ... lowered test body via translate_ir.go ...
    t.assertTrue(c.count === 0, 'count must start at zero');
}

Registry.register('Foo', testFoo);
```

Body lowering reuses the existing `codegen/lang/javascript/ translate_ir.go` machinery. Same shape as Plan 1 Task 5 for Go and
Plan 3 Task 2 for Kotlin.

## html platform agent emission

Modify `codegen/platform/html/html.go`'s `Generate`. Under
`testMode=agent`:

1. Emit `testagent_main.js` — output of `LowerTestFile(TestEmitAgent)`.
2. Emit `snapshot.js` — registers the DOM-string capture under
   `Snapshots.register('', ...)`.
3. Emit `current_model.js` — `setCurrentTestModel(c)` /
   `currentTestModel()` / `newTestComponent()` accessors, analogous to
   Go/Kotlin's TestModelAccessor.
4. **Bundle**. The html platform already uses esbuild for JS bundling
   (`codegen/platform/html/jsbundle.go`). Extend it to include
   `pkg/js/testagent/*` as ES-module dependencies. The bundler resolves
   the relative `./testagent/...` imports during build; the final
   served JS is a single bundle.
5. Inject a `<script type="module">` tag at the end of the emitted
   `<body>` that imports `testagent_main.js` and calls
   `TestAgent.main()` on `DOMContentLoaded`.

## html TestLauncher

`codegen/platform/html/launcher.go`:

```go
!js

package p
func (g *Generator) LaunchTest(ctx, dir, lang, opts) (RPCChannel, Cleanup, error) {
	if _, found := launcher.LookPath(); !found {
		return nil, nil, &codegen.SkipError{Reason: "Chrome/Chromium not on PATH"}
	}

	// Pick two free localhost ports — one for the HTTP server hosting
	// the generated page, one for the WebSocket carrying JSON-RPC.
	pagePort, _ := pickFreeLocalhostPort()
	wsPort, _ := pickFreeLocalhostPort()

	// Page server.
	pageSrv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", pagePort)}
	pageSrv.Handler = http.FileServer(http.Dir(dir))
	go pageSrv.ListenAndServe()

	// WebSocket server. Accept a single inbound connection.
	accepted := make(chan net.Conn, 1)
	wsSrv := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", wsPort),
		Handler: wsAcceptHandler(accepted),
	}
	go wsSrv.ListenAndServe()

	// Launch Chromium via rod, navigate to the page with the WS port
	// baked into the URL query.
	pageURL := fmt.Sprintf("http://127.0.0.1:%d/index.html?sngl_port=%d",
		pagePort, wsPort)
	browser := rod.New().MustConnect()
	page := browser.MustPage(pageURL)

	// Wait for the page's agent to connect back.
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(10 * time.Second):
		return nil, nil, fmt.Errorf("agent did not connect within 10s")
	}

	cleanup := func() {
		_ = conn.Close()
		_ = pageSrv.Close()
		_ = wsSrv.Close()
		_ = page.Close()
		_ = browser.Close()
	}
	return conn, cleanup, nil
}
```

`wsAcceptHandler` upgrades the HTTP request to a WebSocket, wraps the
conn as an `RPCChannel` (which is `io.ReadWriteCloser` — frame-by-
frame Read/Write over the WebSocket), and pushes it onto the
`accepted` channel.

### WebSocket library

The repo doesn't currently use a WebSocket library (check `go.mod`).
Two options:

- `nhooyr.io/websocket` — modern, minimal, recommended by Go authors.
- `gorilla/websocket` — older, more widely deployed, more options.

Pick the smallest dep that gives us text-frame send/receive. Implementer
verifies with `go.mod` first; if either is already imported by an
unrelated path, reuse it.

## Snapshot

Browser-side DOM-string capture only:

```javascript
Snapshots.register('', () => [
    'text/html',
    new TextEncoder().encode(document.documentElement.outerHTML),
]);
```

Goldens land at `<fixture>.snapshots/<name>.html`. The existing
`codegen/testharness/snapshot.Store` handles text-mode mimes (with
unified diff) already.

`image/png` snapshots via driver-side `Page.captureScreenshot` are a
deferred follow-up. Trivial to add: a second intrinsic `t.screenshot( name)` that submits an empty payload to the driver; the driver
recognises a special `mime=image/png; provider=driver` value and
captures via rod's CDP connection. Skip until a fixture wants it.

## Future work (deferred)

- **Native-mode emission for html**. If users want SNGL-generated
  unit tests they can run under their existing JS test runner, add a
  `testRunner` option (`vitest`, `qunit`, `playwright`) and emit the
  corresponding test file shape. Today: skip — `sngl test --platform=html` is the supported path.
- **Driver-side `Page.captureScreenshot` for image/png snapshots**.
  Add when a fixture demands pixel-level fidelity beyond DOM-string
  comparison.
- **Static-site-mode tests**. Today's html platform has two modes:
  `--lang=none` (static site) and route-mode (`--lang=go`). Tests
  presumably only apply to static mode. Confirm during implementation;
  route-mode test emission is out of scope.

## Test plan

- `pkg/js/testagent/*.test.js` — unit tests via Vitest or node:test.
  Cover RPC codec, T intrinsic surface, Registry, abort-via-exception
  control flow. Skip when node not on PATH.
- `cmd/sngl/testdata/test_html_state.txt` — end-to-end state-only
  assertions. Skips when Chrome/Chromium unavailable.
- `cmd/sngl/testdata/test_html_snapshot.txt` — DOM-string snapshot
  golden round-trip under `<fixture>.snapshots/<name>.html`.

## Sequencing

1. **`pkg/js/testagent`** runtime — full set of files, unit-tested in
   isolation. No platform integration yet.
2. **`codegen/lang/javascript/testlower.go`** — `LowerTestFile` with
   agent mode (native mode reserved, no-op).
3. **html codegen agent emission** — `testagent_main.js`,
   `snapshot.js`, `current_model.js`, bundle wiring, `<script>` tag
   injection.
4. **html `TestLauncher`** — http server + WebSocket server + rod
   browser, wired via `codegen.TestLauncher`.
5. **`cmd/sngl/testdriver.go` integration check** — confirm the
   existing `driveRPC` loop (Plan 1) handles WebSocket-backed
   `RPCChannel` correctly. Should be no changes; the channel is just
   an `io.ReadWriteCloser`.
6. **Fixtures** — `test_html_state.txt` + `test_html_snapshot.txt`.
7. **Cleanup** — delete `codegen/platform/html/testing.go` +
   `testing_js.go` + `cdprunner.go` (or whatever the old CDP-command
   emission lives in).

## Risks

- **WebSocket framing**. Adapting a WebSocket conn to
  `io.ReadWriteCloser` requires careful framing — JSON-RPC's line-
  delimited convention assumes one message per `Read` call. The
  adapter must buffer multiple Reads into a single message if the
  WebSocket fragments. Standard problem; use a `bufio.Scanner` with a
  large buffer.
- **Chromium startup time**. First test run will take ~1-2s for
  Chromium boot. Amortised across the test session.
- **Bundling pkg/js/testagent into the served bundle**. esbuild
  needs to resolve the `./testagent/...` imports. May require setting
  up a virtual filesystem or running esbuild with the project root
  pointing at the codegen dir. The existing `jsbundle.go` infrastructure
  should handle this; verify during implementation.
- **Per-test isolation via DOM wipe**. If event listeners or timers
  leak across tests, fall back to `window.location.reload()` per
  test. Slower (~50-100ms per reload).
