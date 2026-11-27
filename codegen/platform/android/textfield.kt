package PACKAGE

import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.text.input.ImeAction

// SnglTextField is sngl:ui's input and textarea: onValueChange is each edit,
// and onCommit the text committed, as the focus leaves the field or, where
// commitsOnDone, at the keyboard's Done. Only an edit since the last commit is
// committed, so a program's write of the value, or a Done the focus then
// leaves after, commits nothing.
@Composable
fun SnglTextField(
    value: String,
    label: String,
    minLines: Int,
    readOnly: Boolean,
    commitsOnDone: Boolean,
    modifier: Modifier,
    onValueChange: (String) -> Unit,
    onCommit: () -> Unit,
) {
    val commit = remember { SnglCommit() }
    commit.onCommit = onCommit
    OutlinedTextField(
        value = value,
        onValueChange = {
            commit.edited = true
            onValueChange(it)
        },
        readOnly = readOnly,
        minLines = minLines,
        label = if (label.isEmpty()) null else ({ Text(label) }),
        keyboardOptions = if (commitsOnDone) KeyboardOptions(imeAction = ImeAction.Done) else KeyboardOptions.Default,
        // A non-null onDone replaces the default action, which hides the keyboard.
        keyboardActions = KeyboardActions(onDone = {
            commit.fire()
            defaultKeyboardAction(ImeAction.Done)
        }),
        modifier = modifier.onFocusChanged { if (!it.isFocused) commit.fire() },
    )
}

private class SnglCommit {
    var edited = false
    var onCommit: () -> Unit = {}

    fun fire() {
        if (edited) {
            edited = false
            onCommit()
        }
    }
}
