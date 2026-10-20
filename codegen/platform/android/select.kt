package PACKAGE

import androidx.compose.foundation.layout.Box
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier

// SnglSelect is sngl:ui's select: a field showing the label chosen, with a
// button beside it opening a menu of the choices. Picking one in the menu
// reports it, and so does typing a choice's label into the field, which is
// how a keyboard and a test reach it. A label typed or picked that is
// already the value reports nothing.
@Composable
fun SnglSelect(
    value: String,
    options: List<String>,
    label: String,
    enabled: Boolean,
    modifier: Modifier,
    onValueChange: (String) -> Unit,
) {
    // The text is the field's own while it names no choice, and starts again
    // from the value whenever the value changes from outside.
    var text by remember(value) { mutableStateOf(value) }
    var open by remember { mutableStateOf(false) }
    val choose = { v: String ->
        if (v != value) {
            onValueChange(v)
        }
    }
    Box {
        OutlinedTextField(
            value = text,
            onValueChange = { typed ->
                text = typed
                if (typed in options) {
                    choose(typed)
                }
            },
            enabled = enabled,
            singleLine = true,
            label = if (label.isEmpty()) null else ({ Text(label) }),
            trailingIcon = {
                TextButton(onClick = { open = true }, enabled = enabled) { Text("▾") }
            },
            modifier = modifier,
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for (o in options) {
                DropdownMenuItem(text = { Text(o) }, onClick = {
                    open = false
                    text = o
                    choose(o)
                })
            }
        }
    }
}
