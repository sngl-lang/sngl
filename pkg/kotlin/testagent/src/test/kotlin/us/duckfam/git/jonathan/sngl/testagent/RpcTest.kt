// JUnit5 tests for the JSON-RPC 2.0 codec.

package us.duckfam.git.jonathan.sngl.testagent

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNotNull
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream

class RpcTest {

    @Test
    fun `notification roundtrip`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        w.notify("log", mapOf("test" to "t1", "msg" to "hello"))

        val r = RpcReader(ByteArrayInputStream(buf.toByteArray()))
        val m = r.read()
        assertNotNull(m)
        m!!
        assertEquals("log", m.method)
        assertNull(m.id)
        assertTrue(m.isNotification())
        assertEquals("t1", m.params!!.getString("test"))
        assertEquals("hello", m.params.getString("msg"))
    }

    @Test
    fun `request and response roundtrip`() {
        val buf = ByteArrayOutputStream()
        val w = RpcWriter(buf)
        val id = w.request("snapshotAssert", mapOf("test" to "t1", "name" to "first"))
        w.respond(id, mapOf("pass" to true), null)

        val r = RpcReader(ByteArrayInputStream(buf.toByteArray()))
        val req = r.read()!!
        assertTrue(req.isRequest())
        assertEquals(id, req.id)
        assertEquals("snapshotAssert", req.method)

        val resp = r.read()!!
        assertTrue(resp.isResponse())
        assertEquals(id, resp.id)
        assertEquals(true, resp.result!!.getBoolean("pass"))
        assertNull(resp.error)
    }

    @Test
    fun `malformed line raises`() {
        val r = RpcReader(ByteArrayInputStream("not json\n".toByteArray()))
        assertThrows(RpcException::class.java) { r.read() }
    }
}
