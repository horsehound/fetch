package com.horsehound.fetch.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.horsehound.fetch.FeedModel
import com.horsehound.fetch.HubConfig
import kotlinx.coroutines.launch

@Composable
fun SettingsScreen(model: FeedModel, onClose: () -> Unit) {
    var server by rememberSaveable { mutableStateOf(model.config.server) }
    var token by rememberSaveable { mutableStateOf(model.config.token) }
    var device by rememberSaveable { mutableStateOf(model.config.device) }
    var checking by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val scheme = MaterialTheme.colorScheme

    val draft = HubConfig(server, token, device.ifBlank { HubConfig.DEFAULT_DEVICE })

    fun keep() {
        model.apply(draft)
        onClose()
    }

    /** Checks the hub answers with this token before keeping the settings. */
    fun save() {
        scope.launch {
            checking = true
            failure = null
            val problem = model.check(draft)
            checking = false
            if (problem == null) keep() else failure = problem
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onClose) {
                        Icon(Icons.Filled.Close, contentDescription = "Cancel")
                    }
                },
                actions = {
                    if (checking) {
                        CircularProgressIndicator(modifier = Modifier.padding(end = 16.dp).size(22.dp), strokeWidth = 2.dp)
                    } else {
                        TextButton(onClick = { save() }, enabled = draft.isComplete) { Text("Save") }
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .consumeWindowInsets(padding)
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Heading("Hub")
            OutlinedTextField(
                value = server,
                onValueChange = { server = it },
                label = { Text("Address") },
                placeholder = { Text("192.168.1.20:8787 or fetch.example.com") },
                singleLine = true,
                keyboardOptions = plainKeyboard(KeyboardType.Uri, ImeAction.Next),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = token,
                onValueChange = { token = it },
                label = { Text("Token") },
                singleLine = true,
                textStyle = LocalTextStyle.current.copy(fontFamily = FontFamily.Monospace),
                keyboardOptions = plainKeyboard(KeyboardType.Ascii, ImeAction.Next),
                modifier = Modifier.fillMaxWidth(),
            )
            Note(
                "The hub prints its address and token when it starts. A hub on your home network is " +
                    "reachable on the same Wi-Fi; one on a server with its own domain works from anywhere.",
            )

            Spacer(Modifier.height(10.dp))
            Heading("This device")
            OutlinedTextField(
                value = device,
                onValueChange = { device = it },
                label = { Text("Name") },
                placeholder = { Text(HubConfig.DEFAULT_DEVICE) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )
            Note("Shown next to everything you send from here.")

            failure?.let { message ->
                Spacer(Modifier.height(10.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(
                        Icons.Outlined.Warning,
                        contentDescription = null,
                        tint = scheme.error,
                        modifier = Modifier.size(18.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(message, color = scheme.error, style = MaterialTheme.typography.bodyMedium)
                }
                OutlinedButton(onClick = { keep() }) { Text("Save anyway") }
            }
        }
    }
}

@Composable
private fun Heading(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun Note(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

/** A keyboard for addresses and tokens: nothing capitalised, nothing "corrected". */
fun plainKeyboard(type: KeyboardType, action: ImeAction) = KeyboardOptions(
    capitalization = KeyboardCapitalization.None,
    autoCorrectEnabled = false,
    keyboardType = type,
    imeAction = action,
)
