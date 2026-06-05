# Test architecture — Kotlin testagent + android transport — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring android into the unified test architecture under both Robolectric (default) and real-device adb (`--opt testRunner=device`) backends, with the kotlin testagent runtime mirroring the Go testagent's intrinsic surface and JSON-RPC protocol.

**Architecture:** Codegen emits dual-mode kotlin output (JUnit classes for `--opt test=true`; standalone kotlin `main` linked against the testagent for `sngl test`). Android platform's `TestLauncher` dispatches on the `testRunner` option: robolectric → gradle build a JVM JAR + spawn it with stdio JSON-RPC; device → gradle assembleDebug + adb install + adb forward + tcp JSON-RPC. Both transports share the same kotlin testagent runtime (`pkg/kotlin/testagent/`).

**Tech Stack:** Kotlin (JVM target for robolectric, Android target for device), gradle for builds, `org.json` (Android stdlib) or `kotlinx.serialization` for RPC codec, Robolectric 4.10+ with native graphics for JVM snapshot rendering, adb + emulator for device path.

**Spec ref:** `docs/superpowers/specs/2026-05-24-test-architecture-kotlin-android-design.md`

**No worktree** — per project memory, work directly on `main`.

---

## File map

- Create `pkg/kotlin/testagent/build.gradle.kts` — gradle module config for the testagent runtime.
- Create `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Rpc.kt` — JSON-RPC 2.0 codec.
- Create `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/T.kt` — `T` class with intrinsic surface (log/fail/skip/snapshot/etc.).
- Create `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Registry.kt` — test registry.
- Create `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Snapshot.kt` — `SnapshotCapture` interface + registration.
- Create `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/TestAgent.kt` — `main()` (stdio) + `startTcp(port)` entry points.
- Create `pkg/kotlin/testagent/src/test/kotlin/us/duckfam/sngl/testagent/TTest.kt` — unit tests for T, Registry, Snapshot.
- Modify `codegen/lang/kotlin/testlower.go` — add `TestEmitMode` + `LowerTestFile` (mirrors Plan 1's Go `LowerTestFile` for native vs agent).
- Modify `codegen/platform/android/android.sngl` — declare `testRunner` option.
- Modify `codegen/platform/android/android.go` — agent-mode emission block (mirrors Plan 1/2 bubbletea/fyne/gtk4). Per-testRunner emission paths.
- Modify `codegen/platform/android/scaffold.go` (or where MainActivity emits) — under testRunner=device + agent mode, inject `TestAgent.startTcp(intent.getIntExtra("SNGL_AGENT_PORT", 0))` into `MainActivity.onCreate`.
- Create `codegen/platform/android/launcher.go` — `LaunchTest` per testRunner.
- Modify `codegen/platform/android/run.go` — export or factor out `ensureDevice` for reuse by `launcher.go`.
- Delete `codegen/platform/android/runtests.go` and `runtests_js.go` after the new path is green (final cleanup task).
- Create `cmd/sngl/testdata/test_android_robolectric_state.txt`.
- Create `cmd/sngl/testdata/test_android_robolectric_snapshot.txt`.
- Create `cmd/sngl/testdata/test_android_device_state.txt`.
- Create `cmd/sngl/testdata/test_android_device_snapshot.txt`.

---

## Sequencing

Each task ends green; the suite stays shippable.

1. **Task 1**: `pkg/kotlin/testagent` — full runtime (RPC codec, T, Registry, Main, transports, snapshot intrinsic). Unit-tested via gradle in isolation.
2. **Task 2**: Kotlin codegen mode-aware `LowerTestFile` (mirrors Plan 1 Go).
3. **Task 3**: `testRunner` option declared in `android.sngl`. Android codegen reads it.
4. **Task 4**: Android codegen — agent-mode emission for `testRunner=robolectric`. Emits kotlin main + testagent registration + snapshot.kt.
5. **Task 5**: Android `TestLauncher` — robolectric branch. Gradle build kotlin JAR, spawn JVM, stdio JSON-RPC.
6. **Task 6**: `test_android_robolectric_state.txt` — end-to-end fixture covering state assertions.
7. **Task 7**: `test_android_robolectric_snapshot.txt` — snapshot golden round-trip under `robolectric/` subdir.
8. **Task 8**: Android codegen — agent-mode emission for `testRunner=device`. MainActivity hook + APK packaging.
9. **Task 9**: Android `TestLauncher` — device branch. Reuse `ensureDevice`, adb install/forward/am start, tcp JSON-RPC.
10. **Task 10**: Device fixtures + probe/skip wiring + delete `runtests.go`.

---

### Task 1: Kotlin testagent runtime

**Files:**
- Create: `pkg/kotlin/testagent/build.gradle.kts`
- Create: `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Rpc.kt`
- Create: `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/T.kt`
- Create: `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Registry.kt`
- Create: `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Snapshot.kt`
- Create: `pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/TestAgent.kt`
- Create: `pkg/kotlin/testagent/src/test/kotlin/us/duckfam/sngl/testagent/TTest.kt`

The whole runtime in one task — small surfaces, no platform integration yet. Mirrors `pkg/go/testagent` structure-for-structure.

- [ ] **Step 1: Module gradle config**

```kotlin
// pkg/kotlin/testagent/build.gradle.kts
plugins {
    kotlin("jvm") version "1.9.22"
    `maven-publish`
}

repositories { mavenCentral() }

dependencies {
    implementation("org.json:json:20231013")
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.1")
}

tasks.test { useJUnitPlatform() }

kotlin { jvmToolchain(17) }

publishing {
    publications {
        create<MavenPublication>("maven") {
            from(components["java"])
            groupId = "us.duckfam.sngl"
            artifactId = "testagent"
            version = "0.1.0"
        }
    }
}
```

- [ ] **Step 2: RPC codec — failing test first**

`pkg/kotlin/testagent/src/test/kotlin/us/duckfam/sngl/testagent/RpcTest.kt`:

```kotlin
package us.duckfam.sngl.testagent

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class RpcTest {
    @Test fun writesAndReadsNotification() {
        val out = ByteArrayOutputStream()
        val w = RpcWriter(out)
        w.notify("log", mapOf("test" to "T1", "msg" to "hi"))

        val r = RpcReader(ByteArrayInputStream(out.toByteArray()))
        val m = r.read()!!
        assertEquals("log", m.method)
        assertTrue(m.isNotification)
    }

    @Test fun writesRequestAndResponse() {
        val out = ByteArrayOutputStream()
        val w = RpcWriter(out)
        val id = w.request("list", emptyMap<String, Any>())
        w.respond(id, mapOf("tests" to listOf("a", "b")), null)

        val r = RpcReader(ByteArrayInputStream(out.toByteArray()))
        val req = r.read()!!
        assertEquals("list", req.method)
        assertNotNull(req.id)

        val resp = r.read()!!
        assertEquals("", resp.method)
        assertNotNull(resp.id)
    }

    @Test fun rejectsMalformedLine() {
        val r = RpcReader("not json\n".byteInputStream())
        try {
            r.read()
            error("expected exception")
        } catch (e: Exception) {
            // ok
        }
    }
}
```

- [ ] **Step 3: Run test, expect failure**

```bash
cd pkg/kotlin/testagent && gradle test --tests "*RpcTest*" 2>&1 | tail -10
```

Expected: compile error (`RpcReader`, `RpcWriter` unresolved).

- [ ] **Step 4: Implement Rpc.kt**

`pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Rpc.kt`:

```kotlin
package us.duckfam.sngl.testagent

import java.io.BufferedReader
import java.io.InputStream
import java.io.InputStreamReader
import java.io.OutputStream
import java.util.concurrent.atomic.AtomicLong
import org.json.JSONObject

data class RpcMessage(
    val method: String,
    val id: Long?,
    val params: JSONObject?,
    val result: JSONObject?,
    val error: RpcError?,
) {
    val isNotification: Boolean get() = method.isNotEmpty() && id == null
    val isResponse: Boolean get() = method.isEmpty() && id != null
}

data class RpcError(val code: Int, val message: String, val data: Any? = null)

class RpcReader(input: InputStream) {
    private val reader = BufferedReader(InputStreamReader(input, Charsets.UTF_8))

    fun read(): RpcMessage? {
        val line = reader.readLine() ?: return null
        if (line.isBlank()) return read()
        val obj = JSONObject(line)
        val method = obj.optString("method", "")
        val id = if (obj.has("id") && !obj.isNull("id")) obj.getLong("id") else null
        val params = if (obj.has("params") && !obj.isNull("params")) obj.getJSONObject("params") else null
        val result = if (obj.has("result") && !obj.isNull("result")) obj.getJSONObject("result") else null
        val error = if (obj.has("error") && !obj.isNull("error")) {
            val e = obj.getJSONObject("error")
            RpcError(e.getInt("code"), e.getString("message"))
        } else null
        return RpcMessage(method, id, params, result, error)
    }
}

class RpcWriter(private val output: OutputStream) {
    private val seq = AtomicLong(0)
    private val lock = Any()

    fun notify(method: String, params: Map<String, Any?>) {
        val obj = JSONObject()
            .put("jsonrpc", "2.0")
            .put("method", method)
            .put("params", JSONObject(params))
        send(obj)
    }

    fun request(method: String, params: Map<String, Any?>): Long {
        val id = seq.incrementAndGet()
        val obj = JSONObject()
            .put("jsonrpc", "2.0")
            .put("id", id)
            .put("method", method)
            .put("params", JSONObject(params))
        send(obj)
        return id
    }

    fun respond(id: Long, result: Map<String, Any?>?, error: RpcError?) {
        require(result != null || error != null)
        val obj = JSONObject().put("jsonrpc", "2.0").put("id", id)
        if (error != null) {
            obj.put("error", JSONObject(mapOf("code" to error.code, "message" to error.message)))
        } else {
            obj.put("result", JSONObject(result!!))
        }
        send(obj)
    }

    private fun send(obj: JSONObject) {
        synchronized(lock) {
            val bytes = (obj.toString() + "\n").toByteArray(Charsets.UTF_8)
            output.write(bytes)
            output.flush()
        }
    }
}
```

- [ ] **Step 5: Run, confirm green**

```bash
cd pkg/kotlin/testagent && gradle test --tests "*RpcTest*" 2>&1 | tail -10
```

Expected: 3 tests pass.

- [ ] **Step 6: T + Registry — failing tests first**

`pkg/kotlin/testagent/src/test/kotlin/us/duckfam/sngl/testagent/TTest.kt`:

```kotlin
package us.duckfam.sngl.testagent

import java.io.ByteArrayOutputStream
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertTrue

class TTest {
    @AfterEach fun reset() {
        Registry.reset()
        Snapshots.reset()
    }

    @Test fun logEmitsNotification() {
        val out = ByteArrayOutputStream()
        val t = T("T1", RpcWriter(out))
        t.log("hi")
        val line = out.toString().trim()
        assertTrue(line.contains("\"method\":\"log\""))
        assertTrue(line.contains("\"msg\":\"hi\""))
    }

    @Test fun failNowAborts() {
        val t = T("T1", RpcWriter(ByteArrayOutputStream()))
        assertFails { t.failNow() }
        assertTrue(t.failed)
    }

    @Test fun errorMarksAndLogs() {
        val out = ByteArrayOutputStream()
        val t = T("T1", RpcWriter(out))
        t.error("oops")
        assertTrue(t.failed)
        assertTrue(out.toString().contains("\"msg\":\"oops\""))
    }

    @Test fun registryEnumeratesTests() {
        Registry.register("foo") { it.log("ran") }
        Registry.register("bar") { it.log("ran") }
        assertEquals(listOf("bar", "foo"), Registry.list())
    }
}
```

- [ ] **Step 7: Implement T.kt + Registry.kt + Snapshot.kt**

`pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/T.kt`:

```kotlin
package us.duckfam.sngl.testagent

import java.util.Base64

class AbortSentinel : RuntimeException()

class T(val name: String, internal val rpc: RpcWriter) {
    var failed = false
        internal set
    var skipped = false
        internal set

    fun log(msg: String) { rpc.notify("log", mapOf("test" to name, "msg" to msg)) }
    fun fail() { failed = true; rpc.notify("markFail", mapOf("test" to name)) }
    fun failNow(): Nothing { fail(); throw AbortSentinel() }
    fun skip(reason: String): Nothing {
        skipped = true
        rpc.notify("markSkip", mapOf("test" to name, "reason" to reason))
        throw AbortSentinel()
    }
    fun error(msg: String) { log(msg); fail() }
    fun fatal(msg: String): Nothing { error(msg); throw AbortSentinel() }
    fun assertTrue(b: Boolean, msg: String) { if (!b) fatal(msg) }
    fun wait(ms: Long) { Thread.sleep(ms) }

    fun snapshot(snapshotName: String) {
        val capture = Snapshots.capture()
        if (capture == null) {
            error("snapshot \"$snapshotName\": no capture registered for this platform")
            return
        }
        val (mime, bytes) = try { capture() } catch (e: Throwable) {
            error("snapshot \"$snapshotName\": capture: ${e.message}")
            return
        }
        val params = mapOf(
            "test" to name,
            "name" to snapshotName,
            "mime" to mime,
            "bytes" to Base64.getEncoder().encodeToString(bytes),
        )
        val id = rpc.request("snapshotAssert", params)
        val resp = Pending.await(id) ?: run {
            error("snapshot \"$snapshotName\": timeout")
            return
        }
        if (resp.error != null) { error("snapshot \"$snapshotName\": driver: ${resp.error.message}"); return }
        val r = resp.result ?: run { error("snapshot \"$snapshotName\": empty response"); return }
        val pass = r.optBoolean("pass", false)
        val diff = r.optString("diff", "")
        if (!pass) error("snapshot \"$snapshotName\" mismatch:\n$diff")
    }
}

object Pending {
    private val pending = mutableMapOf<Long, java.util.concurrent.SynchronousQueue<RpcMessage>>()
    private val lock = Any()

    fun register(id: Long): java.util.concurrent.SynchronousQueue<RpcMessage> {
        val q = java.util.concurrent.SynchronousQueue<RpcMessage>()
        synchronized(lock) { pending[id] = q }
        return q
    }
    fun await(id: Long, timeoutMs: Long = 30_000): RpcMessage? {
        val q = synchronized(lock) { pending[id] }
            ?: return null
        return try {
            q.poll(timeoutMs, java.util.concurrent.TimeUnit.MILLISECONDS)
        } finally {
            synchronized(lock) { pending.remove(id) }
        }
    }
    fun deliver(m: RpcMessage) {
        if (m.id == null) return
        val q = synchronized(lock) { pending[m.id] } ?: return
        q.offer(m)
    }
}
```

Replace `T.snapshot`'s `Pending.register` call: the request loop registers via `Pending.register(id)` before submitting. Adjust:

```kotlin
fun snapshot(snapshotName: String) {
    val capture = Snapshots.capture()
    if (capture == null) { error(...); return }
    val (mime, bytes) = try { capture() } catch (e: Throwable) { error(...); return }
    val params = mapOf(...)
    // Register pending BEFORE sending the request, so the response
    // can't arrive before the queue is set up.
    val id = synchronized(rpc) {
        val tmpId = rpc.request("snapshotAssert", params)
        Pending.register(tmpId)
        tmpId
    }
    val resp = Pending.await(id) ?: ...
    ...
}
```

Actually re-examine the race: `Pending.register(id)` needs to happen before the response is read on the receiver side. Since the request and registration both happen on the sender thread, but responses are read by the receiver thread, register first, then write — never write before registering. Restructure:

```kotlin
fun snapshot(snapshotName: String) {
    val capture = Snapshots.capture() ?: run { error(...); return }
    val (mime, bytes) = try { capture() } catch (e: Throwable) { error(...); return }

    // Allocate id and register BEFORE writing the request.
    val id = rpc.allocId()
    Pending.register(id)
    rpc.requestWithId(id, "snapshotAssert", mapOf(
        "test" to name,
        "name" to snapshotName,
        "mime" to mime,
        "bytes" to Base64.getEncoder().encodeToString(bytes),
    ))
    val resp = Pending.await(id) ?: run { error("snapshot \"$snapshotName\": timeout"); return }
    ...
}
```

Add to `RpcWriter`:

```kotlin
fun allocId(): Long = seq.incrementAndGet()

fun requestWithId(id: Long, method: String, params: Map<String, Any?>) {
    val obj = JSONObject()
        .put("jsonrpc", "2.0")
        .put("id", id)
        .put("method", method)
        .put("params", JSONObject(params))
    send(obj)
}
```

`Registry.kt`:

```kotlin
package us.duckfam.sngl.testagent

object Registry {
    private val tests = mutableMapOf<String, (T) -> Unit>()

    fun register(name: String, fn: (T) -> Unit) { synchronized(this) { tests[name] = fn } }
    fun list(): List<String> = synchronized(this) { tests.keys.sorted() }
    fun get(name: String): ((T) -> Unit)? = synchronized(this) { tests[name] }
    internal fun reset() { synchronized(this) { tests.clear() } }
}
```

`Snapshot.kt`:

```kotlin
package us.duckfam.sngl.testagent

typealias SnapshotCapture = () -> Pair<String, ByteArray>

object Snapshots {
    @Volatile private var capture: SnapshotCapture? = null
    @Volatile var namePrefix: String = ""

    /**
     * Register the per-platform capture function plus a name prefix.
     * The prefix is prepended to every snapshot name before the
     * snapshotAssert RPC is sent, so goldens land under
     * <fixture>.snapshots/<prefix><name>.<ext>. Used by android codegen
     * to scope goldens per testRunner ("robolectric/" or "device/");
     * other platforms pass "" (no prefix).
     */
    fun register(prefix: String, fn: SnapshotCapture) {
        capture = fn
        namePrefix = prefix
    }
    fun capture(): SnapshotCapture? = capture
    internal fun reset() { capture = null; namePrefix = "" }
}
```

And update `T.snapshot` (in T.kt) to apply the prefix:

```kotlin
fun snapshot(snapshotName: String) {
    val capture = Snapshots.capture() ?: run { error(...); return }
    val (mime, bytes) = try { capture() } catch (e: Throwable) { error(...); return }
    val id = rpc.allocId()
    Pending.register(id)
    rpc.requestWithId(id, "snapshotAssert", mapOf(
        "test" to name,
        "name" to (Snapshots.namePrefix + snapshotName),
        "mime" to mime,
        "bytes" to Base64.getEncoder().encodeToString(bytes),
    ))
    // ... await + handle response ...
}
```

- [ ] **Step 8: Run tests**

```bash
cd pkg/kotlin/testagent && gradle test 2>&1 | tail -10
```

Expected: all green (RpcTest 3 + TTest 4).

- [ ] **Step 9: TestAgent.kt — main + startTcp**

`pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/TestAgent.kt`:

```kotlin
package us.duckfam.sngl.testagent

import java.io.InputStream
import java.io.OutputStream
import java.net.ServerSocket

fun main() {
    val port = System.getenv("SNGL_AGENT_PORT")?.toIntOrNull()
    if (port != null) {
        connectAndDrive(port)
    } else {
        runDriver(System.`in`, System.out)
    }
}

fun startTcp(port: Int) {
    // Listener for the device path: sngl test's process connects to us
    // via adb-forwarded localhost:port.
    val server = ServerSocket(port)
    val socket = server.accept()
    runDriver(socket.getInputStream(), socket.getOutputStream())
}

private fun connectAndDrive(port: Int) {
    val socket = java.net.Socket("127.0.0.1", port)
    runDriver(socket.getInputStream(), socket.getOutputStream())
}

private fun runDriver(input: InputStream, output: OutputStream) {
    val reader = RpcReader(input)
    val writer = RpcWriter(output)

    while (true) {
        val msg = reader.read() ?: return
        if (msg.isResponse) { Pending.deliver(msg); continue }
        if (msg.id == null) continue
        when (msg.method) {
            "list" -> {
                val tests = Registry.list()
                writer.respond(msg.id, mapOf("tests" to tests), null)
            }
            "run" -> {
                val filter = msg.params?.optString("filter", "") ?: ""
                runFiltered(writer, filter)
                writer.respond(msg.id, emptyMap(), null)
            }
            "cancel" -> {
                writer.respond(msg.id, emptyMap(), null)
                return
            }
            else -> writer.respond(msg.id, null, RpcError(-32601, "method not found: ${msg.method}"))
        }
    }
}

private fun runFiltered(writer: RpcWriter, filter: String) {
    var passed = 0; var failed = 0; var skipped = 0
    for (name in Registry.list()) {
        if (filter.isNotEmpty() && !name.contains(filter)) continue
        when (runOne(writer, name)) {
            "pass" -> passed++; "fail" -> failed++; "skip" -> skipped++
        }
    }
    writer.notify("runComplete", mapOf("passed" to passed, "failed" to failed, "skipped" to skipped))
}

private fun runOne(writer: RpcWriter, name: String): String {
    val fn = Registry.get(name) ?: run {
        writer.notify("testEnd", mapOf("test" to name, "status" to "fail", "durationMs" to 0))
        return "fail"
    }
    val t = T(name, writer)
    writer.notify("testStart", mapOf("test" to name))
    val start = System.currentTimeMillis()
    try { fn(t) } catch (e: AbortSentinel) { /* ok */ }
    catch (e: Throwable) {
        t.failed = true
        writer.notify("log", mapOf("test" to name, "msg" to "exception: ${e.message}"))
    }
    val status = when { t.skipped -> "skip"; t.failed -> "fail"; else -> "pass" }
    writer.notify("testEnd", mapOf("test" to name, "status" to status, "durationMs" to (System.currentTimeMillis() - start).toInt()))
    return status
}
```

- [ ] **Step 10: Verify build**

```bash
cd pkg/kotlin/testagent && gradle build 2>&1 | tail -5
```

Expected: build succeeds.

- [ ] **Step 11: Commit**

```bash
git add pkg/kotlin/testagent/
git commit -m "$(cat <<'EOF'
pkg/kotlin/testagent: Kotlin testagent runtime

Mirrors pkg/go/testagent: T class with log/fail/skip/error/fatal/
assertTrue/wait/snapshot intrinsics, Registry of test functions, RPC
codec (line-delimited JSON-RPC 2.0), main() entry point selecting
stdio or TCP transport, startTcp(port) entry for device path.

Snapshot intrinsic registers a pending response queue, submits
snapshotAssert via RPC, awaits the driver's pass/diff response.
AbortSentinel routes failNow/skip control flow via exceptions.

Unit-tested in isolation via gradle. No platform integration yet.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Kotlin codegen — LowerTestFile mode-aware

**Files:**
- Modify: `codegen/lang/kotlin/testlower.go`
- Modify: `codegen/lang/kotlin/testlower_test.go` (may not exist; create alongside)

Mirrors Plan 1 Task 5 for Go. Add `TestEmitMode` enum and `LowerTestFile` that emits native JUnit class form OR agent-mode kotlin file with RegisterTest init.

- [ ] **Step 1: Add the mode enum and LowerTestFile**

Append to `codegen/lang/kotlin/testlower.go`:

```go
// TestEmitMode selects how LowerTestFile wraps per-test bodies.
type TestEmitMode int

const (
	// TestEmitNative produces a JUnit-style class with @Test methods,
	// suitable for inclusion in the user's gradle test sourceset.
	TestEmitNative TestEmitMode = iota
	// TestEmitAgent produces a Kotlin source file that calls
	// Registry.register for each test plus a main() that calls
	// TestAgent.main(). Linked against pkg/kotlin/testagent at build time.
	TestEmitAgent
)

// LowerTestFile produces the entire source of a generated test file.
// Body lowering is identical across modes; the wrapper differs.
//
// Native: package + import JUnit + class MainScreenTest { @Test fun testFoo() { ... } }
// Agent:  package + import testagent + fun testFoo(t: T) { ... } + init { Registry.register(...) }
func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string, methodFields map[string]bool, mode TestEmitMode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	switch mode {
	case TestEmitNative:
		b.WriteString("import org.junit.Test\n")
		b.WriteString("import org.junit.Assert.assertTrue\n\n")
		b.WriteString("class MainScreenTest {\n")
	case TestEmitAgent:
		b.WriteString("import us.duckfam.sngl.testagent.T\n")
		b.WriteString("import us.duckfam.sngl.testagent.Registry\n\n")
	}

	for i, fn := range fns {
		suffix := suffixes[i]
		if mode == TestEmitNative {
			fmt.Fprintf(&b, "    @Test fun test%s() {\n", suffix)
			b.WriteString("        val c = newTestComponent()\n")
			for _, line := range lowerTestBody(fn, methodFields, true) {
				fmt.Fprintf(&b, "        %s\n", line)
			}
			b.WriteString("    }\n\n")
		} else {
			fmt.Fprintf(&b, "fun test%s(t: T) {\n", suffix)
			b.WriteString("    val c = newTestComponent()\n")
			b.WriteString("    setCurrentTestModel(c)\n")
			for _, line := range lowerTestBody(fn, methodFields, false) {
				fmt.Fprintf(&b, "    %s\n", line)
			}
			b.WriteString("}\n\n")
		}
	}

	switch mode {
	case TestEmitNative:
		b.WriteString("}\n")
	case TestEmitAgent:
		b.WriteString("private fun registerAll() {\n")
		for i := range fns {
			suffix := suffixes[i]
			fmt.Fprintf(&b, "    Registry.register(%q, ::test%s)\n", suffix, suffix)
		}
		b.WriteString("}\n\n")
		b.WriteString("val __sngl_test_init: Unit = registerAll()\n")
	}

	return b.String()
}

