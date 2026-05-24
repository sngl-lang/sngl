// JUnit5 tests for T + Registry. These don't require Android — the
// runtime is pure JVM (Robolectric only enters the picture when the
// android codegen wires in a screen-capture function via Snapshots).

package us.duckfam.git.jonathan.sngl.testagent

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertNotNull
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.PipedInputStream
import java.io.PipedOutputStream

class TTest {

    @BeforeEach
    fun resetState() {
        Registry.clear()
        Snapshots.reset()
        Pending.clear()
    }

    @Test
    fun `log emits notification`() {
        val buf = ByteArrayOutputStream()
        val t = newAgentT(buf, "myTest")
        t.log("hello")

        val m = RpcReader(ByteArrayInputStream(buf.toByteArray())).read()!!
        assertTrue(m.isNotification())
        assertEquals("log", m.method)
        assertEquals("myTest", m.params!!.getString("test"))
        assertEquals("hello", m.params.getString("msg"))
    }

    @Test
    fun `failNow aborts via AbortSentinel`() {
        val buf = ByteArrayOutputStream()
        val t = newAgentT(buf, "myTest")
        assertThrows(AbortSentinel::class.java) { t.failNow() }
        assertTrue(t.failed)
    }

    @Test
    fun `error marks failed and logs`() {
        val buf = ByteArrayOutputStream()
        val t = newAgentT(buf, "myTest")
        t.error("bad: 7")
        assertTrue(t.failed)

        val r = RpcReader(ByteArrayInputStream(buf.toByteArray()))
        val first = r.read()!!
        assertEquals("log", first.method)
        assertTrue(first.params!!.getString("msg").contains("bad: 7"))
        val second = r.read()!!
        assertEquals("markFail", second.method)
    }

    @Test
    fun `Registry enumerates registered names`() {
        Registry.register("alpha") { it.log("a") }
        Registry.register("beta") { it.log("b") }
        assertEquals(listOf("alpha", "beta"), Registry.names())
    }

    // --- Snapshot ---

    @Test
    fun `snapshot fails when no capture registered`() {
        val buf = ByteArrayOutputStream()
        val t = newAgentT(buf, "T1")
        t.snapshot("first")
        assertTrue(t.failed)
    }

    @Test
    fun `snapshot submits request and passes on driver ack`() {
        Snapshots.register("robolectric/") { "text/plain" to "hello".toByteArray() }

        // Paired pipes: agent ↔ driver.
        val agentIn = PipedInputStream()
        val driverOut = PipedOutputStream(agentIn)
        val driverIn = PipedInputStream()
        val agentOut = PipedOutputStream(driverIn)

        val agentWriter = RpcWriter(agentOut)
        val t = T("T1", agentWriter)

        // Read loop on the agent side: route responses into Pending.
        val readerThread = Thread {
            val r = RpcReader(agentIn)
            try {
                while (true) {
                    val m = r.read() ?: return@Thread
                    if (m.isResponse()) Pending.deliver(m)
                }
            } catch (_: Exception) {
            }
        }
        readerThread.isDaemon = true
        readerThread.start()

        // Simulated driver: read snapshotAssert, send pass=true.
        val driverThread = Thread {
            val r = RpcReader(driverIn)
            val w = RpcWriter(driverOut)
            val msg = r.read()!!
            assertEquals("snapshotAssert", msg.method)
            assertEquals("robolectric/first", msg.params!!.getString("name"))
            w.respond(msg.id!!, mapOf("pass" to true), null)
        }
        driverThread.isDaemon = true
        driverThread.start()

        t.snapshot("first")
        assertFalse(t.failed)
    }

    @Test
    fun `snapshot mismatch fails the test with diff`() {
        Snapshots.register("device/") { "text/plain" to "actual".toByteArray() }

        val agentIn = PipedInputStream()
        val driverOut = PipedOutputStream(agentIn)
        val driverIn = PipedInputStream()
        val agentOut = PipedOutputStream(driverIn)

        val agentWriter = RpcWriter(agentOut)
        val t = T("T2", agentWriter)

        Thread {
            val r = RpcReader(agentIn)
            try {
                while (true) {
                    val m = r.read() ?: return@Thread
                    if (m.isResponse()) Pending.deliver(m)
                }
            } catch (_: Exception) {
            }
        }.apply { isDaemon = true }.start()

        Thread {
            val r = RpcReader(driverIn)
            val w = RpcWriter(driverOut)
            while (true) {
                val msg = r.read() ?: return@Thread
                if (msg.method == "snapshotAssert") {
                    w.respond(msg.id!!, mapOf("pass" to false, "diff" to "want X got Y"), null)
                }
            }
        }.apply { isDaemon = true }.start()

        t.snapshot("foo")
        assertTrue(t.failed)
    }

    // --- runBody integration ---

    @Test
    fun `runBody passes for clean test`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        Registry.register("ok") { it.log("ran") }
        val status = TestAgent.runOne(w, "ok")
        assertEquals("pass", status)

        val r = RpcReader(ByteArrayInputStream(buf.toByteArray()))
        val methods = mutableListOf<String>()
        while (true) {
            val m = r.read() ?: break
            methods.add(m.method ?: "<resp>")
        }
        assertEquals(listOf("testStart", "log", "testEnd"), methods)
    }

    @Test
    fun `runBody catches non-sentinel exceptions as failures`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        Registry.register("boom") { throw IllegalStateException("kaboom") }
        val status = TestAgent.runOne(w, "boom")
        assertEquals("fail", status)
    }

    @Test
    fun `runBody treats AbortSentinel from failNow as fail`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        Registry.register("bad") { it.error("nope"); it.failNow() }
        val status = TestAgent.runOne(w, "bad")
        assertEquals("fail", status)
    }

    @Test
    fun `runBody treats skip as skip`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        Registry.register("s") { it.skip("not today") }
        val status = TestAgent.runOne(w, "s")
        assertEquals("skip", status)
    }
}
