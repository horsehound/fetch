package com.horsehound.fetch.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/**
 * Graphite Black, Cyber Lime, Electric Indigo, Neon Fuchsia. One dark theme,
 * the same one the iPhone app and the hub's web page use.
 */
object Theme {
    val graphite = Color(0xFF1C1C1C)
    val card = Color(0xFF242424)
    val inset = Color(0xFF151515)
    val line = Color(0xFF393939)
    val text = Color(0xFFF5F5F2)
    val muted = Color(0xFF9A9A94)

    val lime = Color(0xFFCFFF04)
    val indigo = Color(0xFF5D00FF)
    val fuchsia = Color(0xFFFF2E9A)

    // Tints that stay readable as text on graphite.
    val limeSoft = Color(0xFFE6FF7A)
    val indigoSoft = Color(0xFFB79CFF)
    val fuchsiaSoft = Color(0xFFFF8CC8)

    /** Colour for one highlight class sent by the hub (see hub/highlight.go). */
    fun code(kind: String): Color = when (kind) {
        "kw", "del" -> fuchsia
        "str" -> limeSoft
        "fn", "key", "add" -> lime
        "num", "typ", "var" -> indigoSoft
        "attr" -> fuchsiaSoft
        "com", "meta" -> Color(0xFF7D7D77)
        else -> text
    }
}

// Material's roles filled from the palette, so stock components (menus,
// dialogs, text fields) come out in the same colours without being told.
private val Colors = darkColorScheme(
    primary = Theme.lime,
    onPrimary = Theme.graphite,
    primaryContainer = Theme.indigo,
    onPrimaryContainer = Color.White,
    secondary = Theme.indigoSoft,
    onSecondary = Theme.graphite,
    secondaryContainer = Theme.indigo,
    onSecondaryContainer = Color.White,
    tertiary = Theme.fuchsiaSoft,
    onTertiary = Theme.graphite,
    background = Theme.graphite,
    onBackground = Theme.text,
    surface = Theme.graphite,
    onSurface = Theme.text,
    surfaceVariant = Theme.card,
    onSurfaceVariant = Theme.muted,
    surfaceTint = Theme.lime,
    inverseSurface = Theme.text,
    inverseOnSurface = Theme.graphite,
    inversePrimary = Theme.indigo,
    outline = Color(0xFF6B6B66),
    outlineVariant = Theme.line,
    surfaceContainerLowest = Theme.inset,
    surfaceContainerLow = Theme.graphite,
    surfaceContainer = Theme.card,
    surfaceContainerHigh = Color(0xFF2B2B2B),
    surfaceContainerHighest = Color(0xFF333333),
    error = Theme.fuchsia,
    onError = Color.White,
)

@Composable
fun FetchTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = Colors, content = content)
}