// lowerTestBody returns the lowered test body lines. When isNativeJUnit is
// true the body uses org.junit.Assert idioms (assertTrue from JUnit static
// import); when false it uses the testagent T methods (`t.assertTrue(...)`).
// For now the difference is only the assertion entry point — most other
// statements lower identically since both T and JUnit share a Kotlin
// runtime.
func lowerTestBody(fn *ir.Func, methodFields map[string]bool, isNativeJUnit bool) []string {
	// Existing LowerTestFunc walks fn.Block; share that machinery.
	raw := LowerTestFunc(fn, "", methodFields)
	// Strip the wrapper "fun testX() {\n ... \n}" produced by LowerTestFunc
	// and yield the body lines. Simple: take lines between the first '{'
	// and the last '}'.
	open := strings.Index(raw, "{")
	closeIdx := strings.LastIndex(raw, "}")
	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return nil
	}
	body := raw[open+1 : closeIdx]
	lines := strings.Split(strings.TrimSpace(body), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " \t")
		if isNativeJUnit {
			// No transform yet — assertTrue is the only assertion form and
			// it exists in both surfaces.
			out = append(out, trimmed)
		} else {
			// Agent-mode: `t.assertTrue(...)` already matches.
			out = append(out, trimmed)
		}
	}
	return out
}
```

The `lowerTestBody` parses the existing `LowerTestFunc` output and strips its wrapper. Implementer: verify that the existing `LowerTestFunc` is callable in this way (it returns a fully-wrapped function string); if it's tightly coupled, refactor it to extract a `lowerTestStatements(fn, scope) []string` helper that both `LowerTestFunc` and `LowerTestFile` call, parallel to Plan 1 Task 5's Go side.

- [ ] **Step 2: Add tests**

`codegen/lang/kotlin/testlower_test.go` (append or create):

```go
func TestKotlinLowerTestFile_agentModeEmitsRegisterAll(t *testing.T) {
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
	out := LowerTestFile("us.duckfam.sngl.app", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitAgent)
	if !strings.Contains(out, "import us.duckfam.sngl.testagent.T") {
		t.Errorf("agent mode missing T import:\n%s", out)
	}
	if !strings.Contains(out, "fun testFoo(t: T)") {
		t.Errorf("agent func signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Registry.register(\"Foo\", ::testFoo)") {
		t.Errorf("RegisterTest call missing:\n%s", out)
	}
}

func TestKotlinLowerTestFile_nativeModeEmitsJUnitClass(t *testing.T) {
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
	out := LowerTestFile("us.duckfam.sngl.app", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitNative)
	if !strings.Contains(out, "import org.junit.Test") {
		t.Errorf("native mode missing JUnit import:\n%s", out)
	}
	if !strings.Contains(out, "@Test fun testFoo()") {
		t.Errorf("native @Test missing:\n%s", out)
	}
	if strings.Contains(out, "Registry.register") {
		t.Errorf("native mode should not call Registry.register:\n%s", out)
	}
}
```

- [ ] **Step 3: Run**

```bash
go test ./codegen/lang/kotlin/ -run TestKotlinLowerTestFile -count=1 -v
```

Expected: both pass.

```bash
go test ./codegen/lang/kotlin/ -count=1 2>&1 | tail -5
go build ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/kotlin/testlower.go codegen/lang/kotlin/testlower_test.go
git commit -m "$(cat <<'EOF'
codegen/lang/kotlin: add LowerTestFile with native/agent modes

Native mode emits a JUnit @Test class for the user's gradle test
sourceset. Agent mode emits standalone fun testX(t: T) declarations
plus a Registry.register init.

Body lowering shared with LowerTestFunc — single source of truth
for what a SNGL test statement becomes in Kotlin.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Declare `testRunner` option

**Files:**
- Modify: `codegen/platform/android/android.sngl`
- (Possibly) modify: `codegen/platform/android/android.go` Config struct.

Add the schema field; downstream codegen reads it.

- [ ] **Step 1: Add the field to android.sngl**

Edit `codegen/platform/android/android.sngl`. Find the existing `struct Options { ... }`. Append:

```sngl
// Which Android runtime to test against.
//   "robolectric": JVM simulation, fast, no emulator (default).
//   "device":      APK on a real emulator/device via adb.
testRunner
string = "robolectric"
```

- [ ] **Step 2: Ensure the Config struct exposes it**

In `codegen/platform/android/android.go`, find the `Config` struct (likely declared near the top of the file or in `android_test.go`). Add the field:

```go
type Config struct {
	// ... existing fields ...
	TestRunner string `sngl:"testRunner"`
}
```

In `Config.withDefaults()`:

```go
if cfg.TestRunner == "" {
	cfg.TestRunner = "robolectric"
}
```

- [ ] **Step 3: Confirm parse + check**

```bash
go test ./internal/checker/... -count=1 2>&1 | tail -5
go test ./codegen/platform/android/ -count=1 2>&1 | tail -5
go build ./...
```

Expected: clean.

- [ ] **Step 4: Manual smoke**

```bash
go install ./cmd/sngl/
mkdir -p /tmp/sngl-tr && cd /tmp/sngl-tr
cat > app.sngl <<'EOF'
output { kotlin { android } }
component main { text(value="hi") }
EOF
sngl generate --lang=kotlin --platform=android --opt testRunner=robolectric --out out app.sngl
sngl generate --lang=kotlin --platform=android --opt testRunner=device --out out2 app.sngl
echo "both invocations exit 0"
```

Expected: both commands succeed. (The codegen doesn't yet do anything different per testRunner — that lands in Tasks 4 and 8 — but the option must be accepted.)

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/android/android.sngl codegen/platform/android/android.go
git commit -m "$(cat <<'EOF'
codegen/platform/android: declare testRunner option

robolectric (default) | device. Read by android codegen + TestLauncher
in subsequent commits. CLI accepts --opt testRunner=value today;
codegen behavior switches in the next commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Android codegen — agent emission for testRunner=robolectric

**Files:**
- Modify: `codegen/platform/android/android.go`

Emit a kotlin testagent main + Registry.register file + Robolectric snapshot capture file when `testRunner=robolectric` and `testMode=agent`.

- [ ] **Step 1: Locate android's Generate**

```bash
grep -n 'func .*Generator. Generate' codegen/platform/android/android.go
```

Read the function. Find where MainScreen.kt is emitted (search "MainScreen.kt"). The new agent-mode emission lands at the end of Generate, similar to bubbletea/fyne/gtk4.

- [ ] **Step 2: Add the agent-mode emission block**

At the end of `Generate`, just before the final `return nil`:

```go
if codegen.OptionString(req.Options, "testMode") == "agent" {
	codegen.SetOptionField(req.Options, "main", false)
}

if codegen.OptionBool(req.Options, "test") {
	testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
	if len(testFns) > 0 {
		mode := kotlin.TestEmitNative
		if codegen.OptionString(req.Options, "testMode") == "agent" {
			mode = kotlin.TestEmitAgent
		}
		pkgName := "us.duckfam.sngl.app"
		src := kotlin.LowerTestFile(pkgName, testFns, suffixes, methodFields, mode)
		fname := "TestAgentRunner.kt"
		if mode == kotlin.TestEmitNative {
			// Native mode targets test sourceset (robolectric) or
			// androidTest sourceset (device); split per testRunner.
			tr := codegen.OptionString(req.Options, "testRunner")
			if tr == "device" {
				fname = "app/src/androidTest/kotlin/us/duckfam/sngl/app/MainScreenTest.kt"
			} else {
				fname = "app/src/test/kotlin/us/duckfam/sngl/app/MainScreenTest.kt"
			}
		}
		if err := writeAndroidSourceFile(sink, fname, req.Lang, ktOpts, src); err != nil {
			return err
		}

		if mode == kotlin.TestEmitAgent {
			agentMain := `package us.duckfam.sngl.app

import us.duckfam.sngl.testagent.TestAgent

fun main() { us.duckfam.sngl.testagent.main() }
`
			if err := writeAndroidSourceFile(sink, "AgentMain.kt", req.Lang, ktOpts, []byte(agentMain)); err != nil {
				return err
			}

			tr := codegen.OptionString(req.Options, "testRunner")
			if tr == "robolectric" || tr == "" {
				snapshotSrc := robolectricSnapshotCaptureKotlin(pkgName)
				if err := writeAndroidSourceFile(sink, "RobolectricSnapshot.kt", req.Lang, ktOpts, []byte(snapshotSrc)); err != nil {
					return err
				}
			}
			// Device testRunner snapshot capture lands in Task 8.

			// currentTestModel accessor — codegen-specific because the
			// Model type varies per emission. For now MainScreenState
			// is the hoisted state from android codegen.
			accessorSrc := `package us.duckfam.sngl.app

var __sngl_currentModel: MainScreenState? = null

fun setCurrentTestModel(m: MainScreenState) { __sngl_currentModel = m }
fun currentTestModel(): MainScreenState = __sngl_currentModel!!

fun newTestComponent(): MainScreenState = MainScreenState()
`
			if err := writeAndroidSourceFile(sink, "TestModelAccessor.kt", req.Lang, ktOpts, []byte(accessorSrc)); err != nil {
				return err
			}
		}
	}
}
```

Adjust the package name and Model type to match what android's Generate actually produces. Verify by running `sngl generate --lang=kotlin --platform=android --out out app.sngl` against a small fixture and reading the emitted `MainScreen.kt` for the actual `package` and `MainScreenState` (or equivalent) names. Substitute consistently.

- [ ] **Step 3: Add `robolectricSnapshotCaptureKotlin`**

Add helper in `codegen/platform/android/android.go` (or a new file `codegen/platform/android/snapshot_emit.go`):

```go
func robolectricSnapshotCaptureKotlin(pkg string) []byte {
	return []byte(`package ` + pkg + `

import android.graphics.Bitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.graphics.asAndroidBitmap
import org.robolectric.annotation.GraphicsMode
import us.duckfam.sngl.testagent.Snapshots
import java.io.ByteArrayOutputStream

@GraphicsMode(GraphicsMode.Mode.NATIVE)
object RobolectricSnapshotCapture {
    var rule: ComposeContentTestRule? = null

    init {
        Snapshots.register {
            val r = rule ?: error("snapshot: ComposeTestRule not set")
            val img = r.onRoot().captureToImage()
            val bmp = img.asAndroidBitmap()
            val out = ByteArrayOutputStream()
            bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
            "image/png" to out.toByteArray()
        }
    }
}
`)
}
```

The `Snapshots.register` callback expects to find `RobolectricSnapshotCapture.rule` set; the testagent's harness wires up the ComposeContentTestRule during launcher start (Task 5).

Verified during implementation: ComposeContentTestRule's API surface for `captureToImage` and `onRoot` may differ across Compose versions; pin to whatever the existing android scaffold uses (read `codegen/platform/android/scaffold.go`'s gradle Compose version pins).

- [ ] **Step 4: Build + suite**

```bash
go build ./...
go test ./codegen/platform/android/ -count=1 2>&1 | tail -5
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 5: Smoke**

```bash
mkdir -p /tmp/sngl-a && cd /tmp/sngl-a
cat > app.sngl <<'EOF'
output { kotlin { android } }
component greeter {
    var name = "world"
    text(value=name)
}
func testFoo(t Test, c greeter) { t.assert(c.name == "world") }
EOF
sngl generate --lang=kotlin --platform=android --opt test=true --opt testMode=agent --opt testRunner=robolectric --out out .
ls out/
cat out/TestAgentRunner.kt
cat out/AgentMain.kt
cat out/RobolectricSnapshot.kt
```

Expected: all four files exist, well-formed Kotlin source.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/android/
git commit -m "$(cat <<'EOF'
codegen/platform/android: agent-mode emission for testRunner=robolectric

Generate emits TestAgentRunner.kt (test funcs + Registry.register),
AgentMain.kt (calls testagent.main()), RobolectricSnapshot.kt (Compose
capture wired into Snapshots), and TestModelAccessor.kt (currentTestModel
plumbing) when Options.test && testMode=agent && testRunner=robolectric.

Native-mode (--opt test=true without testMode) lands MainScreenTest.kt
in app/src/test/kotlin/ for robolectric or app/src/androidTest/kotlin/
for device.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Android TestLauncher — robolectric branch

**Files:**
- Create: `codegen/platform/android/launcher.go`

Implement `(*Generator).LaunchTest` with the robolectric path: synthesize a gradle JVM project (not an Android APK), build, spawn the JVM, stdio JSON-RPC. Device branch is empty/stubbed; Task 9 wires it.

- [ ] **Step 1: Create launcher.go**

```go
//go:build !js

package android

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LaunchTest implements codegen.TestLauncher for android. Dispatches on
// the testRunner option: robolectric → JVM build + stdio JSON-RPC;
// device → APK + adb forward + tcp JSON-RPC.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	runner := codegen.OptionString(opts, "testRunner")
	if runner == "" {
		runner = "robolectric"
	}
	switch runner {
	case "robolectric":
		return g.launchRobolectric(ctx, dir, lang, opts)
	case "device":
		return g.launchDevice(ctx, dir, lang, opts)
	default:
		return nil, nil, fmt.Errorf("unknown testRunner %q", runner)
	}
}

