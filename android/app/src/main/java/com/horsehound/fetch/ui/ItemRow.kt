package com.horsehound.fetch.ui

import android.text.format.DateUtils
import android.text.format.Formatter
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.outlined.BrokenImage
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.horsehound.fetch.FeedModel
import com.horsehound.fetch.Item

/**
 * One item in the feed. Tapping text or a picture copies it, tapping a file
 * opens the share sheet, and a long press lists everything else.
 */
@Composable
fun ItemRow(model: FeedModel, item: Item, now: Long) {
    val haptics = LocalHapticFeedback.current
    val shape = RoundedCornerShape(12.dp)
    var expanded by rememberSaveable(item.id) { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    // Only text the hub recognised as code comes with tokens.
    val code = item.tokens?.takeIf { item.isText && it.isNotEmpty() }

    Box {
        Column(
            Modifier
                .fillMaxWidth()
                .clip(shape)
                .background(Theme.card)
                .border(1.dp, if (item.pinned) Theme.lime else Theme.line, shape)
                .combinedClickable(
                    onClickLabel = when {
                        code != null -> "Copy this code"
                        item.isText -> "Copy this text"
                        item.isImage -> "Copy this picture"
                        else -> "Share this file"
                    },
                    onLongClickLabel = "More",
                    onLongClick = {
                        haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                        menu = true
                    },
                    onClick = { if (item.isText || item.isImage) model.copy(item) else model.share(item) },
                ),
        ) {
            when {
                code != null -> CodeBlock(item, code, expanded)
                item.isText -> Text(
                    item.text.orEmpty(),
                    maxLines = if (expanded) Int.MAX_VALUE else 8,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.fillMaxWidth().padding(start = 14.dp, end = 14.dp, top = 12.dp),
                )
                item.isImage -> HubImage(model, item)
                else -> FileSummary(item)
            }
            Footer(model, item, now, expanded, onExpand = { expanded = !expanded })
        }

        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            val close = { menu = false }
            MenuEntry("Copy", Icons.Outlined.ContentCopy, close) { model.copy(item) }
            if (!item.isText) {
                val label = if (item.isImage) "Save to Photos" else "Save to Downloads"
                MenuEntry(label, Icons.Outlined.Download, close) { model.save(item) }
            }
            MenuEntry("Share", Icons.Outlined.Share, close) { model.share(item) }
            MenuEntry(if (item.pinned) "Unpin" else "Pin", Icons.Outlined.PushPin, close) { model.togglePin(item) }
            MenuEntry("Delete", Icons.Outlined.Delete, close) { model.delete(item) }
        }
    }
}

@Composable
private fun MenuEntry(label: String, icon: ImageVector, close: () -> Unit, action: () -> Unit) {
    DropdownMenuItem(
        text = { Text(label) },
        leadingIcon = { Icon(icon, contentDescription = null) },
        onClick = {
            close()
            action()
        },
    )
}

// ---- content ----

@Composable
private fun FileSummary(item: Item) {
    val context = LocalContext.current
    val scheme = MaterialTheme.colorScheme
    Row(
        Modifier.fillMaxWidth().padding(start = 14.dp, end = 14.dp, top = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            Icons.AutoMirrored.Outlined.InsertDriveFile,
            contentDescription = null,
            tint = Theme.indigoSoft,
            modifier = Modifier.size(28.dp),
        )
        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(item.name ?: "File", maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                Formatter.formatShortFileSize(context, item.size),
                style = MaterialTheme.typography.labelMedium,
                fontFamily = FontFamily.Monospace,
                color = scheme.onSurfaceVariant,
            )
        }
    }
}

