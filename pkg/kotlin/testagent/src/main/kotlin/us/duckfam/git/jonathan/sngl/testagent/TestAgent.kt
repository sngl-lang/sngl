// Entry point for the Kotlin testagent. Selects transport at startup:
//
//   - default: speak JSON-RPC over stdin/stdout (e.g. Robolectric/JVM main()
//     launched as a subprocess by the SNGL driver).
//   - SNGL_AGENT_PORT=N set: dial back to the driver via TCP on localhost:N
//     (e.g. device-side path: the driver opens a listener, adb-forwards a
//     port, the on-device agent connects out to it).
//   - startTcp(port) callers (e.g. on-device launcher that wants to accept
//     instead of dial): block on a ServerSocket.accept() and then drive the
//     same RPC loop.

package us.duckfam.git.jonathan.sngl.testagent

import java.io.InputStream
import java.io.OutputStream
import java.net.ServerSocket
import java.net.Socket

object TestAgent {
    /** Standard SNGL-emitted main(): stdio (default) or env-var-driven TCP-connect. */
    @JvmStatic
    fun main(args: Array<String>) {
        val portStr = System.getenv("SNGL_AGENT_PORT")
        if (!portStr.isNullOrBlank()) {
            val port = portStr.toInt()
            Socket("127.0.0.1", port).use { sock ->
                runDriver(sock.getInputStream(), sock.getOutputStream())
            }
            return
        }
        runDriver(System.`in`, System.out)
    }

    /** Device path: bind a TCP listener on `port` and serve the first connection. */
    @JvmStatic
    fun startTcp(port: Int) {
        ServerSocket(port).use { server ->
            server.accept().use { sock ->
                runDriver(sock.getInputStream(), sock.getOutputStream())
            }
        }
    }

    /**
     * Robolectric/JUnit path: dial back to the driver-side listener on
     * `127.0.0.1:port` and serve the RPC loop over that socket. Mirror
     * image of [startTcp] — the JUnit @Test body runs inside the gradle
     * unit-test JVM (which the driver spawned), and the driver itself
     * opens the listener before invoking gradle.
     *
     * Threading model differs from [runDriver]: this entry point keeps
     * the dispatch loop on the calling thread (the JUnit @Test thread,
     * which Robolectric treats as the Android main thread) and spawns
     * a *reader* thread for inbound RPC responses. Compose + Robolectric
     * require all UI work to happen on the main thread, so snapshot
     * capture closures invoked via T.snapshot() MUST run on this thread.
     *
     * The reader thread only delivers snapshotAssert responses via
     * [Pending] — it never executes test bodies.
     */
    @JvmStatic
    fun connectAndDrive(port: Int) {
        Socket("127.0.0.1", port).use { sock ->
            val reader = RpcReader(sock.getInputStream())
            val writer = RpcWriter(sock.getOutputStream())

            // Pump inbound messages off the calling thread. The dispatch
            // loop below pulls method-call requests off a queue so the
            // calling thread (main / JUnit) can synchronously run the
            // test body when a "run" RPC arrives.
            val requests = java.util.concurrent.LinkedBlockingQueue<RpcMessage>()
            val readerThread = Thread {
                while (true) {
                    val msg = try {
                        reader.read() ?: break
                    } catch (e: RpcException) {
                        writer.respond(0L, null, RpcError(-32700, e.message ?: "parse error"))
                        continue
                    }
                    if (msg.isResponse()) {
                        Pending.deliver(msg)
                        continue
                    }
                    if (msg.id == null) continue
                    requests.put(msg)
                }
                // EOF: enqueue a sentinel so the main loop wakes and exits.
                requests.put(RpcMessage(method = "__eof__", id = -1L))
            }.apply { isDaemon = true; name = "sngl-testagent-reader"; start() }

            try {
                while (true) {
                    val msg = requests.take()
                    if (msg.method == "__eof__") return
                    val id = msg.id ?: continue
                    when (msg.method) {
                        "list" -> {
                            writer.respond(id, mapOf("tests" to Registry.names()), null)
                        }
                        "run" -> {
                            val filter = msg.params?.optString("filter", "") ?: ""
                            // Run inline on the main/JUnit thread so the
                            // snapshot capture closure (which touches
                            // Compose + Robolectric's looper) executes
                            // on the only thread Robolectric accepts.
                            runFiltered(writer, filter)
                            writer.respond(id, emptyMap(), null)
                        }
                        "cancel" -> {
                            writer.respond(id, emptyMap(), null)
                            return
                        }
                        else -> writer.respond(
                            id, null,
                            RpcError(-32601, "method not found: ${msg.method ?: "<null>"}")
                        )
                    }
                }
            } finally {
                try { sock.close() } catch (_: Throwable) {}
                readerThread.interrupt()
            }
        }
    }