func (g *Generator) launchRobolectric(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if !javaFound() {
		return nil, nil, &codegen.SkipError{Reason: "JDK 17+ not on PATH"}
	}
	if err := writeRobolectricGradleProject(dir); err != nil {
		return nil, nil, err
	}
	gradle, err := exec.LookPath("gradle")
	if err != nil {
		if err := writeGradleWrapper(dir); err != nil {
			return nil, nil, fmt.Errorf("no gradle and wrapper synth failed: %w", err)
		}
		gradle = filepath.Join(dir, "gradlew")
	}
	var buildOut bytes.Buffer
	bld := exec.CommandContext(ctx, gradle, ":app:installDist", "--no-daemon")
	bld.Dir = dir
	bld.Stdout = &buildOut
	bld.Stderr = &buildOut
	if err := bld.Run(); err != nil {
		fmt.Fprint(os.Stderr, buildOut.String())
		return nil, nil, fmt.Errorf("gradle installDist: %w", err)
	}

	binPath := filepath.Join(dir, "app", "build", "install", "app", "bin", "app")
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
		return nil, nil, err
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

func (g *Generator) launchDevice(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	return nil, nil, fmt.Errorf("testRunner=device: not yet implemented")
}

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

func writeRobolectricGradleProject(dir string) error {
	settings := []byte(`rootProject.name = "snglroot"
include(":app")
`)
	if err := os.WriteFile(filepath.Join(dir, "settings.gradle.kts"), settings, 0o644); err != nil {
		return err
	}
	rootBuild := []byte(`plugins { kotlin("jvm") version "1.9.22" apply false }
`)
	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), rootBuild, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		return err
	}
	appBuild := []byte(`plugins {
    kotlin("jvm") version "1.9.22"
    application
}

repositories { mavenCentral(); google() }

dependencies {
    implementation("us.duckfam.sngl:testagent:0.1.0")
    implementation("androidx.compose.ui:ui:1.6.0")
    implementation("androidx.compose.material:material:1.6.0")
    implementation("org.robolectric:robolectric:4.11.1")
    implementation("androidx.compose.ui:ui-test:1.6.0")
    implementation("androidx.compose.ui:ui-test-junit4:1.6.0")
}

application { mainClass.set("us.duckfam.sngl.app.AgentMainKt") }

kotlin { jvmToolchain(17) }
`)
	if err := os.WriteFile(filepath.Join(dir, "app", "build.gradle.kts"), appBuild, 0o644); err != nil {
		return err
	}
	// The emitted .kt files were placed at the project root by Generate;
	// move them into app/src/main/kotlin/us/duckfam/sngl/app/.
	return moveEmittedKotlinIntoAppSrc(dir)
}

