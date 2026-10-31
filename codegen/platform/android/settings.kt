package PACKAGE

import android.content.Context
import android.content.SharedPreferences

// sngl:settings' place: the app's SharedPreferences, "sngl.settings" for the
// app's own values and "sngl.settings.<scope>" for a scope's. MainActivity
// opens it; a test runs on an activity of its own and never does, so it keeps
// its values in the library's memory.
object SnglSettings {
    private var context: Context? = null

    fun init(c: Context) {
        context = c.applicationContext
    }

    fun persistent(): Boolean = context != null

    private fun prefs(scope: String): SharedPreferences? =
        context?.getSharedPreferences(if (scope.isEmpty()) "sngl.settings" else "sngl.settings.$scope", Context.MODE_PRIVATE)

    fun read(scope: String, name: String): String = prefs(scope)?.getString(name, null) ?: ""

    fun write(scope: String, name: String, text: String) {
        prefs(scope)?.edit()?.putString(name, text)?.apply()
    }
}
