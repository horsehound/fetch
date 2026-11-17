package com.horsehound.fetch.ui

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.PersistableBundle
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.horsehound.fetch.FeedModel
import com.horsehound.fetch.HubConfig
import com.horsehound.fetch.HubError
import com.horsehound.fetch.Seal
import kotlinx.coroutines.launch

@Composable
fun SettingsScreen(model: FeedModel, onClose: () -> Unit) {
    var device by rememberSaveable { mutableStateOf(model.config.device) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onClose) {
                        Icon(Icons.Filled.Close, contentDescription = "Close")
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
            if (model.isSetUp) Paired(model, onClose) else Unpaired(model, onClose)

            Spacer(Modifier.height(10.dp))
            Heading("This device")
            OutlinedTextField(
                value = device,
                onValueChange = {
                    device = it.take(HubConfig.MAX_DEVICE_NAME)
                    model.rename(device)
                },
                label = { Text("Name") },
                placeholder = { Text(HubConfig.DEFAULT_DEVICE) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )
            Note("Shown next to everything you send from here. It travels encrypted, like the rest.")
        }
    }
}

// ---- not paired yet ----

@Composable
private fun ColumnScope.Unpaired(model: FeedModel, onClose: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var link by rememberSaveable { mutableStateOf("") }
    var server by rememberSaveable { mutableStateOf("") }
    var token by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    var offerStartOver by remember { mutableStateOf(false) }
    var confirmStartOver by remember { mutableStateOf(false) }

    fun pair(text: String) {
        val pairing = Seal.Pairing.parseOrNull(text)
        if (pairing == null) {
            failure = "That isn't a complete pairing link."
            return
        }
        scope.launch {
            busy = true
            failure = model.pair(pairing)
            busy = false
            if (failure == null) onClose()
        }
    }

    fun setUp(startOver: Boolean) {
        scope.launch {
            busy = true
            failure = model.setUp(server, token, startOver)
            busy = false
            offerStartOver = failure == HubError.AlreadySetUp().message
            if (failure == null) onClose()
        }
    }

    Heading("Pair with your hub")
    Note(
        "On a device that is already set up, open Settings and choose Add a device. " +
            "Then scan its code here, or paste the link it gives you.",
    )
    Button(onClick = { scan(context, ::pair) { failure = it } }, enabled = !busy) {
        Icon(Icons.Outlined.QrCodeScanner, contentDescription = null, modifier = Modifier.size(18.dp))
        Spacer(Modifier.width(8.dp))
        Text("Scan code")
    }
    OutlinedTextField(
        value = link,
        onValueChange = { link = it },
        label = { Text("Pairing link") },
        placeholder = { Text("fetchpair://v1?…") },
        singleLine = true,
        keyboardOptions = plainKeyboard(KeyboardType.Uri, ImeAction.Go),
        modifier = Modifier.fillMaxWidth(),
    )
    OutlinedButton(onClick = { pair(link) }, enabled = !busy && link.isNotBlank()) { Text("Pair") }

    Spacer(Modifier.height(6.dp))
    HorizontalDivider()
    Spacer(Modifier.height(6.dp))
    Heading("Setting up a new hub?")
    Note(
        "Enter the address and token the hub printed when it started. This phone makes the key " +
            "every other device will share; the hub never sees it.",
    )
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
        textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace),
        keyboardOptions = plainKeyboard(KeyboardType.Ascii, ImeAction.Done),
        modifier = Modifier.fillMaxWidth(),
    )
    OutlinedButton(
        onClick = { setUp(startOver = false) },
        enabled = !busy && server.isNotBlank() && token.isNotBlank(),
    ) { Text("Set up") }

    if (busy) CircularProgressIndicator(modifier = Modifier.size(22.dp), strokeWidth = 2.dp)
    failure?.let { Problem(it) }
    if (offerStartOver) {
        TextButton(onClick = { confirmStartOver = true }) { Text("Start over on this hub…", color = MaterialTheme.colorScheme.error) }
    }
    if (confirmStartOver) {
        StartOverDialog(
            onConfirm = {
                confirmStartOver = false
                setUp(startOver = true)
            },
            onDismiss = { confirmStartOver = false },
        )
    }
}

/** Google's code scanner: no camera permission, and it works without our own camera code. */
private fun scan(context: Context, found: (String) -> Unit, failed: (String) -> Unit) {
    val options = GmsBarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build()
    GmsBarcodeScanning.getClient(context, options).startScan()
        .addOnSuccessListener { code -> code.rawValue?.let(found) }
        .addOnFailureListener {
            failed("The code scanner isn't available on this phone. Paste the link instead.")
        }
}

// ---- paired ----

