package com.horsehound.fetch

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import kotlinx.coroutines.launch
import com.horsehound.fetch.ui.FeedScreen
import com.horsehound.fetch.ui.FetchTheme
import com.horsehound.fetch.ui.SettingsScreen

class MainActivity : ComponentActivity() {
    private val model: FeedModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The app is dark everywhere, so the system bars always get light icons.
        val bars = SystemBarStyle.dark(Color.TRANSPARENT)
        enableEdgeToEdge(statusBarStyle = bars, navigationBarStyle = bars)
        if (savedInstanceState == null) receive(intent)
        setContent {
            FetchTheme {
                // A phone that isn't connected yet starts in Settings.
                var showSettings by rememberSaveable { mutableStateOf(!model.isSetUp) }
                // Kept here so the feed is where you left it after a visit to Settings.
                val feedPosition = rememberLazyListState()
                val draft = rememberSaveable { mutableStateOf("") }
                // Outlives the pairing dialog, which closes as soon as pairing starts.
                val scope = rememberCoroutineScope()

                if (showSettings) {
                    BackHandler { showSettings = false }
                    SettingsScreen(model, onClose = { showSettings = false })
                } else {
                    FeedScreen(model, feedPosition, draft, onSettings = { showSettings = true })
                }

                // A pairing link opened from outside: a scanned QR code, a tapped link.
                model.pendingPairing?.let { pairing ->
                    AlertDialog(
                        onDismissRequest = { model.dismissPairing() },
                        title = { Text("Pair with this hub?") },
                        text = {
                            val instead = if (model.isSetUp) " This phone will stop using the hub it's paired with now." else ""
                            Text("${pairing.hub}\nKey ${pairing.key.fingerprint}.$instead")
                        },
                        confirmButton = {
                            TextButton(onClick = {
                                scope.launch {
                                    model.pair(pairing)?.let { model.report(it) }
                                    showSettings = false
                                }
                            }) { Text("Pair") }
                        },
                        dismissButton = { TextButton(onClick = { model.dismissPairing() }) { Text("Cancel") } },
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        receive(intent)
    }

    private fun receive(intent: Intent?) {
        val link = intent?.takeIf { it.action == Intent.ACTION_VIEW }?.dataString ?: return
        if (link.startsWith("fetchpair:", ignoreCase = true)) model.received(link)
    }

    // The feed is live while the app is on screen and quiet otherwise.
    override fun onStart() {
        super.onStart()
        model.start()
    }

    override fun onStop() {
        super.onStop()
        model.stop()
    }
}