    @JvmStatic
    fun runDriver(input: InputStream, output: OutputStream) {
        val reader = RpcReader(input)
        val writer = RpcWriter(output)
        while (true) {
            val msg = try {
                reader.read() ?: return
            } catch (e: RpcException) {
                writer.respond(0L, null, RpcError(-32700, e.message ?: "parse error"))
                continue
            }
            if (msg.isResponse()) {
                Pending.deliver(msg)
                continue
            }
            if (msg.id == null) {
                // Stray driver→agent notification: ignore.
                continue
            }
            val id = msg.id
            when (msg.method) {
                "list" -> {
                    val tests = Registry.names()
                    writer.respond(id, mapOf("tests" to tests), null)
                }
                "run" -> {
                    val filter = msg.params?.optString("filter", "") ?: ""
                    // Run on a worker thread so the read loop keeps draining
                    // — necessary for snapshotAssert responses to be routed
                    // back via Pending.deliver while a test is in flight.
                    Thread {
                        runFiltered(writer, filter)
                        writer.respond(id, emptyMap(), null)
                    }.apply { isDaemon = true; name = "sngl-testagent-run" }.start()
                }
                "cancel" -> {
                    writer.respond(id, emptyMap(), null)
                    return
                }
                else -> writer.respond(
                    id, null,
                    RpcError(-32601, "method not found: ${msg.method ?: "<null>"}")
                )
            }
        }
    }

    internal fun runFiltered(writer: RpcWriter, filter: String) {
        var passed = 0
        var failed = 0
        var skipped = 0
        for (name in Registry.names()) {
            if (filter.isNotEmpty() && !name.contains(filter)) continue
            when (runOne(writer, name)) {
                "pass" -> passed++
                "fail" -> failed++
                "skip" -> skipped++
            }
        }
        writer.notify("runComplete", mapOf(
            "passed" to passed,
            "failed" to failed,
            "skipped" to skipped,
        ))
    }

    internal fun runOne(writer: RpcWriter, name: String): String {
        val fn = Registry.get(name)
        if (fn == null) {
            writer.notify("testEnd", mapOf(
                "test" to name, "status" to "fail", "durationMs" to 0
            ))
            return "fail"
        }
        val t = T(name, writer)
        return runBody(writer, name, t, fn)
    }

    internal fun runBody(writer: RpcWriter, name: String, t: T, fn: (T) -> Unit): String {
        writer.notify("testStart", mapOf("test" to name))
        val start = System.currentTimeMillis()
        try {
            fn(t)
        } catch (_: AbortSentinel) {
            // Normal control-flow exit from failNow/skip/fatal.
        } catch (e: Throwable) {
            t.failed = true
            writer.notify("log", mapOf("test" to name, "msg" to "exception: ${e.javaClass.simpleName}: ${e.message}"))
        }
        val status = when {
            t.skipped -> "skip"
            t.failed -> "fail"
            else -> "pass"
        }
        writer.notify("testEnd", mapOf(
            "test" to name,
            "status" to status,
            "durationMs" to (System.currentTimeMillis() - start).toInt(),
        ))
        return status
    }
}
