package PACKAGE

import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteAbortException
import android.database.sqlite.SQLiteAccessPermException
import android.database.sqlite.SQLiteBindOrColumnIndexOutOfRangeException
import android.database.sqlite.SQLiteBlobTooBigException
import android.database.sqlite.SQLiteCantOpenDatabaseException
import android.database.sqlite.SQLiteConstraintException
import android.database.sqlite.SQLiteCursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteDatabaseCorruptException
import android.database.sqlite.SQLiteDatabaseLockedException
import android.database.sqlite.SQLiteDatatypeMismatchException
import android.database.sqlite.SQLiteDiskIOException
import android.database.sqlite.SQLiteException
import android.database.sqlite.SQLiteFullException
import android.database.sqlite.SQLiteOutOfMemoryException
import android.database.sqlite.SQLiteProgram
import android.database.sqlite.SQLiteReadOnlyDatabaseException
import android.os.CancellationSignal
import android.os.OperationCanceledException
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

// sngl:sql's runtime on android: SQLite, through the platform's own
// SQLiteDatabase, one connection per source held for the app's life. A
// relative path is the app's database directory's once MainActivity has
// handed over its context; a test, which never does, opens it where it runs.
object SnglSql {
    class Result(
        val rows: String,
        val changed: Long,
        val lastId: Long,
        val failed: Boolean,
        val cancelled: Boolean,
        val code: String,
        val message: String,
    )

    private var context: Context? = null
    private val dbs = HashMap<String, SQLiteDatabase>()

    fun init(c: Context) {
        context = c.applicationContext
    }

    @Synchronized
    private fun open(source: String): SQLiteDatabase {
        dbs[source]?.let { return it }
        val db = if (source.startsWith(":memory:") || source.isEmpty()) {
            SQLiteDatabase.create(null)
        } else {
            val file = if (source.startsWith("/")) File(source) else context?.getDatabasePath(source) ?: File(source)
            file.parentFile?.mkdirs()
            SQLiteDatabase.openOrCreateDatabase(file, null)
        }
        dbs[source] = db
        return db
    }

    private fun bind(p: SQLiteProgram, args: String) {
        val a = JSONArray(if (args.isBlank()) "[]" else args)
        for (i in 0 until a.length()) {
            when (val v = a.get(i)) {
                JSONObject.NULL -> p.bindNull(i + 1)
                is Boolean -> p.bindLong(i + 1, if (v) 1L else 0L)
                is Int -> p.bindLong(i + 1, v.toLong())
                is Long -> p.bindLong(i + 1, v)
                is Double -> p.bindDouble(i + 1, v)
                is Number -> p.bindDouble(i + 1, v.toDouble())
                else -> p.bindString(i + 1, v.toString())
            }
        }
    }

    // The name of the SQLite result code a platform exception stands for,
    // as the Go runtime gives it, for sql.classify to read.
    private fun code(e: Exception): String = when (e) {
        is SQLiteConstraintException -> "SQLITE_CONSTRAINT"
        is SQLiteDatatypeMismatchException -> "SQLITE_MISMATCH"
        is SQLiteBindOrColumnIndexOutOfRangeException -> "SQLITE_RANGE"
        is SQLiteBlobTooBigException -> "SQLITE_TOOBIG"
        is SQLiteReadOnlyDatabaseException -> "SQLITE_READONLY"
        is SQLiteAccessPermException -> "SQLITE_PERM"
        is SQLiteDatabaseLockedException -> "SQLITE_BUSY"
        is SQLiteCantOpenDatabaseException -> "SQLITE_CANTOPEN"
        is SQLiteDatabaseCorruptException -> "SQLITE_CORRUPT"
        is SQLiteDiskIOException -> "SQLITE_IOERR"
        is SQLiteFullException -> "SQLITE_FULL"
        is SQLiteOutOfMemoryException -> "SQLITE_NOMEM"
        is SQLiteAbortException -> "SQLITE_ABORT"
        is SQLiteException -> "SQLITE_ERROR"
        else -> ""
    }

    private fun failed(e: Exception, life: Int): Result {
        if (e is OperationCanceledException || SnglCodec.ended(life)) return Result("", 0, 0, true, true, "", "cancelled")
        return Result("", 0, 0, true, false, code(e), e.message ?: e.toString())
    }

    private fun refused(engine: String): Result? =
        if (engine == "sqlite") null else Result("", 0, 0, true, false, "", "android reaches SQLite alone, not $engine")

