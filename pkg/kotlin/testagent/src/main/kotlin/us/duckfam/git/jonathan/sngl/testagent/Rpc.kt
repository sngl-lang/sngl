// JSON-RPC 2.0 line-delimited codec for the SNGL Kotlin testagent.
//
// Mirrors internal/testrpc (Go) and pkg/go/testagent's use of it:
//   - one JSON object per line on stdin/stdout (or TCP socket)
//   - notifications: no `id`, fire-and-forget
//   - requests: numeric `id`, expect a paired response
//   - responses: same `id`, exactly one of `result` or `error`
//
// Writes are synchronised so concurrent T handles can safely emit
// notifications + requests against a shared writer.

package us.duckfam.git.jonathan.sngl.testagent

import org.json.JSONObject
import java.io.BufferedReader
import java.io.InputStream
import java.io.InputStreamReader
import java.io.OutputStream
import java.io.OutputStreamWriter
import java.io.Writer
import java.util.concurrent.atomic.AtomicLong

/** Decoded JSON-RPC 2.0 message. Exactly one of (method,params) / (result|error) is meaningful. */
data class RpcMessage(
    val method: String? = null,
    val id: Long? = null,
    val params: JSONObject? = null,
    val result: JSONObject? = null,
    val error: RpcError? = null,
) {
    fun isNotification(): Boolean = method != null && id == null
    fun isRequest(): Boolean = method != null && id != null
    fun isResponse(): Boolean = method == null && id != null
}

data class RpcError(val code: Int, val message: String)

class RpcException(message: String) : RuntimeException(message)

/** Line-by-line JSON-RPC reader. Read() returns null on EOF. */
class RpcReader(input: InputStream) {
    private val reader: BufferedReader = BufferedReader(InputStreamReader(input, Charsets.UTF_8))

    fun read(): RpcMessage? {
        val line = reader.readLine() ?: return null
        if (line.isBlank()) return read()
        val obj = try {
            JSONObject(line)
        } catch (e: Exception) {
            throw RpcException("malformed json-rpc line: ${e.message}")
        }
        val method = if (obj.has("method") && !obj.isNull("method")) obj.getString("method") else null
        val id = if (obj.has("id") && !obj.isNull("id")) obj.getLong("id") else null
        val params = if (obj.has("params") && !obj.isNull("params")) obj.getJSONObject("params") else null
        val result = if (obj.has("result") && !obj.isNull("result")) obj.getJSONObject("result") else null
        val error = if (obj.has("error") && !obj.isNull("error")) {
            val e = obj.getJSONObject("error")
            RpcError(e.optInt("code"), e.optString("message"))
        } else null
        return RpcMessage(method, id, params, result, error)
    }
}

/** Line-by-line JSON-RPC writer. notify/request/respond all serialise as a single JSON line. */
class RpcWriter(output: OutputStream) {
    private val writer: Writer = OutputStreamWriter(output, Charsets.UTF_8)
    private val lock = Any()
    private val nextId = AtomicLong(1)

    /** Allocates a request id without sending anything. Used to register a Pending channel before write. */
    fun allocId(): Long = nextId.getAndIncrement()

    fun notify(method: String, params: Map<String, Any?>) {
        val obj = JSONObject()
        obj.put("jsonrpc", "2.0")
        obj.put("method", method)
        obj.put("params", paramsToJson(params))
        writeLine(obj.toString())
    }

    /** Convenience: allocates an id and sends the request. */
    fun request(method: String, params: Map<String, Any?>): Long {
        val id = allocId()
        requestWithId(id, method, params)
        return id
    }

    /** Sends a request using a previously-allocated id. */
    fun requestWithId(id: Long, method: String, params: Map<String, Any?>) {
        val obj = JSONObject()
        obj.put("jsonrpc", "2.0")
        obj.put("id", id)
        obj.put("method", method)
        obj.put("params", paramsToJson(params))
        writeLine(obj.toString())
    }

    fun respond(id: Long, result: Map<String, Any?>?, error: RpcError?) {
        val obj = JSONObject()
        obj.put("jsonrpc", "2.0")
        obj.put("id", id)
        if (error != null) {
            val e = JSONObject()
            e.put("code", error.code)
            e.put("message", error.message)
            obj.put("error", e)
        } else {
            obj.put("result", paramsToJson(result ?: emptyMap()))
        }
        writeLine(obj.toString())
    }

    private fun writeLine(s: String) {
        synchronized(lock) {
            writer.write(s)
            writer.write("\n")
            writer.flush()
        }
    }
}

internal fun paramsToJson(params: Map<String, Any?>): JSONObject {
    val obj = JSONObject()
    for ((k, v) in params) {
        obj.put(k, jsonValue(v))
    }
    return obj
}

private fun jsonValue(v: Any?): Any {
    if (v == null) return JSONObject.NULL
    return when (v) {
        is Map<*, *> -> {
            val o = JSONObject()
            for ((k, vv) in v) o.put(k.toString(), jsonValue(vv))
            o
        }
        is List<*> -> {
            val a = org.json.JSONArray()
            for (vv in v) a.put(jsonValue(vv))
            a
        }
        else -> v
    }
}