func moveEmittedKotlinIntoAppSrc(dir string) error {
	dst := filepath.Join(dir, "app", "src", "main", "kotlin", "us", "duckfam", "sngl", "app")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".kt") {
			continue
		}
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	return nil
}

func writeGradleWrapper(dir string) error {
	// Synthesize a gradle wrapper. Easiest: shell `gradle wrapper` via a
	// host gradle if available; if not, fail. This step is only reached
	// when system gradle isn't on PATH AND the host doesn't have one.
	return fmt.Errorf("gradle wrapper synth not implemented; install gradle on PATH")
}
```

The `moveEmittedKotlinIntoAppSrc` step is a hack: it's simpler for codegen to emit at the dir root and let the launcher relocate, vs threading a per-platform "where to write" path through `writeAndroidSourceFile`. Alternative: have android's `Generate` emit directly into the gradle layout. That's cleaner but requires the gradle project layout to be predetermined during codegen. Decide during implementation; the post-emit move works in the meantime.

- [ ] **Step 2: Build + run a smoke**

```bash
go install ./cmd/sngl/
```

Run the actual test path against a fixture (requires JDK 17+ and gradle on PATH):

```bash
mkdir -p /tmp/sngl-rob && cd /tmp/sngl-rob
cat > app.sngl <<'EOF'
output { kotlin { android } }
component greeter {
    var name = "world"
    text(value=name)
}
func testFoo(t Test, c greeter) { t.assert(c.name == "world") }
EOF
SNGL_KEEP_TEST_DIR=1 sngl test --platform=android --opt testRunner=robolectric .
ls /tmp/sngl-test-*/app/src/main/kotlin/us/duckfam/sngl/app/
```

Expected: PASS line; the directory contains the emitted `.kt` files moved into the gradle layout.

If gradle isn't on PATH or fetching deps fails, the test will fail with a clear gradle error — that's acceptable for this task. CI configuration will provide gradle.

- [ ] **Step 3: Confirm suite**

```bash
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/android/launcher.go
git commit -m "$(cat <<'EOF'
codegen/platform/android: TestLauncher robolectric branch