    // A lifetime ended before the statement runs answers cancelled, and so
    // does one ended while it ran: its answer is dropped, as a request's is.
    private suspend fun run(life: Int, body: (CancellationSignal) -> Result): Result {
        if (SnglCodec.ended(life)) return Result("", 0, 0, true, true, "", "cancelled")
        return withContext(Dispatchers.IO) {
            val r = try {
                body(CancellationSignal())
            } catch (e: Exception) {
                failed(e, life)
            }
            if (SnglCodec.ended(life)) Result("", 0, 0, true, true, "", "cancelled") else r
        }
    }

    suspend fun query(engine: String, source: String, text: String, args: String, columns: List<String>, kinds: List<String>, life: Int): Result {
        refused(engine)?.let { return it }
        return run(life) { signal ->
            val db = open(source)
            val factory = SQLiteDatabase.CursorFactory { _, driver, table, q ->
                bind(q, args)
                SQLiteCursor(driver, table, q)
            }
            val out = StringBuilder("[")
            db.rawQueryWithFactory(factory, text, null, "", signal).use { c ->
                var first = true
                while (c.moveToNext()) {
                    if (!first) out.append(',')
                    first = false
                    out.append('{')
                    for (i in 0 until c.columnCount) {
                        if (i > 0) out.append(',')
                        out.append(JSONObject.quote(if (i < columns.size) columns[i] else c.getColumnName(i)))
                        out.append(':')
                        out.append(value(c, i, if (i < kinds.size) kinds[i] else ""))
                    }
                    out.append('}')
                }
            }
            out.append(']')
            Result(out.toString(), 0, 0, false, false, "", "")
        }
    }

    suspend fun exec(engine: String, source: String, text: String, args: String, life: Int): Result {
        refused(engine)?.let { return it }
        return run(life) { _ ->
            val db = open(source)
            // One transaction holds one connection, so changes() and
            // last_insert_rowid() read what this statement did.
            db.beginTransactionNonExclusive()
            try {
                db.compileStatement(text).use { st ->
                    bind(st, args)
                    st.execute()
                }
                val changed = db.compileStatement("SELECT changes()").use { it.simpleQueryForLong() }
                val last = db.compileStatement("SELECT last_insert_rowid()").use { it.simpleQueryForLong() }
                db.setTransactionSuccessful()
                Result("", changed, last, false, false, "", "")
            } finally {
                db.endTransaction()
            }
        }
    }

    private fun value(c: Cursor, i: Int, kind: String): String {
        val t = c.getType(i)
        if (t == Cursor.FIELD_TYPE_NULL) return "null"
        when (kind) {
            "bool" -> return when (t) {
                Cursor.FIELD_TYPE_INTEGER -> (c.getLong(i) != 0L).toString()
                Cursor.FIELD_TYPE_FLOAT -> (c.getDouble(i) != 0.0).toString()
                else -> c.getString(i).let { it != "0" && it != "" && it != "f" && it != "false" }.toString()
            }
            "datetime", "date" -> time(c, i, t, kind)?.let { return JSONObject.quote(it) }
        }
        return when (t) {
            Cursor.FIELD_TYPE_INTEGER -> c.getLong(i).toString()
            Cursor.FIELD_TYPE_FLOAT -> JSONObject.numberToString(c.getDouble(i))
            Cursor.FIELD_TYPE_BLOB -> JSONObject.quote(android.util.Base64.encodeToString(c.getBlob(i), android.util.Base64.NO_WRAP))
            else -> JSONObject.quote(c.getString(i))
        }
    }

    // A time as RFC 3339, from any of the forms SQLite stores one as: its
    // own `YYYY-MM-DD HH:MM:SS`, RFC 3339 itself, or seconds since the epoch.
    private fun time(c: Cursor, i: Int, t: Int, kind: String): String? {
        val text = if (t == Cursor.FIELD_TYPE_INTEGER) {
            java.time.Instant.ofEpochSecond(c.getLong(i)).toString()
        } else {
            c.getString(i).trim()
        }
        if (text.length < 10) return null
        if (kind == "date") return text.substring(0, 10)
        if (text.length == 10) return text + "T00:00:00Z"
        var s = text.replace(' ', 'T')
        if (!s.endsWith("Z") && !Regex("[+-]\\d\\d:\\d\\d$").containsMatchIn(s)) s += "Z"
        return s
    }
}
