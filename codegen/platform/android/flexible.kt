package PACKAGE

import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.Measurable
import androidx.compose.ui.layout.MeasureResult
import androidx.compose.ui.layout.MeasureScope
import androidx.compose.ui.node.LayoutModifierNode
import androidx.compose.ui.node.ModifierNodeElement
import androidx.compose.ui.node.TraversableNode
import androidx.compose.ui.node.invalidateMeasurement
import androidx.compose.ui.node.traverseAncestors
import androidx.compose.ui.unit.Constraints

// snglFlexible is a vbox's, hbox's or scroll's Row or Column, which a child
// taking a share of it marks with snglFlexed. Compose measures a weight in an
// unbounded axis -- a Row in a horizontal scroll, a Column in a vertical one --
// to nothing, where html's flex takes the content's size; so a marked line
// with no bound along it is bounded by its content's, and the weights share
// that. A marked line also marks the line it sits in across it, as html's
// stretched row fills its column: a column with no bound across it is then as
// wide as its widest row, which each row fills, so the rows' shares line up as
// a table's columns. Only a marked one asks, since asking costs a pass over
// the subtree.
fun Modifier.snglFlexible(horizontal: Boolean): Modifier = this then FlexibleElement(horizontal)

fun Modifier.snglFlexed(): Modifier = this then FlexedElement

private object FlexibleKey

private data class FlexibleElement(val horizontal: Boolean) : ModifierNodeElement<FlexibleNode>() {
    override fun create() = FlexibleNode(horizontal)
    override fun update(node: FlexibleNode) {
        node.horizontal = horizontal
        node.invalidateMeasurement()
    }
}

private class FlexibleNode(var horizontal: Boolean) : Modifier.Node(), LayoutModifierNode, TraversableNode {
    override val traverseKey: Any get() = FlexibleKey
    private var outer: FlexibleNode? = null
    private var flexed = 0
    private var across = 0

    fun flex(by: Int) {
        val was = flexed > 0
        flexed += by
        if (was != flexed > 0) cross(if (flexed > 0) 1 else -1)
        invalidateMeasurement()
    }

    private fun cross(by: Int) {
        outer?.takeIf { it.horizontal != horizontal }?.let {
            it.across += by
            it.invalidateMeasurement()
        }
    }

    // Compose runs a parent's onAttach after its children's, so a line may
    // already be marked when it finds the line it sits in.
    override fun onAttach() {
        traverseAncestors(FlexibleKey) {
            outer = it as FlexibleNode
            false
        }
        if (flexed > 0) cross(1)
    }

    override fun onDetach() {
        if (flexed > 0) cross(-1)
        outer = null
    }

    override fun MeasureScope.measure(measurable: Measurable, constraints: Constraints): MeasureResult {
        val placeable = measurable.measure(bounded(measurable, constraints))
        return layout(placeable.width, placeable.height) { placeable.place(0, 0) }
    }

    private fun bounded(measurable: Measurable, c: Constraints): Constraints {
        var out = c
        // A SubcomposeLayout -- a pager, a lazy list -- refuses to be asked
        // its intrinsic size; the line then measures as Compose would.
        try {
            if ((flexed > 0 && horizontal || across > 0 && !horizontal) && !out.hasBoundedWidth) {
                out = out.copy(maxWidth = maxOf(out.minWidth, measurable.maxIntrinsicWidth(out.maxHeight)))
            }
            if ((flexed > 0 && !horizontal || across > 0 && horizontal) && !out.hasBoundedHeight) {
                out = out.copy(maxHeight = maxOf(out.minHeight, measurable.maxIntrinsicHeight(out.maxWidth)))
            }
        } catch (_: IllegalStateException) {
            return c
        }
        return out
    }
}

private object FlexedElement : ModifierNodeElement<FlexedNode>() {
    override fun create() = FlexedNode()
    override fun update(node: FlexedNode) {}
    override fun equals(other: Any?) = other === this
    override fun hashCode() = 0
}

private class FlexedNode : Modifier.Node() {
    private var line: FlexibleNode? = null

    override fun onAttach() {
        traverseAncestors(FlexibleKey) {
            line = it as FlexibleNode
            false
        }
        line?.flex(1)
    }

    override fun onDetach() {
        line?.flex(-1)
        line = null
    }
}
