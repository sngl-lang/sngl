package PACKAGE

import android.os.Handler
import android.os.Looper
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusEventModifierNode
import androidx.compose.ui.focus.FocusState
import androidx.compose.ui.node.ModifierNodeElement
import androidx.compose.ui.text.input.ImeAction

// The contract is SnglTextField's declaration in lib/platform/android/android.sngl.
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
    onSubmit: () -> Unit,
) {
    val commit = remember { SnglCommit() }
    SideEffect {
        commit.onCommit = onCommit
        commit.onSubmit = onSubmit
    }
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        readOnly = readOnly,
        singleLine = commitsOnDone,
        minLines = minLines,
        label = if (label.isEmpty()) null else ({ Text(label) }),
        keyboardOptions = if (commitsOnDone) KeyboardOptions(imeAction = ImeAction.Done) else KeyboardOptions.Default,
        // A non-null onDone replaces the default action, which hides the keyboard.
        keyboardActions = KeyboardActions(onDone = {
            commit.onCommit()
            commit.onSubmit()
            defaultKeyboardAction(ImeAction.Done)
        }),
        modifier = modifier.then(SnglCommitOnBlur(commit)),
    )
}

private class SnglCommit {
    var onCommit: () -> Unit = {}
    var onSubmit: () -> Unit = {}
}

private data class SnglCommitOnBlur(val commit: SnglCommit) : ModifierNodeElement<SnglCommitOnBlurNode>() {
    override fun create() = SnglCommitOnBlurNode(commit)

    override fun update(node: SnglCommitOnBlurNode) {
        node.commit = commit
    }
}

// A field removed while focused loses its focus while this node is still
// attached, so the commit waits for the main looper and asks again: a removed
// field commits nothing, as html's removed input fires no change.
private class SnglCommitOnBlurNode(var commit: SnglCommit) : Modifier.Node(), FocusEventModifierNode {
    override fun onFocusEvent(focusState: FocusState) {
        if (!focusState.isFocused && isAttached) {
            Handler(Looper.getMainLooper()).post { if (isAttached) commit.onCommit() }
        }
    }
}
