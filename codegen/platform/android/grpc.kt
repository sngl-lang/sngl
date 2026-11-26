package PACKAGE

import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.RequestBody
import okio.BufferedSink

// SnglGrpc is the transport behind sngl:grpc's call and stream: a call's
// bytes framed as gRPC and carried over OkHttp's HTTP/2. The messages are
// written and read by SnglProto, from the program's own classes, so nothing
// here knows a message.
object SnglGrpc {
    class Answer(val body: ByteArray, val code: Int, val message: String)

    private val none = ByteArray(0)
    private val grpc = "application/grpc".toMediaType()

    // A server written `host:port` is called in plaintext, which HTTP/2
    // speaks only to a client that assumes it.
    private val plain by lazy { OkHttpClient.Builder().protocols(listOf(Protocol.H2_PRIOR_KNOWLEDGE)).build() }
    private val tls by lazy { OkHttpClient.Builder().protocols(listOf(Protocol.HTTP_2, Protocol.HTTP_1_1)).build() }

    // The request messages are written as they come, while the responses
    // are read: OkHttp hands a duplex body its sink and leaves it open.
    private class Pumped : RequestBody() {
        val sink = CompletableDeferred<BufferedSink>()

        override fun contentType() = grpc

        override fun isDuplex() = true

        override fun writeTo(sink: BufferedSink) {
            this.sink.complete(sink)
        }
    }

    private fun request(server: String, method: String, body: RequestBody): Request {
        val secure = server.startsWith("https://")
        val host = server.removePrefix("https://").removePrefix("http://").trimEnd('/')
        return Request.Builder()
            .url((if (secure) "https://" else "http://") + host + method)
            .header("te", "trailers")
            .post(body)
            .build()
    }

    private fun client(server: String) = if (server.startsWith("https://")) tls else plain

    // A call that fails before answering carries its status in the headers,
    // with no trailers after them.
    private fun status(resp: okhttp3.Response): Answer {
        val status = resp.header("grpc-status") ?: resp.trailers()["grpc-status"]
        val message = resp.header("grpc-message") ?: resp.trailers()["grpc-message"] ?: ""
        val code = status?.toIntOrNull() ?: return Answer(none, 13, "the response has no grpc-status")
        return Answer(none, code, if (code != 0) unescape(message) else "")
    }

    suspend fun call(server: String, method: String, request: ByteArray, life: Int): Answer {
        var got: ByteArray? = null
        var n = 0
        var sent = false
        val a = exchange(server, method, { if (sent) null else request.also { sent = true } }, life) {
            got = it
            n++
            true
        }
        if (a.code != 0) return a
        if (n != 1) return Answer(none, 13, "a unary call answered $n messages")
        return Answer(got!!, 0, "")
    }

    // stream sends each item of sngl:async's queue as write writes it, as it
    // is sent, and hands each response message to each, where the caller
    // runs, as it arrives: until the server ends the call, each answers
    // false (code -1), or life ends. Closing the queue ends the requests.
    suspend fun stream(server: String, method: String, queue: Int, write: (Any?) -> ByteArray, life: Int, each: suspend (ByteArray) -> Boolean): Answer =
        exchange(server, method, { SnglCodec.queueReceive(queue)?.let(write) }, life, each)

    private suspend fun exchange(server: String, method: String, next: suspend () -> ByteArray?, life: Int, each: suspend (ByteArray) -> Boolean): Answer {
        if (SnglCodec.ended(life)) return Answer(none, 1, "cancelled")
        if (server.isEmpty()) return Answer(none, 14, "no server: provide the service's address")
        val body = Pumped()
        val call = client(server).newCall(request(server, method, body))
        val messages = Channel<ByteArray>(16)
        return coroutineScope {
            // SnglCodec keeps its lifetimes' aborts to itself, so the call
            // watches for its lifetime's end.
            val watch = if (life == 0) null else launch {
                while (!SnglCodec.ended(life)) delay(20)
                call.cancel()
            }
            val pump = launch(Dispatchers.IO) {
                val sink = body.sink.await()
                try {
                    while (true) {
                        val m = next() ?: break
                        sink.writeByte(0).writeInt(m.size).write(m).flush()
                    }
                    sink.close()
                } catch (e: IOException) {
                    // The call ended, and the reader answers how.
                }
            }
            val reader = async(Dispatchers.IO) {
                try {
                    call.execute().use { resp ->
                        if (resp.code != 200) return@async Answer(none, fromHttp(resp.code), "HTTP ${resp.code}")
                        val src = resp.body!!.source()
                        while (!src.exhausted()) {
                            val flag = src.readByte()
                            val len = src.readInt()
                            if (flag.toInt() != 0 || len < 0) return@async Answer(none, 13, "a response message the client cannot read")
                            messages.send(src.readByteArray(len.toLong()))
                        }
                        status(resp)
                    }
                } catch (e: java.io.EOFException) {
                    Answer(none, 13, "a response message cut short")
                } catch (e: IOException) {
                    if (SnglCodec.ended(life)) Answer(none, 1, "cancelled") else Answer(none, 14, e.toString())
                } finally {
                    messages.close()
                }
            }
            var stopped: Answer? = null
            for (m in messages) {
                if (!each(m)) stopped = Answer(none, -1, "")
                else if (SnglCodec.ended(life)) stopped = Answer(none, 1, "cancelled")
                if (stopped != null) break
            }
            watch?.cancel()
            val answer = if (stopped != null) {
                call.cancel()
                messages.cancel()
                reader.cancel()
                stopped
            } else {
                reader.await()
            }
            pump.cancel()
            answer
        }
    }

    // gRPC's mapping of an HTTP status that is not one of its own answers.
    private fun fromHttp(status: Int): Int = when (status) {
        400 -> 13
        401 -> 16
        403 -> 7
        404 -> 12
        429, 502, 503, 504 -> 14
        else -> 2
    }