/** Darker inset, monospace, language chip: unmistakably not plain text. */
@Composable
private fun CodeBlock(item: Item, tokens: List<Pair<String, String>>, expanded: Boolean) {
    // The hub's highlight classes applied as colours; diff lines also get a tinted background.
    val code = remember(tokens) {
        buildAnnotatedString {
            for ((kind, text) in tokens) {
                val background = when (kind) {
                    "add" -> Theme.lime.copy(alpha = 0.1f)
                    "del" -> Theme.fuchsia.copy(alpha = 0.12f)
                    else -> Color.Unspecified
                }
                withStyle(SpanStyle(color = Theme.code(kind), background = background)) { append(text) }
            }
        }
    }
    Column(
        Modifier.fillMaxWidth().background(Theme.inset).padding(horizontal = 14.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            (item.langLabel ?: item.lang ?: "Code").uppercase(),
            fontSize = 10.sp,
            fontWeight = FontWeight.SemiBold,
            fontFamily = FontFamily.Monospace,
            color = Color.White,
            modifier = Modifier
                .clip(RoundedCornerShape(5.dp))
                .background(Theme.indigo)
                .padding(horizontal = 7.dp, vertical = 3.dp),
        )
        Text(
            code,
            fontFamily = FontFamily.Monospace,
            fontSize = 13.sp,
            lineHeight = 18.sp,
            maxLines = if (expanded) Int.MAX_VALUE else 8,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** A picture from the hub, loaded with the access token and shown downsampled. */
@Composable
private fun HubImage(model: FeedModel, item: Item) {
    val scheme = MaterialTheme.colorScheme
    var image by remember(item.id) { mutableStateOf<ImageBitmap?>(null) }
    var failed by remember(item.id) { mutableStateOf(false) }

    LaunchedEffect(item.id) {
        val loaded = model.thumbnail(item)
        image = loaded?.asImageBitmap()
        failed = loaded == null
    }

    Box(
        Modifier.fillMaxWidth().background(Theme.inset),
        contentAlignment = Alignment.Center,
    ) {
        val shown = image
        if (shown != null) {
            Image(
                bitmap = shown,
                contentDescription = item.name ?: "Picture",
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .heightIn(max = 380.dp)
                    .aspectRatio(shown.width.toFloat() / shown.height.toFloat()),
            )
        } else {
            Box(Modifier.fillMaxWidth().height(180.dp), contentAlignment = Alignment.Center) {
                if (failed) {
                    Icon(Icons.Outlined.BrokenImage, contentDescription = null, tint = scheme.onSurfaceVariant)
                } else {
                    CircularProgressIndicator(modifier = Modifier.size(24.dp), strokeWidth = 2.dp)
                }
            }
        }
    }
}

// ---- footer ----

@Composable
private fun Footer(model: FeedModel, item: Item, now: Long, expanded: Boolean, onExpand: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val uriHandler = LocalUriHandler.current
    Row(
        Modifier.fillMaxWidth().heightIn(min = 40.dp).padding(start = 14.dp, end = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (item.pinned) {
            Icon(Icons.Filled.PushPin, contentDescription = "Pinned", tint = Theme.lime, modifier = Modifier.size(12.dp))
            Spacer(Modifier.width(4.dp))
        }
        Text(
            caption(item, now),
            style = MaterialTheme.typography.labelMedium,
            fontFamily = FontFamily.Monospace,
            color = scheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (item.isText) {
            if (isLong(item)) {
                if (expanded) {
                    Action(Icons.Filled.ExpandLess, "Show less", onExpand)
                } else {
                    Action(Icons.Filled.ExpandMore, "Show all", onExpand)
                }
            }
            linkIn(item)?.let { link ->
                Action(Icons.AutoMirrored.Outlined.OpenInNew, "Open link") {
                    try {
                        uriHandler.openUri(link)
                    } catch (e: Exception) {
                        // No browser to open it with.
                    }
                }
            }
            Action(Icons.Outlined.ContentCopy, "Copy") { model.copy(item) }
        } else {
            Action(Icons.Outlined.Download, if (item.isImage) "Save to Photos" else "Save to Downloads") {
                model.save(item)
            }
            Action(Icons.Outlined.Share, "Share") { model.share(item) }
        }
    }
}

@Composable
private fun Action(icon: ImageVector, label: String, onClick: () -> Unit) {
    IconButton(onClick = onClick, modifier = Modifier.size(40.dp)) {
        Icon(
            icon,
            contentDescription = label,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(18.dp),
        )
    }
}

// ---- helpers ----

/** Where the item came from and how long ago. */
private fun caption(item: Item, now: Long): String {
    val from = item.device ?: "unknown"
    val elapsed = now - item.created
    val since = if (elapsed < DateUtils.MINUTE_IN_MILLIS) {
        "now"
    } else {
        DateUtils.getRelativeTimeSpanString(
            item.created,
            now,
            DateUtils.MINUTE_IN_MILLIS,
            DateUtils.FORMAT_ABBREV_RELATIVE,
        ).toString()
    }
    return "$from · $since"
}

private fun isLong(item: Item): Boolean {
    val text = item.text.orEmpty()
    return text.length > 400 || text.count { it == '\n' } >= 8
}

/** Set when the whole item is a single web link. */
private fun linkIn(item: Item): String? {
    val text = item.text.orEmpty().trim()
    val isLink = (text.startsWith("http://") || text.startsWith("https://")) && text.none { it.isWhitespace() }
    return if (isLink) text else null
}
