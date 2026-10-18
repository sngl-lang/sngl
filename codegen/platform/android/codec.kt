package PACKAGE

// The runtime behind sngl:json and sngl:http, written beside MainScreen.kt
// when a program reaches either. The compiler hands each call a Codec it
// wrote from the SNGL type -- how to build and read each struct, the name each
// field and enum member travels under -- and the rules and the words are
// pkg/go/sngljson's: a key the type does not name is ignored, a missing key
// fails unless its field is an option, a value of the wrong kind fails naming
// where it was, writing leaves an absent option's key out, and a map's keys are
// visited in order so a failure names the same one everywhere.

import java.net.HttpURLConnection
import java.net.SocketTimeoutException
import java.net.URL
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener

class SnglCodec(val kind: Int) {
    var elem: SnglCodec? = null
    var fields: List<SnglCodecField> = emptyList()
    var make: ((List<Any?>) -> Any)? = null
    var read: ((Any) -> List<Any?>)? = null
    var wires: List<String> = emptyList()
    var values: List<Any> = emptyList()

    class Decoded(val value: Any?, val error: String?)

    class Response(
        val status: Int,
        val headers: Map<String, String>,
        val body: String,
        val failed: Boolean,
        val timeout: Boolean,
        val message: String,
    )

    private class Fail(message: String) : Exception(message)