    // grpc-message is percent-encoded UTF-8, where `+` is itself.
    private fun unescape(s: String): String {
        if (!s.contains('%')) return s
        val out = java.io.ByteArrayOutputStream()
        var i = 0
        while (i < s.length) {
            val c = s[i]
            if (c == '%' && i + 2 < s.length) {
                val b = s.substring(i + 1, i + 3).toIntOrNull(16)
                if (b != null) {
                    out.write(b)
                    i += 3
                    continue
                }
            }
            out.write(c.toString().toByteArray(Charsets.UTF_8))
            i++
        }
        return out.toString("UTF-8")
    }
}

class SnglPbFail(message: String) : Exception(message)

class SnglPbDecoded<T>(val value: T?, val error: String?)

// SnglPbWriter is a message's bytes as protobuf's wire format writes them.
class SnglPbWriter {
    private var buf = ByteArray(64)
    private var size = 0

    fun bytes(): ByteArray = buf.copyOf(size)

    private fun room(n: Int) {
        if (size + n > buf.size) buf = buf.copyOf(maxOf(buf.size * 2, size + n))
    }

    fun tag(num: Int, type: Int) = varint((num.toLong() shl 3) or type.toLong())

    fun varint(v: Long) {
        var x = v
        room(10)
        while (x and 0x7fL.inv() != 0L) {
            buf[size++] = ((x and 0x7f) or 0x80).toByte()
            x = x ushr 7
        }
        buf[size++] = x.toByte()
    }

    fun zigzag32(v: Int) = varint(((v shl 1) xor (v shr 31)).toLong() and 0xffffffffL)

    fun zigzag64(v: Long) = varint((v shl 1) xor (v shr 63))

    fun fixed32(v: Int) {
        room(4)
        for (i in 0 until 4) buf[size++] = (v ushr (8 * i)).toByte()
    }

    fun fixed64(v: Long) {
        room(8)
        for (i in 0 until 8) buf[size++] = (v ushr (8 * i)).toByte()
    }

    fun raw(b: ByteArray) {
        varint(b.size.toLong())
        room(b.size)
        b.copyInto(buf, size)
        size += b.size
    }

    fun string(s: String) = raw(s.toByteArray(Charsets.UTF_8))

    fun base64(s: String) = raw(android.util.Base64.decode(s, android.util.Base64.DEFAULT))

    // A length-delimited record of what write writes: a message, a packed
    // field, a map's entry.
    inline fun sub(write: (SnglPbWriter) -> Unit) {
        val w = SnglPbWriter()
        write(w)
        raw(w.bytes())
    }
}

// SnglPbReader reads the records of a message, b from start to end. A
// record that is not protobuf, or not of the wire type its field is, fails
// naming where.
class SnglPbReader(private val b: ByteArray, private var pos: Int = 0, private val end: Int = b.size) {
    companion object {
        fun narrow(x: Long, lo: Long, hi: Long, path: String): Long {
            if (x < lo || x > hi) throw SnglPbFail("$path: $x is out of range")
            return x
        }
    }

    fun more(): Boolean = pos < end

    fun varint(): Long {
        var x = 0L
        var shift = 0
        while (shift < 64) {
            if (pos >= end) throw SnglPbFail("not protobuf")
            val c = b[pos++].toInt()
            x = x or ((c and 0x7f).toLong() shl shift)
            if (c and 0x80 == 0) return x
            shift += 7
        }
        throw SnglPbFail("not protobuf")
    }

    fun tag(path: String): Long = try {
        varint()
    } catch (e: SnglPbFail) {
        throw SnglPbFail("$path: not protobuf")
    }

    // want checks the record tag reads is of wire type type, as kind is
    // written.
    fun want(tag: Long, type: Int, path: String, kind: String) {
        if ((tag and 7L).toInt() != type) throw SnglPbFail("$path: want $kind")
    }

    fun fixed32(): Int {
        if (end - pos < 4) throw SnglPbFail("not protobuf")
        var x = 0
        for (i in 0 until 4) x = x or ((b[pos++].toInt() and 0xff) shl (8 * i))
        return x
    }

    fun fixed64(): Long {
        if (end - pos < 8) throw SnglPbFail("not protobuf")
        var x = 0L
        for (i in 0 until 8) x = x or ((b[pos++].toLong() and 0xff) shl (8 * i))
        return x
    }

    fun zigzag32(): Int {
        val x = varint().toInt()
        return (x ushr 1) xor -(x and 1)
    }

    fun zigzag64(): Long {
        val x = varint()
        return (x ushr 1) xor -(x and 1)
    }

    private fun length(): Int {
        val n = varint()
        if (n < 0 || n > end - pos) throw SnglPbFail("not protobuf")
        return n.toInt()
    }

    fun bytes(): ByteArray {
        val n = length()
        val out = b.copyOfRange(pos, pos + n)
        pos += n
        return out
    }

    fun string(): String {
        val n = length()
        val out = String(b, pos, n, Charsets.UTF_8)
        pos += n
        return out
    }

    fun base64(): String = android.util.Base64.encodeToString(bytes(), android.util.Base64.NO_WRAP)

    fun sub(): SnglPbReader {
        val n = length()
        val r = SnglPbReader(b, pos, pos + n)
        pos += n
        return r
    }

    fun skip(tag: Long, path: String) {
        try {
            when ((tag and 7L).toInt()) {
                0 -> varint()
                1 -> fixed64()
                2 -> pos += length()
                5 -> fixed32()
                else -> throw SnglPbFail("not protobuf")
            }
        } catch (e: SnglPbFail) {
            throw SnglPbFail("$path: not protobuf")
        }
    }
}
