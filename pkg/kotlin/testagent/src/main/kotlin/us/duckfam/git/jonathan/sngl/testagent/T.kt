// Per-test handle. Mirrors pkg/go/testagent.T — the same method surface
// shows up in both modes (native JUnit / agent) so codegen emits the
// same call sites regardless.
//
// Control flow: failNow / skip / fatal throw AbortSentinel, which is
// caught by runBody and treated as a clean exit (not a panic-class
// failure). Any other thrown exception IS a test failure.

package us.duckfam.git.jonathan.sngl.testagent

import java.util.Base64
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.SynchronousQueue
import java.util.concurrent.TimeUnit

/** Thrown by failNow/skip/fatal to unwind the test body. Caught by runBody. */
class AbortSentinel : RuntimeException()

class T internal constructor(
    val name: String,
    internal val writer: RpcWriter,
) {
    @Volatile internal var failed: Boolean = false
    @Volatile internal var skipped: Boolean = false

    // --- Logging ---

    fun log(msg: String) {
        writer.notify("log", mapOf("test" to name, "msg" to msg))
    }

    fun logf(format: String, vararg args: Any?) = log(format.format(*args))

    // --- Fail/abort ---

    fun fail() {
        failed = true
        writer.notify("markFail", mapOf("test" to name))
    }

    fun failNow(): Nothing {
        fail()
        throw AbortSentinel()
    }

    fun error(msg: String) {
        log(msg)
        fail()
    }

    fun errorf(format: String, vararg args: Any?) = error(format.format(*args))

    fun fatal(msg: String): Nothing {
        error(msg)
        throw AbortSentinel()
    }

    fun fatalf(format: String, vararg args: Any?): Nothing = fatal(format.format(*args))

    fun skip(reason: String = ""): Nothing {
        skipped = true
        writer.notify("markSkip", mapOf("test" to name, "reason" to reason))
        throw AbortSentinel()
    }

    // --- Assertions ---

    fun assertTrue(cond: Boolean, msg: String = "assertion failed") {
        if (!cond) error(msg)
    }

    // --- Timing ---

    fun wait(durationMs: Long) {
        if (durationMs > 0) Thread.sleep(durationMs)
    }

    // --- Snapshot ---

    fun snapshot(snapshotName: String) {
        val capture = Snapshots.capture()
        if (capture == null) {
            error("snapshot \"$snapshotName\": no capture registered for this platform")
            return
        }
        val (mime, raw) = try {
            capture
        } catch (e: Exception) {
            error("snapshot \"$snapshotName\": capture: ${e.message}")
            return
        }
        val fullName = Snapshots.namePrefix + snapshotName

        // Register the pending channel BEFORE writing the request so a
        // fast response can't race the channel into existence.
        val id = writer.allocId()
        val queue = Pending.register(id)
        try {
            writer.requestWithId(id, "snapshotAssert", mapOf(
                "test" to name,
                "name" to fullName,
                "mime" to mime,
                "bytes" to Base64.getEncoder().encodeToString(raw),
            ))
            val resp = queue.poll(30, TimeUnit.SECONDS)
            if (resp == null) {
                error("snapshot \"$snapshotName\": rpc response timeout")
                return
            }
            if (resp.error != null) {
                error("snapshot \"$snapshotName\": driver: ${resp.error.message}")
                return
            }
            val result = resp.result
            val pass = result?.optBoolean("pass", false) ?: false
            if (!pass) {
                val diff = result?.optString("diff", "") ?: ""
                error("snapshot \"$snapshotName\" mismatch:\n$diff")
            }
        } finally {
            Pending.unregister(id)
        }
    }
}

/** Routes JSON-RPC responses by id to whichever T.snapshot call is parked on it. */
object Pending {
    private val byId = ConcurrentHashMap<Long, SynchronousQueue<RpcMessage>>()

    fun register(id: Long): SynchronousQueue<RpcMessage> {
        val q = SynchronousQueue<RpcMessage>()
        byId[id] = q
        return q
    }

    fun unregister(id: Long) {
        byId.remove(id)
    }

    /** Delivers a response to the waiting caller. No-op if no one is parked. */
    fun deliver(msg: RpcMessage) {
        val id = msg.id ?: return
        val q = byId[id] ?: return
        // offer with a short timeout so we don't block the read loop indefinitely
        // if the receiver has already given up (e.g. timeout).
        q.offer(msg, 1, TimeUnit.SECONDS)
    }

    /** Test-only helper. */
    internal fun clear() {
        byId.clear()
    }
}

/** Convenience for unit tests — construct a T directly against an arbitrary writer. */
internal fun newAgentT(out: java.io.OutputStream, name: String): T =
    T(name, RpcWriter(out))