LaunchTest dispatches on testRunner. Robolectric path:
- Synth a gradle JVM application project.
- Move emitted .kt files into the gradle source layout.
- gradle :app:installDist → produces a runnable JVM script.
- exec the script with stdin/stdout connected as the JSON-RPC channel.

Device branch is stubbed; lands in Task 9. Clean SkipError when
JDK 17+ missing.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: `test_android_robolectric_state.txt` fixture

**Files:**
- Create: `cmd/sngl/testdata/test_android_robolectric_state.txt`

End-to-end fixture covering state-only assertions (no snapshot).

- [ ] **Step 1: Write the fixture**

```
# Robolectric end-to-end: state-only assertions.

[!exec:java] skip 'JDK required'
[!exec:gradle] skip 'gradle required for android tests'

sngl test --platform=android --opt testRunner=robolectric app.sngl
stdout 'PASS'

-- app.sngl --
output { kotlin { android } }

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

- [ ] **Step 2: Run**

```bash
go test ./cmd/sngl/ -run TestScript/test_android_robolectric_state -count=1 -v 2>&1 | tail -30
```

Expected: PASS on hosts with JDK + gradle. Skip on others.

The first run will be slow (gradle downloads). Subsequent runs reuse the gradle cache (typically `~/.gradle/caches`).

- [ ] **Step 3: Commit**

```bash
git add cmd/sngl/testdata/test_android_robolectric_state.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: end-to-end fixture for android robolectric state tests

