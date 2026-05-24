// Snapshot capture hook. The android codegen registers a per-platform
// capture function (e.g. Robolectric view → PNG, device screenshot via
// UiDevice) during agent startup. `namePrefix` is set to "robolectric/"
// or "device/" so the same test name produces a different golden file
// per testRunner.

package us.duckfam.git.jonathan.sngl.testagent

/** Capture function: returns (mime, raw bytes) or throws on failure. */
typealias SnapshotFn = () -> Pair<String, ByteArray>

object Snapshots {
    @Volatile var namePrefix: String = ""

    @Volatile private var fn: SnapshotFn? = null

    /** Register the capture function. Codegen-emitted init calls this once at startup. */
    fun register(prefix: String, fn: SnapshotFn) {
        this.namePrefix = prefix
        this.fn = fn
    }

    fun capture(): Pair<String, ByteArray>? = fn?.invoke()

    /** Test-only helper. */
    internal fun reset() {
        namePrefix = ""
        fn = null
    }
}
