package PACKAGE

import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier

// SnglPager is sngl:ui's tabs' panels: a page per tab, swiped between, every
// one composed for as long as the pager is -- beyondViewportPageCount is the
// count, so none is disposed and each keeps its state while another shows.
// `selected` moves it, and a swipe that settles on another page hands that
// page to onSelect. One settling on a disabled page goes on the way it came to
// the next page that is not, as html's swipe chooses, or back to the selected
// one where there is none.
@Composable
fun SnglPager(
    modifier: Modifier,
    selected: Int,
    disabled: List<Boolean>,
    onSelect: (Int) -> Unit,
    page: @Composable (Int) -> Unit,
) {
    val count = disabled.size
    val state = rememberPagerState(initialPage = selected.coerceIn(0, (count - 1).coerceAtLeast(0))) { count }
    val current by rememberUpdatedState(selected)
    val off by rememberUpdatedState(disabled)
    val select by rememberUpdatedState(onSelect)
    LaunchedEffect(selected) {
        if (selected in 0 until count && state.currentPage != selected) state.animateScrollToPage(selected)
    }
    LaunchedEffect(state) {
        snapshotFlow { state.settledPage }.collect { settled ->
            if (settled == current) return@collect
            if (!off.getOrElse(settled) { false }) {
                select(settled)
                return@collect
            }
            val by = if (settled > current) 1 else -1
            var next = settled + by
            while (next in 0 until count && off[next]) next += by
            state.animateScrollToPage(if (next in 0 until count) next else current)
        }
    }
    HorizontalPager(
        state = state,
        modifier = modifier,
        beyondViewportPageCount = (count - 1).coerceAtLeast(0),
        verticalAlignment = Alignment.Top,
    ) { p -> page(p) }
}
