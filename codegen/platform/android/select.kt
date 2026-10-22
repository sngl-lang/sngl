package PACKAGE

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.setText

// SnglSelect is sngl:ui's select: a field showing the label chosen, which a
// tap anywhere on opens a menu of the choices. The field is read-only -- a
// select chooses among its choices, and a field a user types into is a
// different control. Picking one in the menu reports it. A test chooses as a
// keyboard would, by setting the field's text to a choice's label, which the
// field's semantics answer as a pick; a label that names no choice, or is
// already the value, reports nothing.
@Composable
fun SnglSelect(
    value: String,
    options: List<String>,
    label: String,
    enabled: Boolean,
    modifier: Modifier,
    onValueChange: (String) -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    val choose = { v: String ->
        open = false
        if (v != value && v in options) {
            onValueChange(v)
        }
    }
    Box(
        modifier = modifier.semantics {
            role = Role.DropdownList
            setText { typed ->
                choose(typed.text)
                true
            }
        },
    ) {
        OutlinedTextField(
            value = value,
            onValueChange = {},
            readOnly = true,
            enabled = enabled,
            singleLine = true,
            label = if (label.isEmpty()) null else ({ Text(label) }),
            trailingIcon = { Text("▾") },
        )
        // A read-only field still takes a tap as its own, to place a cursor,
        // so the tap that opens the menu is a layer over it.
        Box(Modifier.matchParentSize().clickable(enabled = enabled) { open = true })
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for (o in options) {
                DropdownMenuItem(text = { Text(o) }, onClick = { choose(o) })
            }
        }
    }
}
