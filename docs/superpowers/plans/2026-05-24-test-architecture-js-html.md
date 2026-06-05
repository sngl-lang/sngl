# Test architecture — JS testagent + html WebSocket transport — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring html into the unified test architecture established by Plans 1-3. Replace `codegen/platform/html/testing.go`'s per-statement CDP command emission with a browser-side JS testagent runtime that talks JSON-RPC over WebSocket back to the driver.

**Architecture:** rod for browser lifecycle (launch Chromium, navigate to the served page), WebSocket for the JSON-RPC wire (browser connects out to the driver's localhost listener on page load). Test bodies lower to ordinary JS that runs in-browser — real DOM events, real reactivity, real layout. Snapshots are browser-side DOM-string serialization.

**Tech Stack:** Go (driver), JavaScript (browser-side runtime), `nhooyr.io/websocket` (WebSocket library — new dep), `github.com/go-rod/rod` (browser automation, already used), `github.com/evanw/esbuild/pkg/api` (JS bundling, already used).

**Spec ref:** `docs/superpowers/specs/2026-05-24-test-architecture-js-html-design.md`

**No worktree** — per project memory, work directly on `main`.

---

## File map

- Create `pkg/js/testagent/package.json` — tiny package metadata for the runtime (lets Vitest unit-test it if desired).
- Create `pkg/js/testagent/rpc.js` — JSON-RPC 2.0 codec over WebSocket.
- Create `pkg/js/testagent/t.js` — `T` class with intrinsic surface.
- Create `pkg/js/testagent/registry.js` — `Registry` of test funcs.
- Create `pkg/js/testagent/snapshot.js` — `Snapshots` singleton.
- Create `pkg/js/testagent/testagent.js` — `main()` entry: reads `sngl_port` from URL, opens WebSocket, drives RPC.
- Create `pkg/js/testagent/testagent.test.js` — Vitest/node:test unit tests.
- Create `codegen/lang/javascript/testlower.go` — `LowerTestFile` with `TestEmitAgent` mode.
- Create `codegen/lang/javascript/testlower_test.go` — emission unit tests.
- Create `codegen/platform/html/testagent_emit.go` — emit the per-test-session JS files (`testagent_main.js`, `snapshot.js`, `current_model.js`).
- Modify `codegen/platform/html/html.go` — Generate's tail emits the testagent bundle when `Options.test && testMode=agent`. Also inject a `<script type="module">` tag that calls `TestAgent.main()` on `DOMContentLoaded`.
- Modify `codegen/platform/html/jsbundle.go` — extend the esbuild plugin to resolve imports of `pkg/js/testagent/*` from `pkg/js/testagent/`.
- Create `codegen/platform/html/launcher.go` — html's `TestLauncher`: localhost HTTP server (serves the generated page), localhost WebSocket server (accepts the agent's connection), rod-driven Chromium navigation.
- Modify `go.mod` / `go.sum` — add `nhooyr.io/websocket`.
- Create `cmd/sngl/testdata/test_html_state.txt` — state-only end-to-end fixture.
- Create `cmd/sngl/testdata/test_html_snapshot.txt` — DOM-string snapshot fixture.
- Delete `codegen/platform/html/testing.go`, `testing_js.go`, `cdprunner.go` (or whatever file holds the legacy per-statement CDP emission).

---

## Sequencing

Each task ends green; the suite stays shippable.

1. **Task 1**: `pkg/js/testagent` runtime — full set of files, unit-tested via Node's `node:test`.
2. **Task 2**: `codegen/lang/javascript/testlower.go` — `LowerTestFile` agent-mode emission.
3. **Task 3**: `codegen/platform/html/testagent_emit.go` + html `Generate` integration — emit testagent files under `testMode=agent`.
4. **Task 4**: html `TestLauncher` — rod + http + WebSocket transport.
5. **Task 5**: State fixture (`test_html_state.txt`).
6. **Task 6**: Snapshot fixture (`test_html_snapshot.txt`).
7. **Task 7**: Delete legacy CDP-based testing infrastructure.

---

### Task 1: JS testagent runtime

**Files:**
- Create: `pkg/js/testagent/package.json`
- Create: `pkg/js/testagent/rpc.js`
- Create: `pkg/js/testagent/t.js`
- Create: `pkg/js/testagent/registry.js`
- Create: `pkg/js/testagent/snapshot.js`
- Create: `pkg/js/testagent/testagent.js`
- Create: `pkg/js/testagent/testagent.test.js`

Mirrors `pkg/go/testagent/` and `pkg/kotlin/testagent/` structurally. ~300 LOC total across all 7 files.

- [ ] **Step 1: Module metadata**

`pkg/js/testagent/package.json`:

```json
{
  "name": "@sngl/testagent",
  "version": "0.1.0",
  "type": "module",
  "main": "testagent.js",
  "exports": {
    ".": "./testagent.js",
    "./rpc": "./rpc.js",
    "./t": "./t.js",
    "./registry": "./registry.js",
    "./snapshot": "./snapshot.js"
  },
  "scripts": {
    "test": "node --test testagent.test.js"
  }
}
```

- [ ] **Step 2: RPC codec — failing test first**

`pkg/js/testagent/testagent.test.js`:

```javascript
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encodeMessage, decodeMessage } from './rpc.js';

test('encodes a notification', () => {
    const msg = encodeMessage({ method: 'log', params: { test: 'T1', msg: 'hi' } });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.method, 'log');
    assert.equal(parsed.id, undefined);
    assert.deepEqual(parsed.params, { test: 'T1', msg: 'hi' });
});

test('encodes a request with id', () => {
    const msg = encodeMessage({ id: 7, method: 'list', params: {} });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.id, 7);
    assert.equal(parsed.method, 'list');
});

test('decodes a response', () => {
    const msg = encodeMessage({ id: 7, result: { tests: ['a', 'b'] } });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.method, undefined);
    assert.equal(parsed.id, 7);
    assert.deepEqual(parsed.result, { tests: ['a', 'b'] });
});

test('rejects malformed JSON', () => {
    assert.throws(() => decodeMessage('not json'));
});
```

