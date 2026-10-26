package PACKAGE

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp

// SnglAppBar is a window's app bar over its content: a Material3 Scaffold
// whose TopAppBar shows the icon and the title, an up arrow running onUp while
// canGoBack holds, and the menu's actions. The content is laid out in what the
// bar leaves, the system bars' insets consumed there.
//
// upTag is the up arrow's test tag, the window's `#id` and `__back`, so a
// test's `c.main.back()` presses it.
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SnglAppBar(
    title: String,
    canGoBack: Boolean,
    onUp: () -> Unit,
    upTag: String,
    icon: (@Composable () -> Unit)?,
    actions: @Composable RowScope.() -> Unit,
    content: @Composable () -> Unit,
) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (icon != null) {
                            icon()
                            Spacer(Modifier.width(12.dp))
                        }
                        Text(title)
                    }
                },
                navigationIcon = {
                    if (canGoBack) {
                        IconButton(onClick = onUp, modifier = if (upTag.isEmpty()) Modifier else Modifier.testTag(upTag)) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                        }
                    }
                },
                actions = actions,
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).consumeWindowInsets(padding)) { content() }
    }
}

// What closes the DropdownMenu an entry is in.
private val LocalSnglMenuClose = compositionLocalOf<() -> Unit> { {} }

// SnglMenuAction is a top-level entry of a window's menu: a text button that
// runs onClick and opens a DropdownMenu of its entries, where it has any.
@Composable
fun SnglMenuAction(
    label: String,
    enabled: Boolean,
    hasEntries: Boolean,
    modifier: Modifier,
    onClick: () -> Unit,
    entries: @Composable () -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    Box {
        TextButton(onClick = { onClick(); if (hasEntries) open = true }, enabled = enabled, modifier = modifier) { Text(label) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            CompositionLocalProvider(LocalSnglMenuClose provides { open = false }) { entries() }
        }
    }
}

// SnglMenuEntry is an entry of a SnglMenuAction's DropdownMenu, a check before
// it where it is checked, which closes the menu as it runs onClick.
@Composable
fun SnglMenuEntry(label: String, enabled: Boolean, checked: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val close = LocalSnglMenuClose.current
    DropdownMenuItem(
        text = { Text(label) },
        onClick = { close(); onClick() },
        enabled = enabled,
        modifier = modifier,
        leadingIcon = if (checked) {
            { Icon(Icons.Filled.Check, contentDescription = "Chosen") }
        } else {
            null
        },
    )
}