Verifies sngl test --platform=android --opt testRunner=robolectric
runs SNGL tests via the kotlin testagent in a Robolectric JVM, returns
PASS, and the agent's RPC stream survives a multi-test session.

Skips when JDK or gradle are unavailable on the host.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: `test_android_robolectric_snapshot.txt` fixture

**Files:**
- Create: `cmd/sngl/testdata/test_android_robolectric_snapshot.txt`

Snapshot fixture; goldens under `<fixture>.snapshots/robolectric/`.

- [ ] **Step 1: Wire the snapshot name prefix in agent emission**

Modify the agent-mode emission in `codegen/platform/android/android.go` so the testagent submits snapshot names prefixed with the testRunner. Add to `TestModelAccessor.kt` (or a new `SnapshotNamePrefix.kt`):

```kotlin
// Inject testRunner so snapshot names become "<runner>/<name>" — driver
// stores goldens under <fixture>.snapshots/<runner>/<name>.<ext>.
fun snapshotName(name: String): String = "robolectric/$name"
```

And in the kotlin testagent's `T.snapshot`, call this helper if available:

```kotlin
fun snapshot(snapshotName: String) {
    val prefixed = try {
        // Reflect into the user package to find an optional snapshotName(name) helper.
        // Easier: codegen-emitted snapshot.kt provides it as a static fun and
        // the test bodies are already in the same package.
        snapshotName(snapshotName)   // resolves via Kotlin's package-level fun lookup
    } catch (e: Throwable) {
        snapshotName
    }
    // ... rest unchanged ...
}
```

Better approach: avoid reflection. Have agent emission write the prefix directly into the per-test wrapper:

```go
fmt.Fprintf(&b, "fun test%s(t: T) {\n", suffix)
b.WriteString("    val c = newTestComponent()\n")
b.WriteString("    setCurrentTestModel(c)\n")
// Prefix snapshot names with the testRunner under agent mode so
// goldens are scoped per-runner.
b.WriteString("    val __sngl_snapshotPrefix = \"\"\n") // Replaced at codegen time below
```

That gets clunky. Simplest: codegen emits a top-level constant in `AgentMain.kt`:

```kotlin
const val SNGL_SNAPSHOT_PREFIX: String = "robolectric/"   // or "device/"
```

And `T.snapshot` reads it via a package-level lookup OR — cleanest — `Snapshots.register` takes a prefix:

```kotlin
// pkg/kotlin/testagent/Snapshot.kt
object Snapshots {
    @Volatile private var capture: SnapshotCapture? = null
    @Volatile var namePrefix: String = ""
    fun register(prefix: String, fn: SnapshotCapture) {
        capture = fn
        namePrefix = prefix
    }
    fun capture(): SnapshotCapture? = capture
    internal fun reset() { capture = null; namePrefix = "" }
}
```

`T.snapshot` then prefixes:

```kotlin
val params = mapOf(
    "test" to name,
    "name" to (Snapshots.namePrefix + snapshotName),
    ...
)
```

Update `RobolectricSnapshot.kt` codegen to call `Snapshots.register("robolectric/")`. Add the parameter to the `init` block.

Task 1 already included `Snapshots.namePrefix`; this task just exercises the prefix end-to-end via the robolectric snapshot capture and the fixture's expected golden path.

- [ ] **Step 2: Write the fixture**

```
# Robolectric snapshot round-trip. Goldens land under robolectric/ subdir.

[!exec:java] skip 'JDK required'
[!exec:gradle] skip 'gradle required'

env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=android --opt testRunner=robolectric app.sngl
stdout 'PASS'
exists app.sngl.snapshots/robolectric/initial.png

env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=android --opt testRunner=robolectric app.sngl
stdout 'PASS'

-- app.sngl --
output { kotlin { android } }

component greeter {
    var name = "world"
    text(value=name)
}

func testGreeterSnapshot(t Test, c greeter) {
    t.snapshot("initial")
}
```

- [ ] **Step 3: Run**

```bash
go test ./cmd/sngl/ -run TestScript/test_android_robolectric_snapshot -count=1 -v 2>&1 | tail -30
```