- [ ] **Step 3: Confirm failure**

```bash
cd pkg/js/testagent && node --test 2>&1 | tail -10
```

Expected: import error — `rpc.js` doesn't exist yet.

- [ ] **Step 4: Implement Rpc codec**

`pkg/js/testagent/rpc.js`:

```javascript
// JSON-RPC 2.0 message helpers. Wire is line-delimited JSON; this
// module handles individual message frames (no transport — that's
// in testagent.js).

export function encodeMessage(msg) {
    const obj = { jsonrpc: '2.0' };
    if (msg.id !== undefined) obj.id = msg.id;
    if (msg.method !== undefined) obj.method = msg.method;
    if (msg.params !== undefined) obj.params = msg.params;
    if (msg.result !== undefined) obj.result = msg.result;
    if (msg.error !== undefined) obj.error = msg.error;
    return JSON.stringify(obj);
}

export function decodeMessage(text) {
    const obj = JSON.parse(text);  // throws on bad JSON
    return {
        id: obj.id,
        method: obj.method,
        params: obj.params,
        result: obj.result,
        error: obj.error,
    };
}

// isNotification: request without id (no response expected).
export function isNotification(msg) {
    return msg.method !== undefined && msg.id === undefined;
}

// isResponse: id present, no method (matches a previously-sent request).
export function isResponse(msg) {
    return msg.id !== undefined && msg.method === undefined;
}
```

- [ ] **Step 5: Run, confirm green**

```bash
cd pkg/js/testagent && node --test 2>&1 | tail -10
```

Expected: 4 tests pass.

- [ ] **Step 6: T class + Registry — failing tests**

Append to `pkg/js/testagent/testagent.test.js`:

```javascript
import { T, AbortSentinel } from './t.js';
import { Registry } from './registry.js';
import { Snapshots } from './snapshot.js';

// Mock WS sink — captures emitted messages instead of sending.
function makeSink() {
    const messages = [];
    const pending = new Map();
    return {
        messages,
        pending,
        send(text) {
            const m = JSON.parse(text);
            messages.push(m);
        },
        resolvePending(id, response) {
            const r = pending.get(id);
            if (r) r(response);
        },
    };
}

test('T.log emits a notification', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    t.log('hello');
    assert.equal(sink.messages.length, 1);
    assert.equal(sink.messages[0].method, 'log');
    assert.deepEqual(sink.messages[0].params, { test: 'myTest', msg: 'hello' });
});

test('T.failNow throws AbortSentinel after marking failed', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    assert.throws(() => t.failNow(), e => e instanceof AbortSentinel);
    assert.equal(t.failed, true);
});

test('T.error logs and marks failed without throwing', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    t.error('oops');
    assert.equal(t.failed, true);
    const logMsg = sink.messages.find(m => m.method === 'log');
    assert.ok(logMsg);
    assert.equal(logMsg.params.msg, 'oops');
});

test('Registry enumerates registered tests', () => {
    Registry.reset();
    Registry.register('foo', t => t.log('ran'));
    Registry.register('bar', t => t.log('ran'));
    assert.deepEqual(Registry.list(), ['bar', 'foo']);
});

test('Snapshots stores prefix + capture fn', () => {
    Snapshots.reset();
    Snapshots.register('html/', () => ['text/html', new Uint8Array([1, 2, 3])]);
    assert.equal(Snapshots.namePrefix, 'html/');
    const [mime, bytes] = Snapshots.capture()();
    assert.equal(mime, 'text/html');
    assert.deepEqual(Array.from(bytes), [1, 2, 3]);
});
```

- [ ] **Step 7: Implement T, Registry, Snapshots**

`pkg/js/testagent/t.js`:

```javascript
// AbortSentinel marks a control-flow unwind from failNow/skip/fatal.
// Caught by the per-test dispatcher in testagent.js; not user-facing.
export class AbortSentinel extends Error {
    constructor() { super('sngl test abort'); this.name = 'AbortSentinel'; }
}

// T is the per-test handle exposed to user test functions. API mirrors
// pkg/go/testagent.T and pkg/kotlin/testagent.T.
export class T {
    constructor(name, sink) {
        this.name = name;
        this.sink = sink;
        this.failed = false;
        this.skipped = false;
    }

    _notify(method, params) {
        this.sink.send(JSON.stringify({ jsonrpc: '2.0', method, params }));
    }

    log(msg) { this._notify('log', { test: this.name, msg }); }

    fail() {
        this.failed = true;
        this._notify('markFail', { test: this.name });
    }

    failNow() {
        this.fail();
        throw new AbortSentinel();
    }

    skip(reason) {
        this.skipped = true;
        this._notify('markSkip', { test: this.name, reason });
        throw new AbortSentinel();
    }

    error(msg) {
        this.log(msg);
        this.fail();
    }

    fatal(msg) {
        this.error(msg);
        throw new AbortSentinel();
    }

    assertTrue(b, msg) {
        if (!b) this.fatal(msg);
    }

    async wait(ms) {
        await new Promise(resolve => setTimeout(resolve, ms));
    }

    async snapshot(name) {
        const { Snapshots } = await import('./snapshot.js');
        const capture = Snapshots.capture();
        if (!capture) {
            this.error(`snapshot "${name}": no capture registered for this platform`);
            return;
        }
        let mime, bytes;
        try {
            [mime, bytes] = capture();
        } catch (e) {
            this.error(`snapshot "${name}": capture: ${e.message}`);
            return;
        }
        const prefixed = Snapshots.namePrefix + name;
        const b64 = btoa(String.fromCharCode(...new Uint8Array(bytes)));
        const id = this.sink.allocId();
        const promise = new Promise(resolve => {
            this.sink.pending.set(id, resolve);
        });
        this.sink.send(JSON.stringify({
            jsonrpc: '2.0',
            id,
            method: 'snapshotAssert',
            params: { test: this.name, name: prefixed, mime, bytes: b64 },
        }));
        const response = await promise;
        if (response.error) {
            this.error(`snapshot "${name}": driver: ${response.error.message}`);
            return;
        }
        if (!response.result?.pass) {
            this.error(`snapshot "${name}" mismatch:\n${response.result?.diff ?? ''}`);
        }
    }
}
```

