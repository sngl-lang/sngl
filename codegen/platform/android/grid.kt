package PACKAGE

import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.Placeable
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.constrainHeight
import androidx.compose.ui.unit.constrainWidth

// SnglGrid is sngl:ui's grid: its children a row at a time, `gap` apart within
// a row and between two. Given `columns` above zero, a row holds that many,
// each as wide as an equal share of the grid's width less the gaps, or as the
// widest child where the width is unbounded. Given none, each child is its
// own width, no wider than the grid, and a row ends where the next would not
// fit, as CSS's wrapping row does. A row is as tall as its tallest child,
// which `verticalAlignment` places the others in. FlowRow says the flow, but
// its equal columns are weights a short last row stretches across.
@Composable
fun SnglGrid(
    modifier: Modifier,
    columns: Int,
    gap: Dp,
    verticalAlignment: Alignment.Vertical = Alignment.Top,
    content: @Composable () -> Unit,
) {
    Layout(content = content, modifier = modifier) { measurables, constraints ->
        val space = gap.roundToPx()
        val loose = constraints.copy(minWidth = 0, minHeight = 0)
        val bounded = constraints.hasBoundedWidth
        val placeables: List<Placeable>
        val rows = mutableListOf<Int>()
        if (columns > 0) {
            val column = if (bounded) {
                maxOf((constraints.maxWidth - space * (columns - 1)) / columns, 0)
            } else {
                measurables.maxOfOrNull { it.maxIntrinsicWidth(Constraints.Infinity) } ?: 0
            }
            placeables = measurables.map { it.measure(loose.copy(minWidth = column, maxWidth = column)) }
            for (i in placeables.indices) {
                if ((i + 1) % columns == 0 || i == placeables.size - 1) rows.add(i + 1)
            }
        } else {
            placeables = measurables.map { it.measure(loose) }
            var run = 0
            for ((i, p) in placeables.withIndex()) {
                if (run > 0 && bounded && run + space + p.width > constraints.maxWidth) {
                    rows.add(i)
                    run = 0
                }
                run += (if (run > 0) space else 0) + p.width
            }
            if (placeables.isNotEmpty()) rows.add(placeables.size)
        }
        val heights = IntArray(rows.size)
        var widest = 0
        var start = 0
        for ((r, end) in rows.withIndex()) {
            var run = 0
            for (i in start until end) {
                heights[r] = maxOf(heights[r], placeables[i].height)
                run += (if (i > start) space else 0) + placeables[i].width
            }
            widest = maxOf(widest, run)
            start = end
        }
        val tall = heights.sum() + space * maxOf(rows.size - 1, 0)
        val width = constraints.constrainWidth(if (columns > 0 && bounded) constraints.maxWidth else widest)
        layout(width, constraints.constrainHeight(tall)) {
            var y = 0
            var first = 0
            for ((r, end) in rows.withIndex()) {
                var x = 0
                for (i in first until end) {
                    val p = placeables[i]
                    val at = verticalAlignment.align(p.height, heights[r])
                    p.place(if (layoutDirection == LayoutDirection.Rtl) width - x - p.width else x, y + at)
                    x += p.width + space
                }
                y += heights[r] + space
                first = end
            }
        }
    }
}
