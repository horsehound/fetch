package com.horsehound.fetch.ui

import android.content.ActivityNotFoundException
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Inbox
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material.icons.outlined.WifiTethering
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.horsehound.fetch.FeedModel
import com.horsehound.fetch.Item
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.receiveAsFlow

@Composable
fun FeedScreen(
    model: FeedModel,
    feedPosition: LazyListState,
    draft: MutableState<String>,
    onSettings: () -> Unit,
) {
    val context = LocalContext.current
    var confirmClear by remember { mutableStateOf(false) }

    LaunchedEffect(model) {
        model.launches.receiveAsFlow().collect { intent ->
            try {
                context.startActivity(intent)
            } catch (e: ActivityNotFoundException) {
                // No app on this phone can take it; nothing to open.
            }
        }
    }

    Scaffold(
        topBar = {
            TopBar(
                status = { if (model.isSetUp) StatusPill(model.connection, model.sending, Modifier.padding(start = 12.dp)) },
                onSettings = onSettings,
                onClear = { confirmClear = true },
            )
        },
        bottomBar = { Composer(model, draft) },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            if (model.isSetUp) {
                Feed(model, feedPosition)
            } else {
                Notice(
                    icon = Icons.Outlined.WifiTethering,
                    title = "Pair with your hub",
                    text = model.problem ?: ("Scan the pairing code from a device that is already set up, " +
                        "or set up a new hub. Everything you send is encrypted on this phone."),
                ) {
                    OutlinedButton(onClick = onSettings) { Text("Open Settings") }
                }
            }
            Toast(model, Modifier.align(Alignment.TopCenter))
        }
    }

    if (confirmClear) {
        AlertDialog(
            onDismissRequest = { confirmClear = false },
            text = { Text("Delete everything that isn't pinned, on every device?") },
            confirmButton = {
                TextButton(onClick = {
                    confirmClear = false
                    model.clearUnpinned()
                }) {
                    Text("Delete unpinned items", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmClear = false }) { Text("Cancel") }
            },
        )
    }
}

// ---- feed ----

@Composable
private fun Feed(model: FeedModel, feedPosition: LazyListState) {
    // Keeps "5 min. ago" honest while the screen stays open.
    val now by produceState(System.currentTimeMillis()) {
        while (true) {
            delay(30_000)
            value = System.currentTimeMillis()
        }
    }
    PullToRefreshBox(
        isRefreshing = model.refreshing,
        onRefresh = { model.pullToRefresh() },
        modifier = Modifier.fillMaxSize(),
    ) {
        LazyColumn(
            state = feedPosition,
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            model.problem?.let { problem ->
                item(key = "problem") { Problem(problem) }
            }
            items(model.ordered, key = { it.id }) { item ->
                SwipeRow(model, item, now, Modifier.animateItem())
            }
        }
        if (model.items.isEmpty() && model.problem == null) {
            Notice(
                icon = Icons.Outlined.Inbox,
                title = "Nothing here yet",
                text = "Send something from any device and it shows up on all of them.",
            )
        }
    }
}

/** Swipe left to delete, right to pin. */
@Composable
private fun SwipeRow(model: FeedModel, item: Item, now: Long, modifier: Modifier) {
    val current by rememberUpdatedState(item)
    val state = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            when (value) {
                SwipeToDismissBoxValue.EndToStart -> {
                    model.delete(current)
                    true
                }
                // Pinning keeps the row; it slides back and moves to the top.
                SwipeToDismissBoxValue.StartToEnd -> {
                    model.togglePin(current)
                    false
                }
                SwipeToDismissBoxValue.Settled -> true
            }
        },
        positionalThreshold = { distance -> distance * 0.35f },
    )
    SwipeToDismissBox(
        state = state,
        modifier = modifier,
        backgroundContent = {
            val scheme = MaterialTheme.colorScheme
            when (state.dismissDirection) {
                SwipeToDismissBoxValue.EndToStart ->
                    SwipeHint(Icons.Outlined.Delete, scheme.error, scheme.onError, Alignment.CenterEnd)
                SwipeToDismissBoxValue.StartToEnd ->
                    SwipeHint(Icons.Outlined.PushPin, Theme.line, Theme.text, Alignment.CenterStart)
                SwipeToDismissBoxValue.Settled -> {}
            }
        },
    ) {
        ItemRow(model, item, now)
    }
}

@Composable
private fun SwipeHint(
    icon: ImageVector,
    color: Color,
    tint: Color,
    alignment: Alignment,
) {
    Box(
        Modifier
            .fillMaxSize()
            .clip(RoundedCornerShape(12.dp))
            .background(color)
            .padding(horizontal = 22.dp),
        contentAlignment = alignment,
    ) {
        Icon(icon, contentDescription = null, tint = tint)
    }
}