`pkg/js/testagent/registry.js`:

```javascript
// Registry maps test names to their callable functions. Codegen-
// emitted init blocks register each `test*` SNGL function here.
export const Registry = {
    _tests: new Map(),
    register(name, fn) { this._tests.set(name, fn); },
    get(name) { return this._tests.get(name); },
    list() { return Array.from(this._tests.keys()).sort(); },
    reset() { this._tests.clear(); },
};
```

`pkg/js/testagent/snapshot.js`:

```javascript
// Snapshots stores the per-platform capture function and a name
// prefix that scopes goldens per testRunner. html platform uses ""
// (single runner); android-equivalent platforms use "robolectric/"
// or "device/".
export const Snapshots = {
    _capture: null,
    namePrefix: '',
    register(prefix, fn) { this._capture = fn; this.namePrefix = prefix; },
    capture() { return this._capture; },
    reset() { this._capture = null; this.namePrefix = ''; },
};
```

- [ ] **Step 8: Run all tests**

```bash
cd pkg/js/testagent && node --test 2>&1 | tail -15
```

Expected: 9 tests pass.

- [ ] **Step 9: testagent.js entry point**

`pkg/js/testagent/testagent.js`:

```javascript
import { T, AbortSentinel } from './t.js';
import { Registry } from './registry.js';

// Sink wraps a WebSocket plus a pending-id table for response routing.
function makeSink(ws) {
    let nextId = 0;
    const pending = new Map();
    const sink = {
        send(text) { ws.send(text); },
        allocId() { return ++nextId; },
        pending,
    };
    ws.addEventListener('message', e => {
        const msg = JSON.parse(e.data);
        if (msg.id !== undefined && msg.method === undefined) {
            const resolve = pending.get(msg.id);
            if (resolve) {
                pending.delete(msg.id);
                resolve(msg);
            }
            return;
        }
        if (msg.id !== undefined && msg.method !== undefined) {
            handleRequest(msg, sink);
        }
    });
    return sink;
}

async function handleRequest(msg, sink) {
    const reply = (result, error) => {
        const obj = { jsonrpc: '2.0', id: msg.id };
        if (error) obj.error = error;
        else obj.result = result;
        sink.send(JSON.stringify(obj));
    };

    switch (msg.method) {
        case 'list':
            reply({ tests: Registry.list() });
            return;
        case 'run':
            await runFiltered(sink, msg.params?.filter ?? '');
            reply({});
            return;
        case 'cancel':
            reply({});
            return;
        default:
            reply(null, { code: -32601, message: `method not found: ${msg.method}` });
    }
}

async function runFiltered(sink, filter) {
    let passed = 0, failed = 0, skipped = 0;
    for (const name of Registry.list()) {
        if (filter && !name.includes(filter)) continue;
        const status = await runOne(sink, name);
        if (status === 'pass') passed++;
        else if (status === 'fail') failed++;
        else if (status === 'skip') skipped++;
    }
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'runComplete',
        params: { passed, failed, skipped },
    }));
}

async function runOne(sink, name) {
    const fn = Registry.get(name);
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'testStart',
        params: { test: name },
    }));
    const t = new T(name, sink);
    const start = Date.now();
    try {
        await fn(t);
    } catch (e) {
        if (!(e instanceof AbortSentinel)) {
            t.failed = true;
            sink.send(JSON.stringify({
                jsonrpc: '2.0',
                method: 'log',
                params: { test: name, msg: `exception: ${e.message}` },
            }));
        }
    }
    const status = t.skipped ? 'skip' : (t.failed ? 'fail' : 'pass');
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'testEnd',
        params: { test: name, status, durationMs: Date.now() - start },
    }));
    return status;
}

// main is the entry point the emitted page's <script> calls on
// DOMContentLoaded. Reads SNGL_AGENT_PORT from the URL query string,
// opens a WebSocket, and waits for the dispatcher to drive it.
export async function main() {
    const port = new URLSearchParams(window.location.search).get('sngl_port');
    if (!port) {
        console.warn('sngl testagent: no sngl_port query param — agent will not connect');
        return;
    }
    const ws = new WebSocket(`ws://127.0.0.1:${port}/agent`);
    await new Promise(resolve => {
        ws.addEventListener('open', resolve, { once: true });
    });
    makeSink(ws);
}

// Re-export for codegen-emitted modules.
export { T, AbortSentinel } from './t.js';
export { Registry } from './registry.js';
export { Snapshots } from './snapshot.js';
```

- [ ] **Step 10: Confirm node --test still passes**

```bash
cd pkg/js/testagent && node --test 2>&1 | tail -5
```

Expected: all 9 tests pass.

- [ ] **Step 11: Commit**

```bash
git add pkg/js/testagent/
git commit -m "$(cat <<'EOF'
pkg/js/testagent: JS testagent runtime

