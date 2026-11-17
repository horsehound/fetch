package com.horsehound.fetch

import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
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
        setContent {
            FetchTheme {
                // A phone that isn't connected yet starts in Settings.
                var showSettings by rememberSaveable { mutableStateOf(!model.isSetUp) }
                // Kept here so the feed is where you left it after a visit to Settings.
                val feedPosition = rememberLazyListState()
                val draft = rememberSaveable { mutableStateOf("") }

                if (showSettings) {
                    BackHandler { showSettings = false }
                    SettingsScreen(model, onClose = { showSettings = false })
                } else {
                    FeedScreen(model, feedPosition, draft, onSettings = { showSettings = true })
                }
            }
        }
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
