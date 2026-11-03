package PACKAGE

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.os.SystemClock
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.view.Window
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.ViewRootForTest
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull

// snglNamedKeys are the keys a window's `@key` names rather than reporting the
// character they type, by their key code.
private val snglNamedKeys = mapOf(
    KeyEvent.KEYCODE_ENTER to "Enter",
    KeyEvent.KEYCODE_NUMPAD_ENTER to "Enter",
    KeyEvent.KEYCODE_TAB to "Tab",
    KeyEvent.KEYCODE_ESCAPE to "Escape",
    KeyEvent.KEYCODE_DEL to "Backspace",
    KeyEvent.KEYCODE_FORWARD_DEL to "Delete",
    KeyEvent.KEYCODE_INSERT to "Insert",
    KeyEvent.KEYCODE_DPAD_UP to "ArrowUp",
    KeyEvent.KEYCODE_DPAD_DOWN to "ArrowDown",
    KeyEvent.KEYCODE_DPAD_LEFT to "ArrowLeft",
    KeyEvent.KEYCODE_DPAD_RIGHT to "ArrowRight",
    KeyEvent.KEYCODE_MOVE_HOME to "Home",
    KeyEvent.KEYCODE_MOVE_END to "End",
    KeyEvent.KEYCODE_PAGE_UP to "PageUp",
    KeyEvent.KEYCODE_PAGE_DOWN to "PageDown",
) + (1..12).associate { KeyEvent.KEYCODE_F1 + it - 1 to "F$it" }

// The modifiers a key's own character is read without: with Control, Alt or
// Meta held it is the key's character, "s" for Control+S, as a browser names
// it, where Alt would type another on some keyboards and Control none.
private const val snglShortcutMeta = KeyEvent.META_CTRL_MASK or KeyEvent.META_ALT_MASK or KeyEvent.META_META_MASK

// snglKeyName is what a window's `@key` calls the key event is of, as a
// browser's KeyboardEvent.key names it: one of snglNamedKeys, or the character
// it types. It is null for a key the window does not hear: a modifier alone,
// a key the system keeps -- Home, Back, the volume, power and app switch keys,
// none of which types a character -- and one typing no character, a dead
// key's accent among them.
fun snglKeyName(event: KeyEvent): String? {
    snglNamedKeys[event.keyCode]?.let { return it }
    val c = event.getUnicodeChar(event.metaState and snglShortcutMeta.inv())
    if (c == 0 || c and KeyCharacterMap.COMBINING_ACCENT != 0 || Character.isISOControl(c)) {
        return null
    }
    return String(Character.toChars(c))
}

// SnglKeyPress is a key as the window hears it, which press turns into the
// program's own KeyPress.
class SnglKeyPress(
    val key: String,
    val ctrl: Boolean,
    val alt: Boolean,
    val shift: Boolean,
    val meta: Boolean,
    val editing: Boolean,
)

// snglWindowHears is the window's `@key` while it is composed, handed a
// test's own KeyPress and reporting whether it stopped it, and snglKeysMuted
// holds while snglPressKey presses on a key the handler has heard, so it does
// not hear the key again.
private var snglWindowHears: ((Any?) -> Boolean)? = null
private var snglKeysMuted = false

// SnglWindowKeys hands onKey each key pressed in the activity's window before
// anything in it hears the key. The activity's window callback is what the
// decor view hands a key to, and the activity hands it on to its views, the
// focused composable first: a field types it, a focused button presses on
// its Enter, and Compose moves focus on Tab and the arrows. The
// callback this puts in front of the activity's while it is composed asks
// onKey first, at the key's press, and a key onKey stops reaches nothing
// else, its press nor its release, so a button that presses on the one and
// clicks on the other does neither. A stopped key's repeats are stopped as
// they arrive. A key the input method takes -- a hardware key composing text
// in one -- is handed the input method before any view and never arrives
// here; nor does a key the on-screen keyboard types, which reaches a field
// through its input connection rather than as a key event.
//
// An Escape onKey leaves, which nothing took, is the system's back while
// passEscape holds: the system answers it with the back that goes back.
// While it does not, it ends here, as an Escape ends at a desktop window,
// rather than being the back that closes the window. The system back key
// itself is not a key the window hears: it is OnBackPressedDispatcher's, and
// the window's `@back`.
@Composable
fun <E> SnglWindowKeys(passEscape: Boolean, press: (SnglKeyPress) -> E, onKey: (E, () -> Unit) -> Unit) {
    val activity = LocalContext.current.snglActivity() ?: return
    val report by rememberUpdatedState(onKey)
    val build by rememberUpdatedState(press)
    val escapes by rememberUpdatedState(passEscape)
    DisposableEffect(activity) {
        val window = activity.window
        val inner = window.callback
        val stopped = mutableSetOf<Int>()
        val hear = { e: E ->
            var stop = false
            report(e) { stop = true }
            stop
        }
        val outer = object : Window.Callback by inner {
            override fun dispatchKeyEvent(event: KeyEvent): Boolean {
                val name = snglKeyName(event) ?: return inner.dispatchKeyEvent(event)
                when {
                    snglKeysMuted -> {}
                    event.action == KeyEvent.ACTION_DOWN -> {
                        val stop = hear(build(SnglKeyPress(
                            key = name,
                            ctrl = event.isCtrlPressed,
                            alt = event.isAltPressed,
                            shift = event.isShiftPressed,
                            meta = event.isMetaPressed,
                            editing = activity.snglEditing(),
                        )))
                        if (stop) {
                            stopped.add(event.keyCode)
                            return true
                        }
                        stopped.remove(event.keyCode)
                    }
                    event.action == KeyEvent.ACTION_UP && stopped.remove(event.keyCode) -> return true
                }
                if (inner.dispatchKeyEvent(event)) {
                    return true
                }
                return name == "Escape" && !escapes
            }
        }
        window.callback = outer
        @Suppress("UNCHECKED_CAST")
        val heard = { e: Any? -> hear(e as E) }
        snglWindowHears = heard
        onDispose {
            if (window.callback === outer) {
                window.callback = inner
            }
            if (snglWindowHears === heard) {
                snglWindowHears = null
            }
        }
    }
}

