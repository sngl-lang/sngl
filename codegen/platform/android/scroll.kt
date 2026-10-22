package PACKAGE

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onSizeChanged

// SnglScroll is sngl:ui's scroll: a column scrolled vertically, which calls
// onReachEnd when the end of its content comes within half a viewport of
// being shown -- once as it arrives, and again whenever the content changes
// size while it is there, so a handler that adds a page is asked for another
// while the end still shows. The content's height is measured past the
// scroll, where it is the whole of it rather than the viewport.
@Composable
fun SnglScroll(
    modifier: Modifier,
    onReachEnd: () -> Unit = {},
    content: @Composable ColumnScope.() -> Unit,
) {
    val state = rememberScrollState()
    var height by remember { mutableIntStateOf(0) }
    val reach by rememberUpdatedState(onReachEnd)
    LaunchedEffect(state) {
        snapshotFlow { (state.maxValue - state.value <= state.viewportSize / 2) to height }
            .collect { (atEnd, _) -> if (atEnd) reach() }
    }
    Column(modifier.verticalScroll(state).onSizeChanged { height = it.height }, content = content)
}
