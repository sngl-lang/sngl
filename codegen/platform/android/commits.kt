package PACKAGE

import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type

// snglCommitsOnShiftEnter commits a multi-line field at Shift+Enter, as fyne's
// multi-line entry submits, leaving a bare Enter the newline it types.
fun Modifier.snglCommitsOnShiftEnter(onCommit: () -> Unit): Modifier = onPreviewKeyEvent {
    if (it.key != Key.Enter || !it.isShiftPressed) {
        false
    } else {
        if (it.type == KeyEventType.KeyDown) onCommit()
        true
    }
}