Mirrors pkg/go/testagent and pkg/kotlin/testagent: T class with
log/fail/skip/error/fatal/assertTrue/wait/snapshot intrinsics,
Registry of test functions, Snapshots singleton with namePrefix
(for per-testRunner golden scoping), JSON-RPC 2.0 message helpers,
and a main() entry that opens a WebSocket back to the driver based
on the SNGL_AGENT_PORT query parameter.

Snapshot intrinsic routes the response via a pending-id table —
same shape as Go/Kotlin.

Unit-tested in isolation via node:test. No platform integration yet.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: JS LowerTestFile

**Files:**
- Create: `codegen/lang/javascript/testlower.go`
- Create: `codegen/lang/javascript/testlower_test.go`

Mirrors Plan 1's Go `LowerTestFile` and Plan 3's Kotlin equivalent. Agent mode emits `async function testFoo(t) { ... }` declarations + Registry.register calls; native mode reserved.

- [ ] **Step 1: Read the existing JS translator**

```bash
grep -n 'func TranslateIR\|func.*translateIRStmt\|func.*EmitTestBody' codegen/lang/javascript/*.go | head
```

Identify how test-body statements are translated to JS in the existing path. The existing `testing.go` in the html platform walks AST and emits CDP commands — that's NOT the path we want. We want plain JS lowering, which `translate_ir.go` already does for non-test code. Confirm by reading a function it exports for emitting a statement to JS source.

- [ ] **Step 2: Write `testlower.go`**

`codegen/lang/javascript/testlower.go`:

```go
package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestEmitMode selects how LowerTestFile wraps per-test bodies.
type TestEmitMode int

const (
	// TestEmitAgent produces async functions taking a T parameter plus
	// a Registry.register init for each. Used by sngl test --platform=
	// html.
	TestEmitAgent TestEmitMode = iota
	// TestEmitNative is reserved for symmetry with the go/kotlin
	// lowerers. For html, native-mode emission is currently a no-op
	// (sngl test --platform=html is the supported path). Calling
	// LowerTestFile with this mode returns a doc comment.
	TestEmitNative
)

// LowerTestFile produces the entire source of a generated JS test file.
// Body lowering is identical across modes; the wrapper differs.
//
// Agent mode emits:
//
//	import { Registry } from './testagent/testagent.js';
//
//	export async function testFoo(t) {
//	    const c = newTestComponent();
//	    setCurrentTestModel(c);
//	    ... lowered test body ...
//	}
//
//	Registry.register('Foo', testFoo);
func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string,
	methodFields map[string]bool, mode TestEmitMode) string {

	if mode == TestEmitNative {
		return "// native-mode html test emission is not supported.\n" +
			"// Use `sngl test --platform=html` instead.\n"
	}

	var b strings.Builder
	b.WriteString("// Generated by SNGL — do not edit.\n")
	b.WriteString("import { Registry } from './testagent/testagent.js';\n\n")

	for i, fn := range fns {
		suffix := suffixes[i]
		fmt.Fprintf(&b, "export async function test%s(t) {\n", suffix)
		b.WriteString("    const c = newTestComponent();\n")
		b.WriteString("    setCurrentTestModel(c);\n")
		for _, line := range lowerTestBody(fn, methodFields) {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		b.WriteString("}\n\n")
	}

	b.WriteString("// Eager Registry.register so the module's side effect runs on import.\n")
	for i := range fns {
		suffix := suffixes[i]
		fmt.Fprintf(&b, "Registry.register('%s', test%s);\n", suffix, suffix)
	}

	return b.String()
}

// lowerTestBody walks the IR test function body and returns JS source
// lines. Shares the existing JS translator's statement emitter — same
// machinery that lowers non-test functions.
func lowerTestBody(fn *ir.Func, methodFields map[string]bool) []string {
	// Build a per-call ExprScope so the translator knows which params
	// (especially the component param) get raw-field access.
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

	var out []string
	for _, s := range fn.Block {
		// Use the existing JS translator's per-statement emit hook.
		// If the JS package exposes a function like translateIRStmt
		// returning []string, call it here. Adjust to whatever name
		// is used by Plan 1/2's existing JS codegen path.
		lines := translateIRStmt(s, scope)
		out = append(out, lines...)
	}
	return out
}
```

The `translateIRStmt` function in the snippet must match whatever the JS package exposes for per-statement translation. Search:

```bash
grep -n 'func translateIRStmt\|func TranslateIRStmt\|func.*Stmt.*\[\]string' codegen/lang/javascript/translate_ir.go | head
```

If the existing translator is structured around a `JsIRContext.EvalStmt(s ir.Stmt) []string` method (per `irwalk.EvalStmt`), use that instead:

```go
ctx := NewJsIRContext(scope, ...)
for _, s := range fn.Block {
    out = append(out, ctx.EvalStmt(s)...)
}
```

Read `codegen/lang/javascript/ircontext.go` to find the right entry point. The kotlin testlower in `codegen/lang/kotlin/testlower.go` uses `lowerTestStmt` — JS likely has an analog.

- [ ] **Step 3: Add tests**

`codegen/lang/javascript/testlower_test.go`:

```go
package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestJSLowerTestFile_agentModeEmitsRegisterAll(t *testing.T) {
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
		t.Fatal(err)
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
	if !strings.Contains(out, "import { Registry } from './testagent/testagent.js'") {
		t.Errorf("agent mode missing Registry import:\n%s", out)
	}
	if !strings.Contains(out, "export async function testFoo(t)") {
		t.Errorf("agent func signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Registry.register('Foo', testFoo)") {
		t.Errorf("Registry.register call missing:\n%s", out)
	}
}

func TestJSLowerTestFile_nativeModeIsNoOp(t *testing.T) {
	out := LowerTestFile("ui", nil, nil, nil, TestEmitNative)
	if !strings.Contains(out, "not supported") {
		t.Errorf("native mode should emit a no-op comment:\n%s", out)
	}
}
```