@Composable
private fun Problem(text: String) {
    val color = Theme.fuchsia
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Outlined.Warning, contentDescription = null, tint = color, modifier = Modifier.size(16.dp))
        Spacer(Modifier.width(8.dp))
        Text(text, style = MaterialTheme.typography.bodySmall, color = color)
    }
}

@Composable
private fun Notice(
    icon: ImageVector,
    title: String,
    text: String,
    action: @Composable () -> Unit = {},
) {
    Column(
        Modifier.fillMaxSize().padding(horizontal = 40.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            icon,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(44.dp),
        )
        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        Text(
            text,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(4.dp))
        action()
    }
}

// ---- bars ----

@Composable
private fun TopBar(status: @Composable () -> Unit, onSettings: () -> Unit, onClear: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    CenterAlignedTopAppBar(
        title = { Text("Fetch", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold) },
        navigationIcon = status,
        actions = {
            Box {
                IconButton(onClick = { menu = true }) {
                    Icon(Icons.Filled.MoreVert, contentDescription = "More")
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(
                        text = { Text("Settings") },
                        leadingIcon = { Icon(Icons.Outlined.Settings, contentDescription = null) },
                        onClick = {
                            menu = false
                            onSettings()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text("Clear unpinned") },
                        leadingIcon = { Icon(Icons.Outlined.Delete, contentDescription = null) },
                        onClick = {
                            menu = false
                            onClear()
                        },
                    )
                }
            }
        },
    )
}

@Composable
private fun Composer(model: FeedModel, draft: MutableState<String>) {
    val scheme = MaterialTheme.colorScheme
    val keyboard = LocalSoftwareKeyboardController.current
    // The system photo picker: no permission needed, the app only sees what was picked.
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(20)) { uris ->
        model.send(uris)
    }

    Surface(color = scheme.surface) {
        Column {
            HorizontalDivider(color = scheme.outlineVariant)
            Column(
                Modifier
                    .windowInsetsPadding(
                        WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal + WindowInsetsSides.Bottom),
                    )
                    .padding(start = 16.dp, end = 16.dp, top = 10.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Button(
                        onClick = { model.paste() },
                        contentPadding = PaddingValues(horizontal = 14.dp),
                        colors = ButtonDefaults.buttonColors(containerColor = Theme.indigo, contentColor = Color.White),
                        modifier = Modifier.height(36.dp),
                    ) {
                        Icon(Icons.Outlined.ContentPaste, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("Paste")
                    }
                    OutlinedButton(
                        onClick = {
                            photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                        },
                        contentPadding = PaddingValues(horizontal = 14.dp),
                        colors = ButtonDefaults.outlinedButtonColors(containerColor = Theme.card, contentColor = Theme.text),
                        border = BorderStroke(1.dp, Theme.line),
                        modifier = Modifier.height(36.dp),
                    ) {
                        Icon(Icons.Outlined.PhotoLibrary, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("Photos")
                    }
                }
                Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    BasicTextField(
                        value = draft.value,
                        onValueChange = { draft.value = it },
                        modifier = Modifier.weight(1f),
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = scheme.onSurface),
                        cursorBrush = SolidColor(scheme.primary),
                        maxLines = 5,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        decorationBox = { field ->
                            Box(
                                Modifier
                                    .fillMaxWidth()
                                    .heightIn(min = 40.dp)
                                    .clip(RoundedCornerShape(20.dp))
                                    .background(Theme.card)
                                    .border(1.dp, Theme.line, RoundedCornerShape(20.dp))
                                    .padding(horizontal = 14.dp, vertical = 9.dp),
                                contentAlignment = Alignment.CenterStart,
                            ) {
                                if (draft.value.isEmpty()) {
                                    Text(
                                        "Send text",
                                        style = MaterialTheme.typography.bodyLarge,
                                        color = scheme.onSurfaceVariant,
                                    )
                                }
                                field()
                            }
                        },
                    )
                    FilledIconButton(
                        onClick = {
                            val text = draft.value
                            draft.value = ""
                            keyboard?.hide()
                            model.send(text)
                        },
                        enabled = draft.value.isNotBlank(),
                        modifier = Modifier.size(40.dp),
                    ) {
                        Icon(Icons.Filled.ArrowUpward, contentDescription = "Send")
                    }
                }
            }
        }
    }
}

/** A short confirmation that fades by itself. */
@Composable
private fun Toast(model: FeedModel, modifier: Modifier) {
    AnimatedVisibility(
        visible = model.toastVisible,
        enter = fadeIn(),
        exit = fadeOut(),
        modifier = modifier.padding(top = 8.dp),
    ) {
        Surface(
            shape = CircleShape,
            color = Theme.indigo,
            contentColor = Color.White,
            shadowElevation = 2.dp,
        ) {
            Text(
                model.toast,
                style = MaterialTheme.typography.labelLarge,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 9.dp),
            )
        }
    }
}