    companion object {
        const val BOOL = 0
        const val INT = 1
        const val LONG = 2
        const val FLOAT = 3
        const val STRING = 4
        const val DATE = 5
        const val DATETIME = 6
        const val OPTION = 7
        const val LIST = 8
        const val MAP = 9
        const val ENUM = 10
        const val STRUCT = 11

        private val dateRE = Regex("""^\d{4}-\d{2}-\d{2}$""")
        private val datetimeRE = Regex("""^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$""")

        fun of(kind: Int, elem: SnglCodec? = null): SnglCodec = SnglCodec(kind).also { it.elem = elem }

        private val scope = kotlinx.coroutines.MainScope()

        // How many bodies start began are still running: a test waits for none
        // before it reads what they wrote.
        val active = java.util.concurrent.atomic.AtomicInteger()

        // Starts a body that waits -- a handler, an effect's mount -- on the
        // thread the target draws on, which a wait leaves and comes back to.
        fun start(block: suspend () -> Unit) {
            active.incrementAndGet()
            scope.launch {
                try {
                    block()
                } finally {
                    active.decrementAndGet()
                }
            }
        }

        fun decode(text: String, c: SnglCodec): Decoded {
            val v: Any? = try {
                val t = JSONTokener(text)
                val parsed = t.nextValue()
                if (t.nextClean() != '\u0000') throw Fail("not JSON")
                parsed
            } catch (e: Exception) {
                return Decoded(null, "not JSON")
            }
            return try {
                Decoded(dec(if (v == JSONObject.NULL) null else v, c, "$"), null)
            } catch (e: Fail) {
                Decoded(null, e.message)
            }
        }

        private fun describe(c: SnglCodec): String = when (c.kind) {
            BOOL -> "a boolean"
            INT, LONG -> "an integer"
            FLOAT -> "a number"
            STRING -> "a string"
            DATE -> "a date"
            DATETIME -> "a datetime"
            OPTION -> describe(c.elem!!)
            LIST -> "an array"
            ENUM -> "one of " + c.wires.joinToString(", ")
            else -> "an object"
        }

        private fun integral(v: Any?): Long? {
            if (v !is Number) return null
            val d = v.toDouble()
            if (v is Double || v is Float) {
                if (d.isNaN() || d.isInfinite() || d != Math.floor(d) || Math.abs(d) > 9.223372036854776E18) return null
                return d.toLong()
            }
            return v.toLong()
        }

        private fun dec(v: Any?, c: SnglCodec, path: String): Any? {
            fun want(): Fail = Fail(path + ": want " + describe(c))
            when (c.kind) {
                OPTION -> return if (v == null) null else dec(v, c.elem!!, path)
                BOOL -> return v as? Boolean ?: throw want()
                INT -> {
                    val n = integral(v) ?: throw want()
                    if (n < Int.MIN_VALUE || n > Int.MAX_VALUE) throw want()
                    return n.toInt()
                }
                LONG -> return integral(v) ?: throw want()
                FLOAT -> return (v as? Number)?.toDouble() ?: throw want()
                STRING -> return v as? String ?: throw want()
                DATE -> {
                    val s = v as? String ?: throw want()
                    if (!dateRE.matches(s)) throw want()
                    try { java.time.LocalDate.parse(s) } catch (e: Exception) { throw want() }
                    return s
                }
                DATETIME -> {
                    val s = v as? String ?: throw want()
                    if (!datetimeRE.matches(s)) throw want()
                    try { java.time.OffsetDateTime.parse(s.uppercase()) } catch (e: Exception) { throw want() }
                    return s
                }
                ENUM -> {
                    val i = if (v is String) c.wires.indexOf(v) else -1
                    if (i < 0) throw want()
                    return c.values[i]
                }
                LIST -> {
                    val a = v as? JSONArray ?: throw want()
                    val out = ArrayList<Any?>(a.length())
                    for (i in 0 until a.length()) out.add(dec(unwrap(a.get(i)), c.elem!!, "$path[$i]"))
                    return out
                }
                MAP -> {
                    val o = v as? JSONObject ?: throw want()
                    val out = LinkedHashMap<String, Any?>()
                    for (k in o.keys().asSequence().toList().sorted()) out[k] = dec(unwrap(o.get(k)), c.elem!!, "$path.$k")
                    return out
                }
                STRUCT -> {
                    val o = v as? JSONObject ?: throw want()
                    val args = ArrayList<Any?>(c.fields.size)
                    for (f in c.fields) {
                        val present = o.has(f.wire)
                        if (!present && f.codec.kind != OPTION) throw Fail("$path.${f.wire}: missing")
                        args.add(dec(if (present) unwrap(o.get(f.wire)) else null, f.codec, "$path.${f.wire}"))
                    }
                    return c.make!!(args)
                }
            }
            throw want()
        }

        private fun unwrap(v: Any?): Any? = if (v == JSONObject.NULL) null else v

        fun encode(v: Any?, c: SnglCodec): String = StringBuilder().also { enc(it, v, c) }.toString()

        private fun enc(b: StringBuilder, v: Any?, c: SnglCodec) {
            when (c.kind) {
                OPTION -> if (v == null) b.append("null") else enc(b, v, c.elem!!)
                BOOL -> b.append(if (v == true) "true" else "false")
                INT, LONG -> b.append((v as Number).toLong().toString())
                FLOAT -> b.append(number((v as Number).toDouble()))
                STRING, DATE, DATETIME -> quote(b, v as String)
                ENUM -> quote(b, enumWire(c, v))
                LIST -> {
                    b.append('[')
                    (v as List<*>).forEachIndexed { i, x ->
                        if (i > 0) b.append(',')
                        enc(b, x, c.elem!!)
                    }
                    b.append(']')
                }
                MAP -> {
                    val m = v as Map<*, *>
                    b.append('{')
                    m.keys.map { it as String }.sorted().forEachIndexed { i, k ->
                        if (i > 0) b.append(',')
                        quote(b, k)
                        b.append(':')
                        enc(b, m[k], c.elem!!)
                    }
                    b.append('}')
                }
                STRUCT -> {
                    val vals = c.read!!(v!!)
                    b.append('{')
                    var first = true
                    c.fields.forEachIndexed { i, f ->
                        val x = vals[i]
                        if (f.codec.kind == OPTION && x == null) return@forEachIndexed
                        if (!first) b.append(',')
                        first = false
                        quote(b, f.wire)
                        b.append(':')
                        enc(b, x, f.codec)
                    }
                    b.append('}')
                }
            }
        }

        private fun enumWire(c: SnglCodec, v: Any?): String {
            val i = c.values.indexOf(v)
            return if (i < 0) v.toString() else c.wires[i]
        }

        // A float as JSON and JavaScript write one: the shortest decimal that
        // reads back as the same value, with no fraction for a whole number and
        // an exponent from 1e21 up and below 1e-6.
        fun number(d: Double): String {
            if (d.isNaN() || d.isInfinite()) return "null"
            if (d == 0.0) return "0"
            val bd = java.math.BigDecimal(d.toString()).stripTrailingZeros()
            val a = Math.abs(d)
            if (a >= 1e-6 && a < 1e21) return bd.toPlainString()
            val digits = bd.unscaledValue().abs().toString()
            val exp = digits.length - 1 - bd.scale()
            val mant = if (digits.length > 1) digits[0] + "." + digits.substring(1) else digits
            return (if (d < 0) "-" else "") + mant + "e" + (if (exp >= 0) "+" else "-") + Math.abs(exp)
        }

        private fun quote(b: StringBuilder, s: String) {
            b.append('"')
            for (ch in s) {
                when (ch) {
                    '"' -> b.append("\\\"")
                    '\\' -> b.append("\\\\")
                    '\b' -> b.append("\\b")
                    '\u000C' -> b.append("\\f")
                    '\n' -> b.append("\\n")
                    '\r' -> b.append("\\r")
                    '\t' -> b.append("\\t")
                    else -> if (ch < ' ') b.append(String.format("\\u%04x", ch.code)) else b.append(ch)
                }
            }
            b.append('"')
        }

        fun escape(s: String): String {
            val hex = "0123456789ABCDEF"
            val b = StringBuilder()
            for (byte in s.toByteArray(Charsets.UTF_8)) {
                val c = byte.toInt() and 0xff
                val ch = c.toChar()
                if (ch in 'A'..'Z' || ch in 'a'..'z' || ch in '0'..'9' || ch == '-' || ch == '.' || ch == '_' || ch == '~') {
                    b.append(ch)
                } else {
                    b.append('%').append(hex[c shr 4]).append(hex[c and 15])
                }
            }
            return b.toString()
        }

        private fun queryValues(v: Any?, c: SnglCodec): List<String> = when (c.kind) {
            OPTION -> if (v == null) emptyList() else queryValues(v, c.elem!!)
            LIST -> (v as List<*>).flatMap { queryValues(it, c.elem!!) }
            BOOL -> listOf(if (v == true) "true" else "false")
            INT, LONG -> listOf((v as Number).toLong().toString())
            FLOAT -> listOf(number((v as Number).toDouble()))
            ENUM -> listOf(enumWire(c, v))
            else -> listOf(v.toString())
        }

        fun withQuery(url: String, q: Any, c: SnglCodec): String {
            val vals = c.read!!(q)
            val parts = ArrayList<String>()
            c.fields.forEachIndexed { i, f ->
                for (s in queryValues(vals[i], f.codec)) parts.add(escape(f.wire) + "=" + escape(s))
            }
            if (parts.isEmpty()) return url
            return url + (if (url.contains('?')) "&" else "?") + parts.joinToString("&")
        }

        // Sends one request on a thread of its own and waits for its answer
        // there: a failure only where no response arrived.
        suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): Response =
            withContext(Dispatchers.IO) {
                try {
                    val conn = URL(url).openConnection() as HttpURLConnection
                    conn.requestMethod = method.uppercase()
                    for (k in headers.keys.sorted()) conn.setRequestProperty(k, headers[k])
                    if (body != null) {
                        conn.doOutput = true
                        conn.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
                    }
                    val status = conn.responseCode
                    val stream = if (status >= 400) conn.errorStream else conn.inputStream
                    val text = stream?.use { it.readBytes().toString(Charsets.UTF_8) } ?: ""
                    val hs = LinkedHashMap<String, String>()
                    for ((k, vs) in conn.headerFields) {
                        if (k != null) hs[k.lowercase()] = vs.joinToString(", ")
                    }
                    conn.disconnect()
                    Response(status, hs, text, false, false, "")
                } catch (e: SocketTimeoutException) {
                    Response(0, emptyMap(), "", true, true, e.toString())
                } catch (e: Exception) {
                    Response(0, emptyMap(), "", true, false, e.toString())
                }
            }
    }
}

class SnglCodecField(val wire: String, val codec: SnglCodec)