- [ ] **Step 4: Run**

```bash
go test ./codegen/lang/javascript/ -run TestJSLowerTestFile -count=1 -v 2>&1 | tail -15
```

Expected: both new tests pass.

```bash
go test ./codegen/lang/javascript/ -count=1 2>&1 | tail -5
go build ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/testlower.go codegen/lang/javascript/testlower_test.go
git commit -m "$(cat <<'EOF'
codegen/lang/javascript: add LowerTestFile with agent mode

Agent mode emits async function test<Suffix>(t) declarations plus a
Registry.register call per test. Body lowering reuses the existing
JS translator's per-statement emitter — same machinery as non-test
code.

TestEmitNative reserved for symmetry; html doesn't support native-mode
emission (use sngl test --platform=html). Returns a doc comment if
invoked.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: html codegen agent emission

**Files:**
- Create: `codegen/platform/html/testagent_emit.go`
- Modify: `codegen/platform/html/html.go`
- Modify: `codegen/platform/html/jsbundle.go` (extend the esbuild resolver to pull in `pkg/js/testagent/`)

- [ ] **Step 1: Find html Generate and the script-tag injection point**

```bash
grep -n 'func.*Generator. Generate\|func.*compilation. Compile\|writeRawFile\|<script' codegen/platform/html/*.go | head -15
```

Identify:
- Where the main `<body>` content is assembled.
- Where the `<script>` block is injected.
- The bundling step (`bundleNativeScript` in `jsbundle.go`).

- [ ] **Step 2: Write `testagent_emit.go`**

`codegen/platform/html/testagent_emit.go`:

```go
//go:build !js

package html

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

// emitTestagentFiles writes the JS test files into sink when the
// html platform's Generate runs with --opt test=true testMode=agent.
// Produces:
//   - testagent_main.js — output of javascript.LowerTestFile(agent),
//     contains test funcs + Registry.register calls.
//   - snapshot.js — registers the DOM-string capture under
//     Snapshots.register(”, ...).
//   - current_model.js — setCurrentTestModel/currentTestModel/
//     newTestComponent accessors.
//
// The browser-side <script type="module"> that imports these and
// calls TestAgent.main() is appended to the page's body by Generate
// itself (see html.go).
func emitTestagentFiles(sink codegen.Sink, pkg *ir.Package, suffixes []string, testFns []*ir.Func, methodFields map[string]bool, modelType string) error {
	testagentMain := javascript.LowerTestFile("ui", testFns, suffixes, methodFields, javascript.TestEmitAgent)
	if err := writeRawFile(sink, "testagent_main.js", []byte(testagentMain)); err != nil {
		return err
	}

	snapshotJS := `// Generated by SNGL — do not edit.
import { Snapshots } from './testagent/testagent.js';

// Browser-side snapshot capture for html. Serialises the document
// element as text/html bytes. Per-testRunner name prefix is empty —
// html is a single-backend platform.
Snapshots.register('', () => {
    const text = document.documentElement.outerHTML;
    return ['text/html', new TextEncoder().encode(text)];
});
`
	if err := writeRawFile(sink, "snapshot.js", []byte(snapshotJS)); err != nil {
		return err
	}

	accessor := fmt.Sprintf(`// Generated by SNGL — do not edit.
// Per-test fresh-model accessor.

let __snglCurrentModel = null;
export function setCurrentTestModel(m) { __snglCurrentModel = m; }
export function currentTestModel() { return __snglCurrentModel; }
export function newTestComponent() { return new %s(); }

// Re-export so testagent_main.js can pick them up by importing this file.
`, modelType)
	if err := writeRawFile(sink, "current_model.js", []byte(accessor)); err != nil {
		return err
	}
	return nil
}

// writeRawFile copies bytes through sink (matches the pattern used in
// codegen/platform/bubbletea/bubbletea.go, etc.).
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

The `modelType` argument is whatever class name the html platform's existing JS codegen produces for the root component (likely `Model` or `App` — confirm by reading an existing emitted JS file). If multiple test groups exist (different components under test), this needs to be per-group; for the first cut, hard-code or pass the root component's class name.

- [ ] **Step 3: Wire into html.Generate**

In `codegen/platform/html/html.go`, find the spot near the end of Generate where the final HTML is assembled. After the existing emission, add:

```go
if codegen.OptionBool(req.Options, "test") {
	testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
	if len(testFns) > 0 && codegen.OptionString(req.Options, "testMode") == "agent" {
		// Determine the model type — emitted JS uses a class named
		// after the root component. Use the first user component's
		// name as a heuristic.
		modelType := "Model"
		if c := codegen.MainComponent(req.Pkg); c != nil {
			modelType = c.Name
		}
		if err := emitTestagentFiles(sink, req.Pkg, suffixes, testFns, methodFields, modelType); err != nil {
			return err
		}
		// Inject <script type="module"> at the end of <body> that
		// imports testagent_main.js, snapshot.js, current_model.js,
		// and calls TestAgent.main() on DOMContentLoaded.
		// (Append the script tag to the existing HTML buffer at the
		// point where </body> is closed — exact splice depends on
		// how html.go assembles the body. See below.)
		appendTestagentScriptTag( /* the buffer or builder */ )
	}
}
```

The `appendTestagentScriptTag` injection point depends on the existing buffer's structure. Pattern:

```go
func appendTestagentScriptTag(b *strings.Builder) {
	b.WriteString(`<script type="module">
import { main } from './testagent/testagent.js';
import './testagent_main.js';
import './snapshot.js';
import './current_model.js';
document.addEventListener('DOMContentLoaded', () => main());
</script>
`)
}
```

If the html platform's HTML assembly uses a Go html/template or string builder, splice this before `</body>`. Read `codegen/platform/html/html.go`'s Generate to confirm the assembly path.

- [ ] **Step 4: Extend jsbundle for testagent imports**

`codegen/platform/html/jsbundle.go` already runs an esbuild plugin that resolves `import` statements. Extend the resolver so paths starting with `./testagent/` resolve to `pkg/js/testagent/` in the SNGL source tree.

The discovery mechanism (similar to Plan 3's `findTestAgentPath`): use `SNGL_HOST_GO_MOD` env or walk up from `runtime.Caller(0)`. Or, since the testagent JS is small, **embed it via `//go:embed pkg/js/testagent/*.js`** directly into the html package. Embedding is cleaner — no filesystem dependency at run time.