Expected: PASS on hosts with JDK + gradle. If Robolectric's native-graphics rendering fails (e.g. native libs missing), capture the error and report DONE_WITH_CONCERNS — Robolectric 4.10+'s GraphicsMode.NATIVE requires the native graphics deps that the gradle config pulls in. If they fetch and link, the snapshot works.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/android/ pkg/kotlin/testagent/src/main/kotlin/us/duckfam/sngl/testagent/Snapshot.kt cmd/sngl/testdata/test_android_robolectric_snapshot.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata + pkg/kotlin/testagent: android robolectric snapshot fixture

Snapshots.register accepts a name prefix; T.snapshot threads it into
the snapshotAssert RPC so goldens land under <fixture>.snapshots/
<testRunner>/<name>.<ext>. android codegen emits "robolectric/" prefix
under testRunner=robolectric.

End-to-end fixture creates the golden under SNGL_UPDATE_SNAPSHOTS=1
then diffs on second run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Android codegen — agent emission for testRunner=device

**Files:**
- Modify: `codegen/platform/android/android.go`
- Modify: `codegen/platform/android/scaffold.go` (where MainActivity emits)

Emit a full APK with the testagent embedded and `MainActivity.onCreate` hooked to start the TCP listener on receive of `SNGL_AGENT_PORT` intent extra.

- [ ] **Step 1: Add device-path emission to Generate**

In `codegen/platform/android/android.go`'s Generate, inside the agent-mode block from Task 4, branch on testRunner:

```go
tr := codegen.OptionString(req.Options, "testRunner")
if tr == "device" {
	// Emit a separate Kotlin file that contains the test-agent
	// bootstrap. MainActivity.onCreate calls into it.
	bootstrapSrc := `package us.duckfam.sngl.app

import android.app.Activity
import us.duckfam.sngl.testagent.startTcp
import kotlin.concurrent.thread

object TestAgentBootstrap {
    fun start(activity: Activity) {
        val port = activity.intent?.getIntExtra("SNGL_AGENT_PORT", 0) ?: 0
        if (port <= 0) return
        Snapshots.register("device/", DeviceSnapshotCapture(activity::findRootView))
        thread(start = true, isDaemon = false, name = "sngl-testagent") {
            startTcp(port)
        }
    }
}

fun Activity.findRootView(): android.view.View =
    findViewById(android.R.id.content) ?: error("no root view")
`
	if err := writeAndroidSourceFile(sink, "TestAgentBootstrap.kt", req.Lang, ktOpts, []byte(bootstrapSrc)); err != nil {
		return err
	}

	deviceSnapshotSrc := deviceSnapshotCaptureKotlin(pkgName)
	if err := writeAndroidSourceFile(sink, "DeviceSnapshot.kt", req.Lang, ktOpts, []byte(deviceSnapshotSrc)); err != nil {
		return err
	}
}
```

Add the helper:

```go
func deviceSnapshotCaptureKotlin(pkg string) []byte {
	return []byte(`package ` + pkg + `

import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import java.io.ByteArrayOutputStream

class DeviceSnapshotCapture(private val rootView: () -> View) : () -> Pair<String, ByteArray> {
    override fun invoke(): Pair<String, ByteArray> {
        val v = rootView()
        val bmp = Bitmap.createBitmap(v.width.coerceAtLeast(1), v.height.coerceAtLeast(1), Bitmap.Config.ARGB_8888)
        v.draw(Canvas(bmp))
        val out = ByteArrayOutputStream()
        bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
        return "image/png" to out.toByteArray()
    }
}
`)
}
```

- [ ] **Step 2: Hook MainActivity.onCreate**

In `codegen/platform/android/scaffold.go` (or wherever MainActivity.kt is emitted), under `testMode=agent && testRunner=device`, inject before any user-UI code in `onCreate`:

```kotlin
TestAgentBootstrap.start(this)
```

Locate the MainActivity emission template and add the call. The template is likely a string constant in scaffold.go; thread the testRunner option into the template parameters.

- [ ] **Step 3: Smoke (generate only)**

```bash
mkdir -p /tmp/sngl-dev && cd /tmp/sngl-dev
cat > app.sngl <<'EOF'
output { kotlin { android } }
component greeter { var name = "world" text(value=name) }
func testFoo(t Test, c greeter) { t.assert(c.name == "world") }
EOF
sngl generate --lang=kotlin --platform=android --opt test=true --opt testMode=agent --opt testRunner=device --out out .
ls out/
grep -l "TestAgentBootstrap" out/MainActivity.kt
cat out/TestAgentBootstrap.kt | head -20
```

Expected: emitted files contain TestAgentBootstrap.kt, DeviceSnapshot.kt; MainActivity.kt calls `TestAgentBootstrap.start(this)`.

- [ ] **Step 4: Build + suite**

```bash
go build ./...
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/android/
git commit -m "$(cat <<'EOF'
codegen/platform/android: agent-mode emission for testRunner=device

Under testRunner=device, Generate emits:
- TestAgentBootstrap.kt — calls TestAgent.startTcp on a background
  thread when SNGL_AGENT_PORT intent extra is set.
- DeviceSnapshot.kt — View.draw → PNG capture; registered into the
  Snapshots singleton with "device/" name prefix.
MainActivity.onCreate calls TestAgentBootstrap.start(this) under
testMode=agent.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Android TestLauncher — device branch

**Files:**
- Modify: `codegen/platform/android/launcher.go`
- Modify: `codegen/platform/android/run.go` — make `ensureDevice` callable from launcher (export or move into a shared helper).

- [ ] **Step 1: Make `ensureDevice` accessible**

In `codegen/platform/android/run.go`, the function is currently package-private. Same package, so callable directly from `launcher.go` — no export needed. If the function isn't already idempotent (call it twice, no double-launch), wrap it:

```go
// ensureDeviceOnce starts an emulator if no device is currently
// attached. Safe to call multiple times across a test session — only
// starts one emulator total.
func ensureDeviceOnce() error {
	if hasDevice() {
		return nil
	}
	return ensureDevice()
}
```

If `ensureDevice` is already idempotent (it returns early when `hasDevice()`), use it directly.

- [ ] **Step 2: Implement `launchDevice`**

Replace the stub in `codegen/platform/android/launcher.go`:

```go
func (g *Generator) launchDevice(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	// Preconditions: ANDROID_HOME, adb, emulator, at least one AVD.
	if sdkRoot() == "" {
		return nil, nil, &codegen.SkipError{Reason: "ANDROID_HOME / ANDROID_SDK_ROOT not set"}
	}
	if _, err := androidTool("adb"); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "adb not found in Android SDK"}
	}
	if _, err := androidTool("emulator"); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "emulator not found in Android SDK"}
	}
	if _, err := pickAVD(); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "no AVD configured"}
	}

	// Synthesize the gradle Android project layout, emit the APK.
	if err := writeDeviceGradleProject(dir); err != nil {
		return nil, nil, err
	}
	if err := moveEmittedKotlinIntoAppSrcAndroid(dir); err != nil {
		return nil, nil, err
	}
	if err := gradleAssembleDebug(ctx, dir); err != nil {
		return nil, nil, err
	}

	apk := filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk")
	if _, err := os.Stat(apk); err != nil {
		return nil, nil, fmt.Errorf("apk not found: %w", err)
	}

	// Bring up emulator if no device attached.
	if err := ensureDeviceOnce(); err != nil {
		return nil, nil, err
	}

	// Install. Pick a port. adb forward. am start. Connect.
	pkg := "us.duckfam.sngl.app" // must match emitted Manifest applicationId
	if err := adbInstall(apk); err != nil {
		return nil, nil, fmt.Errorf("adb install: %w", err)
	}

	hostPort, err := pickFreeLocalhostPort()
	if err != nil {
		return nil, nil, err
	}
	devicePort := hostPort // by convention; the device-side listener uses the host port

	adb, _ := androidTool("adb")
	fwd := exec.CommandContext(ctx, adb, "forward",
		fmt.Sprintf("tcp:%d", hostPort), fmt.Sprintf("tcp:%d", devicePort))
	if err := fwd.Run(); err != nil {
		return nil, nil, fmt.Errorf("adb forward: %w", err)
	}

	start := exec.CommandContext(ctx, adb, "shell", "am", "start",
		"-n", pkg+"/.MainActivity",
		"--ei", "SNGL_AGENT_PORT", fmt.Sprintf("%d", devicePort))
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		return nil, nil, fmt.Errorf("am start: %w", err)
	}

	// Retry-loop connect to the forwarded port; the agent thread takes
	// ~50–500ms after am start to open its ServerSocket.
	var conn net.Conn
	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), time.Second)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		_ = exec.CommandContext(ctx, adb, "shell", "am", "force-stop", pkg).Run()
		_ = exec.CommandContext(ctx, adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
		return nil, nil, fmt.Errorf("device agent did not accept connection on %d", hostPort)
	}

	cleanup := func() {
		_ = conn.Close()
		_ = exec.Command(adb, "shell", "am", "force-stop", pkg).Run()
		_ = exec.Command(adb, "uninstall", pkg).Run()
		_ = exec.Command(adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
	}
	return conn, cleanup, nil
}

func pickFreeLocalhostPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func writeDeviceGradleProject(dir string) error {
	// Reuse the existing android scaffold logic which writes
	// build.gradle.kts + AndroidManifest.xml + per-module configs.
	// The existing path through Generate already does this when
	// Options.main=false isn't set; for the device test path we want
	// the same scaffold to be produced into dir.
	return nil // delegated; codegen has already populated dir under Generate
}

func moveEmittedKotlinIntoAppSrcAndroid(dir string) error {
	dst := filepath.Join(dir, "app", "src", "main", "kotlin", "us", "duckfam", "sngl", "app")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".kt") {
			continue
		}
		if err := os.Rename(filepath.Join(dir, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func gradleAssembleDebug(ctx context.Context, dir string) error {
	gradle, err := exec.LookPath("gradle")
	if err != nil {
		return &codegen.SkipError{Reason: "gradle not on PATH"}
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, gradle, ":app:assembleDebug", "--no-daemon")
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprint(os.Stderr, buf.String())
		return fmt.Errorf("gradle assembleDebug: %w", err)
	}
	return nil
}
```

Required imports added to `launcher.go`: `net`, `time`, `path/filepath`, plus the codegen helpers already in scope.

- [ ] **Step 3: Test the device path end-to-end (if host can)**

Only run if host has ANDROID_HOME + a configured AVD:

```bash
echo "$ANDROID_HOME"
emulator -list-avds | head
```

If both succeed:

```bash
mkdir -p /tmp/sngl-dev && cd /tmp/sngl-dev
cat > app.sngl <<'EOF'
output { kotlin { android } }
component greeter { var name = "world" text(value=name) }
func testFoo(t Test, c greeter) { t.assert(c.name == "world") }
EOF
sngl test --platform=android --opt testRunner=device .
```

Expected: emulator starts (if not already running), APK installs, agent connects, PASS reported. First run is slow (minutes).

If the host lacks any precondition, the launcher returns a clean SkipError; `sngl test` prints SKIP.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/android/launcher.go codegen/platform/android/run.go
git commit -m "$(cat <<'EOF'
codegen/platform/android: TestLauncher device branch

launchDevice: probe SDK preconditions; synth gradle Android project;
gradle assembleDebug → APK; ensureDeviceOnce starts an emulator if
needed (reused from sngl run); adb install + forward + am start with
SNGL_AGENT_PORT extra; retry-loop connect to the forwarded port; hand
the net.Conn back as RPCChannel.

Cleanup tears down install + forward; emulator left running for
subsequent invocations.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Device fixtures + delete legacy runtests.go

**Files:**
- Create: `cmd/sngl/testdata/test_android_device_state.txt`
- Create: `cmd/sngl/testdata/test_android_device_snapshot.txt`
- Delete: `codegen/platform/android/runtests.go`
- Delete: `codegen/platform/android/runtests_js.go`

- [ ] **Step 1: Device state fixture**

```
# Device end-to-end. Skips when ANDROID_HOME unset or no AVD configured.

[!exec:java] skip 'JDK required'
[!exec:adb] skip 'adb required'
[!exec:emulator] skip 'emulator required'

sngl test --platform=android --opt testRunner=device app.sngl
stdout 'PASS'

-- app.sngl --
output { kotlin { android } }

component counter {
    var count = 0
    text(value=string(count))
}

func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}
```

The `[!exec:adb]` and `[!exec:emulator]` guards skip when those binaries aren't on PATH. On hosts where they exist but no AVD is configured, the launcher's `pickAVD` probe returns `SkipError`, and `cmd/sngl/test.go`'s skip-handling (Plan 2 Task 6) surfaces a clean SKIP.

- [ ] **Step 2: Device snapshot fixture**

```
# Device snapshot via View.draw → PNG. Goldens under device/ subdir.

[!exec:java] skip 'JDK required'
[!exec:adb] skip 'adb required'
[!exec:emulator] skip 'emulator required'

env SNGL_UPDATE_SNAPSHOTS=1
sngl test --platform=android --opt testRunner=device app.sngl
stdout 'PASS'
exists app.sngl.snapshots/device/initial.png

env SNGL_UPDATE_SNAPSHOTS=
sngl test --platform=android --opt testRunner=device app.sngl
stdout 'PASS'

-- app.sngl --
output { kotlin { android } }

component greeter {
    var name = "world"
    text(value=name)
}

func testGreeterSnapshot(t Test, c greeter) {
    t.snapshot("initial")
}
```

- [ ] **Step 3: Delete legacy runners**

```bash
git rm codegen/platform/android/runtests.go
git rm codegen/platform/android/runtests_js.go
```

If anything references `RunTests` on the android Generator outside `runtests.go` itself (e.g. an init registration), fix as you go.

- [ ] **Step 4: Build + run**

```bash
go build ./...
go test ./cmd/sngl/ -run TestScript -count=1 2>&1 | tail -10
```

Expected: clean. The new fixtures pass or skip cleanly depending on host capability.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata + codegen/platform/android: device fixtures + drop legacy runtests

cmd/sngl/testdata/test_android_device_state.txt and
test_android_device_snapshot.txt cover the device path end-to-end.
Skip cleanly when JDK / adb / emulator unavailable.

codegen/platform/android/runtests.go + runtests_js.go deleted —
TestLauncher path is now the only entry, matching bubbletea/fyne/
gtk4.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review notes (for the implementer)

- **`pkg/kotlin/testagent` distribution.** The runtime is published to a local maven repository (`mavenLocal()`) so the generated gradle projects can resolve `us.duckfam.sngl:testagent:0.1.0`. The launcher should run `gradle publishToMavenLocal` on the testagent module before any test build the first time. Add this as a one-time setup step in `launchRobolectric` / `launchDevice` or document the prerequisite. **Tactical alternative**: include the testagent as a flat-dir dependency or a composite-build `includeBuild("path/to/pkg/kotlin/testagent")` declaration in the generated `settings.gradle.kts`. Composite-build is cleaner — implementer's call.
- **Package naming consistency.** This plan uses `us.duckfam.sngl.app` for emitted code and `us.duckfam.sngl.testagent` for the runtime. Verify the existing android codegen actually emits to `us.duckfam.sngl.app` (or whatever it uses today — likely a per-project applicationId from `Options.applicationId`). Thread the actual package name through `LowerTestFile`'s `pkg` argument.
- **Robolectric native graphics deps.** Robolectric 4.10+ requires both the base `org.robolectric:robolectric` dep AND the native-graphics shim. Verify the gradle config in `writeRobolectricGradleProject` includes everything Robolectric needs; check the upstream Compose snapshot tutorial for the exact dep list.
- **adb forward port reuse.** `adb forward` persists across runs unless removed; cleanup is critical. If a previous test crashed without cleanup, the next launch may collide. Add a defensive `adb forward --remove tcp:<port>` before the new `adb forward` setup.
- **Per-test isolation under Robolectric.** Each test's `newTestComponent()` returns a fresh `MainScreenState`. Compose's `setContent` is per-test; the ComposeContentTestRule should be re-set between tests. The launcher's `runDriver` already runs tests sequentially within one gradle JVM; the per-test setup happens inside the testagent's `runOne`.
- **No worktree.** Project memory says solo repo; commit to `main`.
- **Don't sweep pre-session drift.** The tree has ~17 lines of stale modifications across `internal/lower/caps.go`, `ir/convert.go`, etc. that pre-date this work. Only stage files explicitly touched per task.
