package PACKAGE

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed

// LocalFlexShare is what a child's flex is a share of in a Column the
// generated code writes rather than a vbox -- a window's nodes, a page's, a
// window's content arriving through a slot -- provided in that Column's
// content, where weight is in scope. A slot's content is composed where the
// slot is invoked, so it reads the Column it lands in, which no closure its
// caller wrote could name.
val LocalFlexShare = compositionLocalOf<((Float) -> Modifier)?> { null }

// snglFlexShare is sngl:ui's flexShare where no vbox or hbox provides one:
// the written Column's share, else the screen. A value rather than a
// composable read, since a test build holds the context in a state class
// built outside composition; the modifier reads LocalFlexShare where the node
// it is on is composed.
val snglFlexShare: (Float) -> Modifier = { w ->
    Modifier.composed { LocalFlexShare.current?.invoke(w) ?: Modifier.fillMaxSize() }
}