Suggested:

```go
//go:embed testagent_assets/*.js
var testagentAssets embed.FS
```

Wait — `//go:embed` only embeds files inside the package's own directory tree. The testagent JS lives at `pkg/js/testagent/`, outside `codegen/platform/html/`. Two options:

- (a) Copy the JS files into `codegen/platform/html/testagent_assets/` as build artifacts.
- (b) Use a `go generate` directive that copies them in.
- (c) Resolve at runtime via `SNGL_HOST_GO_MOD` (matches Plan 3 android pattern).

For Plan 4 simplicity, go with **(c)**: at codegen time, the launcher (or the bundler) reads testagent files from disk via the discovered path. Reuse Plan 3 android's `findTestAgentPath` pattern; rename and move to a shared helper:

```go
// codegen/testharness/testagent_path.go
package testharness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LangTestagentPath returns the absolute path to pkg/<lang>/testagent in
// the sngl source tree. Reused across platforms whose generated test
// binaries link the testagent module.
func LangTestagentPath(lang string) (string, error) {
	if mod := os.Getenv("SNGL_HOST_GO_MOD"); mod != "" {
		root := filepath.Dir(mod)
		p := filepath.Join(root, "pkg", lang, "testagent")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exe)
	}
	if _, here, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, here)
	}
	for _, start := range candidates {
		dir := filepath.Dir(start)
		for i := 0; i < 12; i++ {
			modPath := filepath.Join(dir, "go.mod")
			if data, err := os.ReadFile(modPath); err == nil {
				if strings.Contains(string(data), "module git.duckfam.us/jonathan/sngl") {
					p := filepath.Join(dir, "pkg", lang, "testagent")
					if _, err := os.Stat(p); err == nil {
						return p, nil
					}
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not locate pkg/%s/testagent (set SNGL_HOST_GO_MOD)", lang)
}
```

The existing `codegen/platform/android/launcher.go`'s `findTestAgentPath` becomes a thin wrapper calling `testharness.LangTestagentPath("kotlin")`. New helper.

Then in `jsbundle.go`, when bundling the html test page, add an esbuild plugin onResolve handler that intercepts `./testagent/*` imports and serves them from the discovered `pkg/js/testagent/` directory.

- [ ] **Step 5: Smoke**

```bash
go install ./cmd/sngl/
mkdir -p /tmp/sngl-html-emit && cd /tmp/sngl-html-emit
cat > app.sngl <<'EOF'
output { none { html } }
component counter {
    var count = 0
    text(value=string(count))
}
func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}
EOF
sngl generate --lang=none --platform=html --opt test=true --opt testMode=agent --out out .
ls out/
echo --- testagent_main.js ---
cat out/testagent_main.js
echo --- snapshot.js ---
cat out/snapshot.js
echo --- current_model.js ---
cat out/current_model.js
echo --- index.html final lines ---
tail -10 out/index.html
```

Expected:
- `out/` contains `testagent_main.js`, `snapshot.js`, `current_model.js`, `index.html`.
- `index.html` ends with `<script type="module"> ... main() ... </script></body>`.

- [ ] **Step 6: Build + suite**

```bash
go build ./...
go test ./codegen/platform/html/ -count=1 2>&1 | tail -5
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/html/ codegen/testharness/testagent_path.go
git commit -m "$(cat <<'EOF'
codegen/platform/html: emit testagent JS files under testMode=agent

Generate now emits testagent_main.js, snapshot.js, current_model.js
when Options.test && testMode=agent, plus a <script type="module">
tag that imports them and calls TestAgent.main() on DOMContentLoaded.

testharness.LangTestagentPath is a new shared helper for discovering
pkg/<lang>/testagent in the SNGL source tree (env + walk-up); the
existing android findTestAgentPath now delegates to it.

esbuild bundle resolver pulls pkg/js/testagent/*.js as ES modules at
codegen time.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: html TestLauncher

**Files:**
- Create: `codegen/platform/html/launcher.go`
- Modify: `go.mod` (add `nhooyr.io/websocket`)

- [ ] **Step 1: Add the WebSocket dep**

```bash
go get nhooyr.io/websocket@latest
go mod tidy
```

Expected: `nhooyr.io/websocket` added to `require` block.

- [ ] **Step 2: Write the launcher**

`codegen/platform/html/launcher.go`:

```go
//go:build !js

package html

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"nhooyr.io/websocket"
)

