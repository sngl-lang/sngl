package PACKAGE

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.focus.onFocusChanged

// snglCommitsOnBlur commits a field as the focus leaves it, if its text
// differs from what it held when the focus arrived: html's change event.
fun Modifier.snglCommitsOnBlur(text: String, onCommit: () -> Unit): Modifier = composed {
    var atFocus by remember { mutableStateOf<String?>(null) }
    val current by rememberUpdatedState(text)
    val commit by rememberUpdatedState(onCommit)
    onFocusChanged {
        val held = atFocus
        if (it.isFocused) {
            if (held == null) atFocus = current
        } else if (held != null) {
            atFocus = null
            if (held != current) commit()
        }
    }
}
