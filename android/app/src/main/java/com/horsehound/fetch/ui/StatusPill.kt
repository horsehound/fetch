package com.horsehound.fetch.ui

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.horsehound.fetch.Connection

private enum class Look { Connecting, Connected, Sending, Offline }

/**
 * Whether the hub is reachable and what's on its way there: a pill with a
 * tennis ball. Lime ball when connected; indigo, with the ball spinning,
 * while sending; fuchsia with a grey ball when the hub can't be reached.
 */
@Composable
fun StatusPill(connection: Connection, sending: Int, modifier: Modifier = Modifier) {
    val look = when {
        sending > 0 -> Look.Sending
        connection == Connection.Connected -> Look.Connected
        connection == Connection.Offline -> Look.Offline
        else -> Look.Connecting
    }
    val label = when (look) {
        Look.Sending -> if (sending > 1) "Sending $sending" else "Sending"
        Look.Connected -> "Connected"
        Look.Offline -> "Offline"
        Look.Connecting -> "Connecting"
    }
    val fill by animateColorAsState(
        when (look) {
            Look.Sending -> Theme.indigo
            Look.Offline -> Theme.fuchsia.copy(alpha = 0.08f)
            else -> Theme.card
        },
        label = "fill",
    )
    val stroke by animateColorAsState(
        when (look) {
            Look.Sending -> Theme.indigo
            Look.Offline -> Theme.fuchsia
            else -> Theme.line
        },
        label = "stroke",
    )
    val textColor = when (look) {
        Look.Connected, Look.Sending -> Color.White
        Look.Connecting -> Theme.muted
        Look.Offline -> Theme.fuchsiaSoft
    }
    val ball = when (look) {
        Look.Connected, Look.Sending -> Theme.lime
        Look.Connecting -> Theme.muted
        Look.Offline -> Color(0xFF5A5A55)
    }
    val turn by rememberInfiniteTransition(label = "spin").animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(tween(800, easing = LinearEasing), RepeatMode.Restart),
        label = "turn",
    )

    Row(
        modifier = modifier
            .clearAndSetSemantics {
                contentDescription = label
                liveRegion = LiveRegionMode.Polite
            }
            .background(fill, CircleShape)
            .border(1.dp, stroke, CircleShape)
            .padding(start = 5.dp, end = 10.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        TennisBall(ball, Modifier.size(15.dp).rotate(if (look == Look.Sending) turn else 0f))
        Text(label, color = textColor, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
    }
}

/** A ball with the two curved seams of a tennis ball. */
@Composable
private fun TennisBall(color: Color, modifier: Modifier) {
    Canvas(modifier) {
        val s = size.minDimension / 16f
        drawCircle(color, radius = 7f * s)
        val seams = Path().apply {
            moveTo(3.2f * s, 3.4f * s)
            cubicTo(5.6f * s, 5f * s, 5.6f * s, 11f * s, 3.2f * s, 12.6f * s)
            moveTo(12.8f * s, 3.4f * s)
            cubicTo(10.4f * s, 5f * s, 10.4f * s, 11f * s, 12.8f * s, 12.6f * s)
        }
        drawPath(seams, Theme.graphite, style = Stroke(width = 1.6f * s, cap = StrokeCap.Round))
    }
}