@Composable
private fun ColumnScope.Paired(model: FeedModel, onClose: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val config = model.config
    val pairing = config.pairing
    var showLink by rememberSaveable { mutableStateOf(false) }
    var confirmUnpair by remember { mutableStateOf(false) }
    var confirmStartOver by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }

    Heading("Hub")
    Text(pairing?.hub ?: config.server, style = MaterialTheme.typography.bodyLarge)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text("Key ", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(config.sealKey?.fingerprint.orEmpty(), fontFamily = FontFamily.Monospace, color = Theme.lime)
    }
    Note(
        "Everything is encrypted on the device that sends it, and only your paired devices can read it. " +
            "They all show the same key fingerprint.",
    )

    Spacer(Modifier.height(6.dp))
    Heading("Add a device")
    if (!showLink || pairing == null) {
        Button(onClick = { showLink = true }) { Text("Show pairing code") }
    } else {
        Note(
            "Scan this with Fetch on the other device, or copy the link to it. The link opens " +
                "everything you send: don't post it anywhere.",
        )
        QrCode(
            pairing.link,
            Modifier
                .widthIn(max = 280.dp)
                .fillMaxWidth()
                .align(Alignment.CenterHorizontally),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = { copySecret(context, pairing.link) }) {
                Icon(Icons.Outlined.ContentCopy, contentDescription = null, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text("Copy link")
            }
            OutlinedButton(onClick = {
                val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, pairing.link)
                context.startActivity(Intent.createChooser(send, null))
            }) {
                Icon(Icons.Outlined.Share, contentDescription = null, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text("Share")
            }
        }
        TextButton(onClick = { showLink = false }) { Text("Hide") }
    }

    Spacer(Modifier.height(6.dp))
    HorizontalDivider()
    Spacer(Modifier.height(6.dp))
    OutlinedButton(onClick = { confirmUnpair = true }, enabled = !busy) { Text("Unpair this phone") }
    TextButton(onClick = { confirmStartOver = true }, enabled = !busy) {
        Text("Start over…", color = MaterialTheme.colorScheme.error)
    }
    if (busy) CircularProgressIndicator(modifier = Modifier.size(22.dp), strokeWidth = 2.dp)
    failure?.let { Problem(it) }

    if (confirmUnpair) {
        AlertDialog(
            onDismissRequest = { confirmUnpair = false },
            title = { Text("Unpair this phone?") },
            text = { Text("It forgets the hub and the key. Everything on the hub stays; pair again to see it.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmUnpair = false
                    model.unpair()
                }) { Text("Unpair") }
            },
            dismissButton = { TextButton(onClick = { confirmUnpair = false }) { Text("Cancel") } },
        )
    }
    if (confirmStartOver) {
        StartOverDialog(
            onConfirm = {
                confirmStartOver = false
                scope.launch {
                    busy = true
                    failure = model.startOver()
                    busy = false
                    if (failure == null) onClose()
                }
            },
            onDismiss = { confirmStartOver = false },
        )
    }
}

@Composable
private fun StartOverDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Start over?") },
        text = {
            Text(
                "This deletes everything on the hub, pinned items too, and makes a new key. " +
                    "Every other device has to pair again.",
            )
        },
        confirmButton = {
            TextButton(
                onClick = onConfirm,
                colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
            ) { Text("Delete everything") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** A pairing link on the clipboard, marked so Android doesn't show it in its preview. */
private fun copySecret(context: Context, text: String) {
    val clip = ClipData.newPlainText("Fetch pairing link", text)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
    }
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(clip)
}

@Composable
private fun QrCode(text: String, modifier: Modifier = Modifier) {
    val matrix = remember(text) {
        QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, 0, 0, mapOf(EncodeHintType.MARGIN to 0))
    }
    Canvas(
        modifier
            .aspectRatio(1f)
            .clip(RoundedCornerShape(12.dp))
            .background(Color.White)
            .padding(14.dp),
    ) {
        val cell = size.width / matrix.width
        for (y in 0 until matrix.height) {
            for (x in 0 until matrix.width) {
                if (matrix[x, y]) {
                    // A hair larger than a cell, so no seams show between modules.
                    drawRect(Color.Black, Offset(x * cell, y * cell), Size(cell + 0.5f, cell + 0.5f))
                }
            }
        }
    }
}

@Composable
private fun Problem(message: String) {
    val scheme = MaterialTheme.colorScheme
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Outlined.Warning, contentDescription = null, tint = scheme.error, modifier = Modifier.size(18.dp))
        Spacer(Modifier.width(8.dp))
        Text(message, color = scheme.error, style = MaterialTheme.typography.bodyMedium)
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

/** A keyboard for addresses, tokens and links: nothing capitalised, nothing "corrected". */
fun plainKeyboard(type: KeyboardType, action: ImeAction) = KeyboardOptions(
    capitalization = KeyboardCapitalization.None,
    autoCorrectEnabled = false,
    keyboardType = type,
    imeAction = action,
)
