package com.horsehound.fetch

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.graphics.Color
import android.os.Bundle
import android.view.HapticFeedbackConstants
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.IntentCompat
import androidx.lifecycle.lifecycleScope
import com.horsehound.fetch.ui.FetchTheme
import com.horsehound.fetch.ui.Theme
import com.horsehound.fetch.ui.plainKeyboard
import kotlin.coroutines.cancellation.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * "Fetch" in the share sheet: sends whatever was shared straight to the hub
 * and closes itself.
 */
class ShareActivity : ComponentActivity() {
    private enum class Phase { Sending, Done, Failed, Setup }

    private var phase by mutableStateOf(Phase.Sending)
    private var message by mutableStateOf("Sending")

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState != null) {
            // Brought back after Android cleared the app away; whatever was
            // being sent has either arrived or is no longer readable.
            finish()
            return
        }
        // The app is dark everywhere, so the system bars always get light icons.
        val bars = SystemBarStyle.dark(Color.TRANSPARENT)
        enableEdgeToEdge(statusBarStyle = bars, navigationBarStyle = bars)
        setContent {
            FetchTheme {
                Box(Modifier.fillMaxSize().safeDrawingPadding().padding(32.dp), contentAlignment = Alignment.Center) {
                    Surface(
                        shape = RoundedCornerShape(18.dp),
                        color = Theme.card,
                        modifier = Modifier.widthIn(min = 180.dp, max = 360.dp),
                    ) {
                        if (phase == Phase.Setup) HubForm() else Progress()
                    }
                }
            }
        }
        begin()
    }

    // ---- flow ----

    private fun begin() {
        val client = HubClient.from(HubConfig.load(this))
        if (client == null) {
            phase = Phase.Setup
            return
        }
        phase = Phase.Sending
        message = "Sending"
        lifecycleScope.launch {
            try {
                val count = sendAll(client)
                done(if (count == 0) "Nothing to send" else if (count == 1) "Sent" else "Sent $count items")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e.message ?: "Something went wrong.")
            }
        }
    }

    private suspend fun sendAll(client: HubClient): Int {
        // Never send under a key the other devices no longer have.
        client.verify()
        val streams = sharedStreams()
        if (streams.isEmpty()) {
            // A link or a piece of text: browsers share the page address this way.
            val text = intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()
            if (text.isNullOrBlank()) return 0
            client.sendText(text)
            return 1
        }
        streams.forEachIndexed { index, uri ->
            message = if (streams.size > 1) "Sending ${index + 1} of ${streams.size}" else "Sending"
            Outgoing.send(this, client, uri)
        }
        return streams.size
    }

    /** Pictures, videos and files arrive as addresses to read them from. */
    private fun sharedStreams(): List<Uri> = when (intent.action) {
        Intent.ACTION_SEND ->
            listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
        Intent.ACTION_SEND_MULTIPLE ->
            IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        else -> emptyList()
    }

    private fun done(result: String) {
        phase = Phase.Done
        message = result
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.decorView.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
        }
        lifecycleScope.launch {
            delay(600) // long enough to read the result
            finish()
        }
    }

    private fun fail(reason: String) {
        phase = Phase.Failed
        message = reason
    }

    // ---- what is on screen ----

    @Composable
    private fun Progress() {
        Column(
            Modifier.padding(start = 24.dp, end = 24.dp, top = 24.dp, bottom = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            if (phase == Phase.Sending) CircularProgressIndicator(modifier = Modifier.size(24.dp), strokeWidth = 2.dp)
            Text(message, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
            if (phase == Phase.Failed) TextButton(onClick = { finish() }) { Text("Close") }
        }
    }

    /**
     * Normally the pairing happens in the Fetch app. If something is shared
     * before the phone was ever paired, take a pairing link here.
     */
    @Composable
    private fun HubForm() {
        var link by rememberSaveable { mutableStateOf("") }

        Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Pair with your hub", style = MaterialTheme.typography.titleMedium)
            Text(
                "Paste a pairing link from a device that is already set up (Settings, Add a device). " +
                    "Or open Fetch to scan its QR code.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedTextField(
                value = link,
                onValueChange = { link = it },
                placeholder = { Text("fetchpair://v1?…") },
                singleLine = true,
                keyboardOptions = plainKeyboard(KeyboardType.Uri, ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                TextButton(onClick = { finish() }) { Text("Cancel") }
                TextButton(onClick = { pair(link) }) { Text("Send") }
            }
        }
    }

    private fun pair(link: String) {
        val pairing = Seal.Pairing.parseOrNull(link)
        if (pairing == null) {
            fail("That isn't a complete pairing link.")
            return
        }
        phase = Phase.Sending
        message = "Pairing"
        lifecycleScope.launch {
            val config = HubConfig.load(this@ShareActivity)
            val client = HubClient.to(pairing.hub, pairing.token, pairing.key, config.device)
            try {
                val check = client!!.hubCheck()
                if (check.isEmpty()) {
                    client.claim()
                } else if (check != pairing.key.check) {
                    fail("This link's key isn't the one the hub uses now. Make a fresh link.")
                    return@launch
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e.message ?: "Something went wrong.")
                return@launch
            }
            config.copy(server = pairing.hub, token = pairing.token, key = pairing.key.text).save(this@ShareActivity)
            begin()
        }
    }
}
