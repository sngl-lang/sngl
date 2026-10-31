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
// that. Only a marked one asks, since asking costs a pass over the subtree.
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
    var flexed = 0

    override fun MeasureScope.measure(measurable: Measurable, constraints: Constraints): MeasureResult {
        val placeable = measurable.measure(bounded(measurable, constraints))
        return layout(placeable.width, placeable.height) { placeable.place(0, 0) }
    }

    private fun bounded(measurable: Measurable, c: Constraints): Constraints {
        if (flexed == 0) return c
        // A SubcomposeLayout -- a pager, a lazy list -- refuses to be asked
        // its intrinsic size; the line then measures as Compose would.
        return try {
            when {
                horizontal && !c.hasBoundedWidth -> c.copy(maxWidth = maxOf(c.minWidth, measurable.maxIntrinsicWidth(c.maxHeight)))
                !horizontal && !c.hasBoundedHeight -> c.copy(maxHeight = maxOf(c.minHeight, measurable.maxIntrinsicHeight(c.maxWidth)))
                else -> c
            }
        } catch (_: IllegalStateException) {
            c
        }
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
        line?.let {
            it.flexed++
            it.invalidateMeasurement()
        }
    }

    override fun onDetach() {
        line?.let {
            it.flexed--
            it.invalidateMeasurement()
        }
        line = null
    }
}
