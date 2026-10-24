package PACKAGE

import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.setText

// snglChooses makes a select's box a dropdown list to accessibility and to a
// test: setting its text is choosing, and onText is handed the text set.
// Compose says it in a lambda with a receiver, which SNGL has no way to write.
fun Modifier.snglChooses(onText: (String) -> Unit): Modifier = semantics {
    role = Role.DropdownList
    setText { typed ->
        onText(typed.text)
        true
    }
}