// LaunchTest implements codegen.TestLauncher for html. Hosts the
// generated page on a localhost HTTP server, accepts the agent's
// WebSocket connection on a second port, and drives a Chromium
// instance via rod.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if _, found := launcher.LookPath(); !found {
		return nil, nil, &codegen.SkipError{Reason: "Chrome/Chromium not on PATH"}
	}

	pageL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen page: %w", err)
	}
	pagePort := pageL.Addr().(*net.TCPAddr).Port
	pageSrv := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go pageSrv.Serve(pageL)

	accepted := make(chan codegen.RPCChannel, 1)
	accErr := make(chan error, 1)
	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			accErr <- err
			return
		}
		accepted <- &wsChannel{conn: c, ctx: ctx}
	}
	wsL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pageSrv.Close()
		return nil, nil, fmt.Errorf("listen ws: %w", err)
	}
	wsPort := wsL.Addr().(*net.TCPAddr).Port
	wsSrv := &http.Server{
		Handler: http.HandlerFunc(wsHandler),
	}
	go wsSrv.Serve(wsL)

	pageURL := fmt.Sprintf("http://127.0.0.1:%d/index.html?sngl_port=%d", pagePort, wsPort)

	browserPath, _ := launcher.LookPath()
	l := launcher.New().Bin(browserPath).Headless(true)
	wsURL, err := l.Launch()
	if err != nil {
		pageSrv.Close()
		wsSrv.Close()
		return nil, nil, fmt.Errorf("browser launch: %w", err)
	}
	browser := rod.New().ControlURL(wsURL).MustConnect()
	page := browser.MustPage(pageURL)

	select {
	case ch := <-accepted:
		cleanup := func() {
			_ = ch.Close()
			pageSrv.Close()
			wsSrv.Close()
			_ = page.Close()
			_ = browser.Close()
			l.Cleanup()
		}
		return ch, cleanup, nil
	case err := <-accErr:
		pageSrv.Close()
		wsSrv.Close()
		browser.Close()
		l.Cleanup()
		return nil, nil, fmt.Errorf("ws accept: %w", err)
	case <-time.After(15 * time.Second):
		pageSrv.Close()
		wsSrv.Close()
		browser.Close()
		l.Cleanup()
		return nil, nil, fmt.Errorf("agent did not connect within 15s")
	}
}

// wsChannel adapts a nhooyr.io/websocket.Conn to codegen.RPCChannel.
// Reads buffer the most recent text frame into a slice consumed by
// Read calls; Write sends each call as a single text frame.
type wsChannel struct {
	conn  *websocket.Conn
	ctx   context.Context
	rdBuf []byte
}

func (w *wsChannel) Read(p []byte) (int, error) {
	if len(w.rdBuf) > 0 {
		n := copy(p, w.rdBuf)
		w.rdBuf = w.rdBuf[n:]
		return n, nil
	}
	typ, data, err := w.conn.Read(w.ctx)
	if err != nil {
		return 0, err
	}
	if typ != websocket.MessageText {
		return 0, fmt.Errorf("unexpected ws frame type %v", typ)
	}
	// Append newline so the driver's bufio.Scanner sees one message per line.
	data = append(data, '\n')
	n := copy(p, data)
	if n < len(data) {
		w.rdBuf = data[n:]
	}
	return n, nil
}

func (w *wsChannel) Write(p []byte) (int, error) {
	// Driver writes a single JSON-RPC message followed by '\n'. Strip
	// the trailing newline before sending as a text frame.
	if len(p) > 0 && p[len(p)-1] == '\n' {
		p = p[:len(p)-1]
	}
	if err := w.conn.Write(w.ctx, websocket.MessageText, p); err != nil {
		return 0, err
	}
	return len(p) + 1, nil // report n including the newline the caller wrote
}

func (w *wsChannel) Close() error {
	return w.conn.Close(websocket.StatusNormalClosure, "")
}

var _ io.ReadWriteCloser = (*wsChannel)(nil)
```

The trailing-newline handling is important: the driver's `internal/testrpc.Reader` uses `bufio.Scanner` which splits on newlines, while WebSocket frames are message-delimited (no newline). The adapter translates.

- [ ] **Step 3: Build + suite**

```bash
go mod tidy
go build ./...
go test ./codegen/platform/html/ -count=1 2>&1 | tail -5
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4: Smoke**

```bash
go install ./cmd/sngl/
mkdir -p /tmp/sngl-html-e2e && cd /tmp/sngl-html-e2e
cat > app.sngl <<'EOF'
output { none { html } }
component counter {
    var count = 0
    text(value=string(count))
}
func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}
EOF
SNGL_KEEP_TEST_DIR=1 SNGL_HOST_GO_MOD=/home/jonathan/src/git.duckfam.us/jonathan/sngl/go.mod \
    sngl test --platform=html . 2>&1 | tail -20
```

Expected: Chromium starts headless, navigates to the served page, agent connects, PASS reported.

If the smoke fails because of:
- `Chrome/Chromium not on PATH`: install Chromium or rely on rod's auto-download (not preferred — keep `go tool verify` predictable).
- `agent did not connect within 15s`: inspect the kept temp dir's `index.html`. Confirm the `<script type="module">` tag is present and the imports resolve. Common gotcha: the bundler didn't pull in `pkg/js/testagent/*` so the page's `import { main } from './testagent/testagent.js'` 404s. Open the page manually in a browser and check DevTools console.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/html/launcher.go go.mod go.sum
git commit -m "$(cat <<'EOF'
codegen/platform/html: TestLauncher using rod + WebSocket

LaunchTest hosts the generated page on a localhost HTTP server, starts
a second localhost server with a WebSocket upgrader, launches headless
Chromium via rod, navigates to the page with sngl_port query param. The
browser-side testagent connects back over WebSocket; the conn is
adapted to codegen.RPCChannel and returned to the driver.

15s connect timeout. Skip-clean when no Chrome/Chromium on PATH. New
dep: nhooyr.io/websocket.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: State fixture

**Files:**
- Create: `cmd/sngl/testdata/test_html_state.txt`

- [ ] **Step 1: Write the fixture**

```
# html end-to-end: state-only assertions via sngl test --platform=html.
# Skips when Chrome/Chromium isn't on PATH.

sngl test --platform=html app.sngl
stdout 'PASS'

-- app.sngl --
output { none { html } }

component counter {
    var count = 0
    text(value=string(count))
}

func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}

func testIncrement(t Test, c counter) {
    c.count = 5
    t.assert(c.count == 5)
}
```

