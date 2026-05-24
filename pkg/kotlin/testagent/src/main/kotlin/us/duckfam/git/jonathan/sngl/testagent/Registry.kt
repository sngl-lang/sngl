// Test registry — name → function. Codegen-emitted init blocks populate
// this at module load time so the agent's `list` RPC returns every
// available test name.

package us.duckfam.git.jonathan.sngl.testagent

object Registry {
    private val lock = Any()
    private val byName = linkedMapOf<String, (T) -> Unit>()

    fun register(name: String, fn: (T) -> Unit) {
        synchronized(lock) { byName[name] = fn }
    }

    fun get(name: String): ((T) -> Unit)? {
        synchronized(lock) { return byName[name] }
    }

    fun names(): List<String> {
        synchronized(lock) { return byName.keys.sorted() }
    }

    /** Test-only helper. */
    internal fun clear() {
        synchronized(lock) { byName.clear() }
    }
}
