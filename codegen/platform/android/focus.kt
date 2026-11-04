package PACKAGE

import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusProperties

// snglUnfocusable keeps the control it is put on from taking focus, by a key
// or by a press: a component written `focusable=false`. A key the control
// would have taken, focused, reaches the window instead. Compose says it in a
// lambda with a receiver, which SNGL has no way to write.
fun Modifier.snglUnfocusable(): Modifier = focusProperties { canFocus = false }