No `[!exec:chromium]` guard — there's no single canonical Chromium binary name. The launcher's `launcher.LookPath()` covers `chrome`, `chromium`, `chromium-browser`, etc. Skip-clean handling via `codegen.SkipError` from Plan 2 Task 6 surfaces a passing `<launcher-skip>` result with `stdout 'PASS'` matching.

- [ ] **Step 2: Run**

```bash
go test ./cmd/sngl/ -run TestScript/test_html_state -count=1 -v 2>&1 | tail -20
```

Expected on hosts with Chromium: PASS, both tests run. On hosts without: skip-clean → passing TestResult with SKIP log.

- [ ] **Step 3: Confirm broader suite**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/testdata/test_html_state.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: html state fixture

End-to-end state-only assertions via sngl test --platform=html. Runs
in headless Chromium via rod. Skip-clean when Chrome/Chromium not
available — launcher returns codegen.SkipError, driver surfaces as
passing TestResult with SKIP log.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Snapshot fixture

**Files:**
- Create: `cmd/sngl/testdata/test_html_snapshot.txt`

- [ ] **Step 1: Write the fixture**

```
# html DOM-string snapshot round-trip. Golden at <fixture>.snapshots/
# initial.html (no name prefix — html is single-runner).

env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=html app.sngl
stdout 'PASS'
exists app.sngl.snapshots/initial.html

env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=html app.sngl
stdout 'PASS'

-- app.sngl --
output { none { html } }

component greeter {
    var name = "world"
    text(value=name)
}

func testGreeterSnapshot(t Test, c greeter) {
    t.snapshot("initial")
}
```

- [ ] **Step 2: Run**

```bash
go test ./cmd/sngl/ -run TestScript/test_html_snapshot -count=1 -v 2>&1 | tail -25
```

Expected on hosts with Chromium: first run creates the golden; second run diffs against it; both PASS.

- [ ] **Step 3: Confirm broader suite**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/testdata/test_html_snapshot.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: html DOM-string snapshot fixture

Browser-side document.documentElement.outerHTML capture, returned as
text/html. Golden lives at <fixture>.snapshots/initial.html. Round-
trip: create on SNGL_UPDATE_SNAPSHOTS=1, diff on second run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Delete legacy CDP-based testing

**Files:**
- Delete: `codegen/platform/html/testing.go`
- Delete: `codegen/platform/html/testing_js.go`
- Delete: `codegen/platform/html/cdprunner.go` (or whatever file holds the legacy per-statement CDP emission)
- Delete: `codegen/platform/html/internal/webtest/*` (if not used elsewhere)

- [ ] **Step 1: Confirm what's removable**

```bash
grep -rn 'RunTests\b' codegen/platform/html/ | grep -v _test.go
grep -rn 'cdprunner\b\|webtest\b' codegen/platform/html/ codegen/ cmd/ | grep -v _test.go
```

Anything outside the html package that references `RunTests`, `cdprunner`, or `webtest` is a non-test caller. Most likely none. The `cmd/sngl/test.go` legacy fallback path (`safeRunTests`) still uses TestRunner for the `none` platform but not html (the new launcher takes precedence).

- [ ] **Step 2: Delete the files**

```bash
git rm codegen/platform/html/testing.go
git rm codegen/platform/html/testing_js.go
# Identify the cdprunner file:
ls codegen/platform/html/*.go | xargs grep -l 'cdprunner\|CDPRunner\|RunCDP'
# And remove it:
git rm <that file>
# Webtest:
ls codegen/platform/html/internal/webtest/ 2>/dev/null
# If only used by testing.go, remove:
git rm -r codegen/platform/html/internal/webtest/
```

- [ ] **Step 3: Build + suite**

```bash
go build ./... 2>&1 | head -10
```

Expected: clean. Anything that broke is something we need to keep — restore from git, then either preserve as-is or migrate the consumer to the launcher path.

```bash
go test ./... -count=1 2>&1 | grep -E 'FAIL|ok' | tail -10
```

Expected: clean.

- [ ] **Step 4: Confirm html state + snapshot fixtures still pass**

```bash
go test ./cmd/sngl/ -run 'TestScript/test_html_' -count=1 -v 2>&1 | tail -20
```

Expected: state + snapshot fixtures PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
codegen/platform/html: delete legacy CDP-based testing infrastructure

testing.go + testing_js.go + cdprunner + internal/webtest are
superseded by the TestLauncher + JS testagent path from Plan 4. The
new path uses rod for browser lifecycle (same Chromium dependency)
and a WebSocket carrying JSON-RPC for results — matches Plans 1-3's
unified protocol.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review notes (for the implementer)

- **`pkg/js/testagent` distribution.** Resolved at codegen time via `testharness.LangTestagentPath("js")` — same pattern as Plan 3 android's testagent. No npm publish, no maven publish; the SNGL source tree is the source of truth.
- **WebSocket framing → `io.ReadWriteCloser`**. The `wsChannel` adapter appends `\n` to incoming frames and strips it from outgoing. The driver's `internal/testrpc.Reader` is `bufio.Scanner`-based; the adapter makes WebSocket look like a line-delimited stream.
- **Headless vs windowed Chromium.** Use headless via rod's `launcher.New().Headless(true)`. Don't display a real window during tests — slow, breaks CI.
- **`launcher.LookPath` caveat**. rod's `LookPath()` searches PATH for common Chromium binary names. If the host has Firefox or Safari only, this skips cleanly. Auto-download is **disabled** to keep `go tool verify` predictable.
- **No worktree.** Project memory says solo repo; commit to `main`.
- **Don't sweep pre-session drift.** Earlier sessions accumulated whitespace + small refactors that we've now cleaned. Don't introduce new noise.