private fun Context.snglActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.snglActivity()
    else -> null
}

// snglEditing reports whether focus is in a field that takes typing. A
// Compose text field is no view of its own -- the view with the focus is the
// composition's -- so it is asked of the focused composition's semantics,
// where the focused field says whether it is editable: a read-only one, a
// select's box, is not.
private fun Activity.snglEditing(): Boolean =
    snglFocusedRoots(window.decorView).any { it.semanticsOwner.unmergedRootSemanticsNode.snglEditing() }

// snglFocusedRoots are the compositions under v whose view has the focus. The
// window's own answer, its currentFocus, is not asked, since under
// Robolectric it is null while a composition holds the focus.
private fun snglFocusedRoots(v: View): List<ViewRootForTest> = when {
    v is ViewRootForTest -> if (v.view.isFocused) listOf(v) else emptyList()
    v is ViewGroup -> (0 until v.childCount).flatMap { snglFocusedRoots(v.getChildAt(it)) }
    else -> emptyList()
}

private fun SemanticsNode.snglEditing(): Boolean {
    if (config.getOrNull(SemanticsProperties.Focused) == true && config.getOrNull(SemanticsProperties.IsEditable) == true) {
        return true
    }
    return children.any { it.snglEditing() }
}

// snglPressKey presses a test's key: the window's `@key` hears e, the
// test's own KeyPress, as it is given, and a key it stops goes no further.
// One it leaves is pressed as a hardware keyboard would press the key name
// names, with the modifiers given held -- its press and release handed to the
// activity's decor view, which is where the system hands them, the handler
// not hearing it again -- so it goes where it would have gone. A character is
// typed with the keys the virtual keyboard's map types it with, shift and
// all. It reports whether the handler stopped the key.
fun snglPressKey(activity: Activity, e: Any?, name: String, ctrl: Boolean, alt: Boolean, shift: Boolean, meta: Boolean): Boolean {
    if (snglWindowHears?.invoke(e) == true) {
        return true
    }
    var mods = 0
    if (ctrl) mods = mods or KeyEvent.META_CTRL_ON or KeyEvent.META_CTRL_LEFT_ON
    if (alt) mods = mods or KeyEvent.META_ALT_ON or KeyEvent.META_ALT_LEFT_ON
    if (shift) mods = mods or KeyEvent.META_SHIFT_ON or KeyEvent.META_SHIFT_LEFT_ON
    if (meta) mods = mods or KeyEvent.META_META_ON or KeyEvent.META_META_LEFT_ON
    val code = snglNamedKeys.entries.firstOrNull { it.value == name }?.key
    val now = SystemClock.uptimeMillis()
    val events = if (code != null) {
        arrayOf(KeyEvent(now, now, KeyEvent.ACTION_DOWN, code, 0, mods), KeyEvent(now, now, KeyEvent.ACTION_UP, code, 0, mods))
    } else {
        val typed = KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD).getEvents(name.toCharArray()) ?: emptyArray()
        typed.map { KeyEvent(it.downTime, it.eventTime, it.action, it.keyCode, it.repeatCount, it.metaState or mods, it.deviceId, it.scanCode, it.flags, it.source) }.toTypedArray()
    }
    // A key a keyboard presses leaves touch mode, out of which Compose lets
    // a button take the focus Tab moves; the system leaves it as the key
    // arrives, before the decor view is handed it, and this as a focused
    // view does, keeping the focus where it is.
    snglFocusedRoots(activity.window.decorView).forEach { it.view.requestFocusFromTouch() }
    val decor = activity.window.decorView
    snglKeysMuted = true
    try {
        for (event in events) {
            decor.dispatchKeyEvent(event)
        }
    } finally {
        snglKeysMuted = false
    }
    return false
}
