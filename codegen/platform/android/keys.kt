package PACKAGE

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.Window
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext

// snglNamedKeys are the keys a window's `@key` names rather than reporting the
// character they type, by their key code.
private val snglNamedKeys = mapOf(
    KeyEvent.KEYCODE_ENTER to "Enter",
    KeyEvent.KEYCODE_NUMPAD_ENTER to "Enter",
    KeyEvent.KEYCODE_ESCAPE to "Escape",
    KeyEvent.KEYCODE_DEL to "Backspace",
    KeyEvent.KEYCODE_FORWARD_DEL to "Delete",
    KeyEvent.KEYCODE_DPAD_UP to "ArrowUp",
    KeyEvent.KEYCODE_DPAD_DOWN to "ArrowDown",
    KeyEvent.KEYCODE_DPAD_LEFT to "ArrowLeft",
    KeyEvent.KEYCODE_DPAD_RIGHT to "ArrowRight",
    KeyEvent.KEYCODE_MOVE_HOME to "Home",
    KeyEvent.KEYCODE_MOVE_END to "End",
    KeyEvent.KEYCODE_PAGE_UP to "PageUp",
    KeyEvent.KEYCODE_PAGE_DOWN to "PageDown",
)

// snglKeyName is what a window's `@key` calls the key event is of, as a
// browser's KeyboardEvent.key names it: one of snglNamedKeys, or the character
// it types. It is null for a key the window does not hear: one pressed with
// Control, Alt or Meta held, Tab, and one typing no character, a dead key's
// accent among them.
fun snglKeyName(event: KeyEvent): String? {
    if (event.isCtrlPressed || event.isAltPressed || event.isMetaPressed) {
        return null
    }
    snglNamedKeys[event.keyCode]?.let { return it }
    val c = event.unicodeChar
    if (c == 0 || c and KeyCharacterMap.COMBINING_ACCENT != 0 || Character.isISOControl(c)) {
        return null
    }
    return String(Character.toChars(c))
}

// SnglWindowKeys hands onKey each key pressed in the activity's window that
// nothing in it took. The activity's window callback is what the decor view
// hands a key to, and the activity hands it to its views, the focused
// composable first: a field takes what is typed into it, a focused button the
// Enter and space that press it, and Compose the Tab and arrows that move
// focus. What they leave reaches the callback this puts in front of the
// activity's while it is composed, which reports a key's press and takes its
// release with it. An Escape is left to the system while passEscape holds,
// which answers one no one took with the back that goes back.
@Composable
fun SnglWindowKeys(passEscape: Boolean, onKey: (String) -> Unit) {
    val activity = LocalContext.current.snglActivity() ?: return
    val report by rememberUpdatedState(onKey)
    val escapes by rememberUpdatedState(passEscape)
    DisposableEffect(activity) {
        val window = activity.window
        val inner = window.callback
        val outer = object : Window.Callback by inner {
            override fun dispatchKeyEvent(event: KeyEvent): Boolean {
                if (inner.dispatchKeyEvent(event)) {
                    return true
                }
                val name = snglKeyName(event) ?: return false
                if (name == "Escape" && escapes) {
                    return false
                }
                if (event.action == KeyEvent.ACTION_DOWN) {
                    report(name)
                }
                return true
            }
        }
        window.callback = outer
        onDispose {
            if (window.callback === outer) {
                window.callback = inner
            }
        }
    }
}

private fun Context.snglActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.snglActivity()
    else -> null
}

// snglPressKey presses the key a window's `@key` names name, as a hardware
// keyboard would: its press and release, handed to the activity's decor view,
// which is where the system hands them. A character is typed with the keys
// the virtual keyboard's map types it with, shift and all. A test presses
// keys with it.
fun snglPressKey(activity: Activity, name: String) {
    val code = snglNamedKeys.entries.firstOrNull { it.value == name }?.key
    val events = if (code != null) {
        arrayOf(KeyEvent(KeyEvent.ACTION_DOWN, code), KeyEvent(KeyEvent.ACTION_UP, code))
    } else {
        KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD).getEvents(name.toCharArray()) ?: emptyArray()
    }
    val decor = activity.window.decorView
    for (event in events) {
        decor.dispatchKeyEvent(event)
    }
}
