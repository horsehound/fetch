package com.horsehound.fetch

import android.app.Application
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.ImageDecoder
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.LruCache
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import java.io.File
import java.io.IOException
import java.nio.ByteBuffer
import kotlin.coroutines.cancellation.CancellationException
import kotlin.math.roundToInt
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext

enum class Connection { Connecting, Connected, Offline }

/** The feed and everything the screen can do with it. */
class FeedModel(private val app: Application) : AndroidViewModel(app) {
    var items by mutableStateOf(emptyList<Item>())
        private set
    var config by mutableStateOf(HubConfig.load(app))
        private set
    var problem by mutableStateOf<String?>(null)
        private set

    /** Whether the live connection to the hub is up, for the status pill. */
    var connection by mutableStateOf(Connection.Connecting)
        private set

    /** Uploads in flight. */
    var sending by mutableStateOf(0)
        private set
    var refreshing by mutableStateOf(false)
        private set

    /** The last short confirmation ("Copied"), and whether it is on screen right now. */
    var toast by mutableStateOf("")
        private set
    var toastVisible by mutableStateOf(false)
        private set

    /** A pairing link that arrived from outside (a scanned QR code), waiting for a yes. */
    var pendingPairing by mutableStateOf<Seal.Pairing?>(null)
        private set

    /** Things for the screen to start: the share sheet for an item. */
    val launches = Channel<Intent>(Channel.BUFFERED)

    private var liveJob: Job? = null
    private var toastJob: Job? = null

    private val client: HubClient? get() = HubClient.from(config)

    val isSetUp: Boolean get() = config.isComplete

    /** Pinned items first, then newest first (the order the hub sends). */
    val ordered: List<Item> get() = items.filter { it.pinned } + items.filter { !it.pinned }

    init {
        // Copies made for the clipboard and the share sheet are only needed briefly.
        viewModelScope.launch(Dispatchers.IO) {
            val cutoff = System.currentTimeMillis() - 24 * 60 * 60 * 1000
            File(app.cacheDir, "shared").listFiles()?.forEach { folder ->
                if (folder.lastModified() < cutoff) folder.deleteRecursively()
            }
        }
    }

    // ---- pairing ----

    private fun apply(new: HubConfig) {
        new.save(app)
        config = new
        items = emptyList()
        problem = null
        thumbnails.evictAll()
        codes.clear()
        start()
    }

    /** A pairing link from a scanned QR code or another app: ask before using it. */
    fun received(link: String) {
        val pairing = Seal.Pairing.parseOrNull(link)
        when {
            pairing == null -> flash("That pairing link is incomplete")
            pairing == config.pairing -> flash("Already paired with this hub")
            else -> pendingPairing = pairing
        }
    }

    fun dismissPairing() {
        pendingPairing = null
    }

    /** Shows a problem above the feed. */
    fun report(message: String) {
        problem = message
    }

    /** Joins a hub with a pairing link. Returns what went wrong, or null. */
    suspend fun pair(pairing: Seal.Pairing): String? {
        pendingPairing = null
        val client = HubClient.to(pairing.hub, pairing.token, pairing.key, config.device)
            ?: return "That pairing link is incomplete."
        try {
            val check = client.hubCheck()
            if (check.isEmpty()) {
                client.claim() // the hub was reset since the link was made
            } else if (check != pairing.key.check) {
                return "This link's key isn't the one the hub uses now. Make a fresh link on a device that works."
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: HubError.WrongToken) {
            return "The hub doesn't accept this link's token."
        } catch (e: Exception) {
            return e.message ?: "Something went wrong."
        }
        apply(config.copy(server = pairing.hub, token = pairing.token, key = pairing.key.text))
        flash("Paired")
        return null
    }

    /**
     * The first device of a feed: makes the key. [code] is the hub's token
     * (your own hub) or an invite code (someone else's); a public hub needs
     * neither. With [startOver], a feed that already has a key is wiped
     * first. Returns what went wrong, or null.
     */
    suspend fun setUp(server: String, code: String, startOver: Boolean): String? {
        val hub = Seal.normalizeUrl(server) ?: return "That address doesn't look right."
        val key = Seal.Key.new()
        var token = code.trim()
        try {
            val first = HubClient.to(hub, token, key, config.device) ?: return "That address doesn't look right."
            when {
                token.isEmpty() -> token = first.join("")
                startOver -> first.resetHub()
                else -> try {
                    if (first.hubCheck().isNotEmpty()) throw HubError.AlreadySetUp()
                } catch (e: HubError.WrongToken) {
                    // Not a token here: maybe an invite code.
                    token = try {
                        first.join(token)
                    } catch (e: HubError.Server) {
                        if (e.code == 403) throw HubError.Server(403, "The hub didn't accept that as its token or as an invite code.")
                        throw e
                    }
                }
            }
            HubClient.to(hub, token, key, config.device)!!.claim()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return e.message ?: "Something went wrong."
        }
        apply(config.copy(server = hub, token = token, key = key.text))
        return null
    }

    /** Makes a space of your own with an invite link; this phone is its first device. */
    suspend fun join(invite: Seal.Invite): String? {
        val problem = setUp(invite.hub, invite.code, startOver = false)
        if (problem == null) flash("Your space is ready")
        return problem
    }

    /** Deletes everything on the hub and makes a new key; every other device has to pair again. */
    suspend fun startOver(): String? {
        val pairing = config.pairing ?: return HubError.NotSetUp().message
        return setUp(pairing.hub, pairing.token, startOver = true)
    }

    fun unpair() {
        stop()
        apply(config.copy(server = "", token = "", key = ""))
    }

    fun rename(device: String) {
        val name = device.trim().take(HubConfig.MAX_DEVICE_NAME)
        if (name.isEmpty() || name == config.device) return
        val next = config.copy(device = name)
        next.save(app)
        config = next
    }

    // ---- code ----

    // Worked out on the phone, off the main thread: the hub never sees the text.
    private val codes = mutableStateMapOf<String, FetchCode.Result?>()

    /** The language and coloured tokens of a text item that is code; null while unknown or for prose. */
    fun code(item: Item): FetchCode.Result? {
        val text = item.text
        if (!item.isText || text == null) return null
        if (codes.containsKey(item.id)) return codes[item.id]
        codes[item.id] = null
        viewModelScope.launch {
            codes[item.id] = withContext(Dispatchers.Default) { FetchCode.analyze(text) }
        }
        return null
    }

    // ---- connection ----

    /** Loads the feed and keeps it current until [stop]. */
    fun start() {
        stop()
        val client = client ?: return
        liveJob = viewModelScope.launch {
            while (isActive) {
                // A device elsewhere may have started over with a new key.
                try {
                    client.verify()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: HubError) {
                    if (e is HubError.OtherKey || e is HubError.NoEncryption || e is HubError.WrongToken) {
                        items = emptyList()
                        problem = e.message
                        delay(15_000)
                        continue
                    }
                } catch (e: Exception) {
                    // Unreachable for now; refresh() below says so.
                }
                refresh()
                try {
                    // "hello" only confirms the connection; the feed was just loaded.
                    client.events().collect { event ->
                        connection = Connection.Connected // the hub greets with "hello" at once
                        if (event != "hello") refresh()
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // Dropped connections are routine (sleep, Wi-Fi changes); retry below.
                }
                connection = Connection.Offline
                delay(3000)
            }
        }
    }

    fun stop() {
        liveJob?.cancel()
        liveJob = null
        connection = Connection.Connecting
    }

    suspend fun refresh() {
        val client = client ?: return
        try {
            items = client.list()
            problem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            problem = e.message
        }
    }

    fun pullToRefresh() {
        viewModelScope.launch {
            refreshing = true
            refresh()
            delay(300) // long enough for the indicator to be seen
            refreshing = false
        }
    }

    // ---- sending ----

    /** Runs [work] and shows what went wrong, if anything. Sends make sure the key still matches first. */
    private suspend fun attempt(sending: Boolean = false, work: suspend (HubClient) -> Unit) {
        val client = client
        if (client == null) {
            problem = HubError.NotSetUp().message
            return
        }
        if (sending) this.sending++
        try {
            if (sending) client.verify()
            work(client)
            problem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            problem = e.message ?: "Something went wrong."
        } finally {
            if (sending) this.sending--
        }
    }

    fun send(text: String) {
        if (text.isBlank()) return
        viewModelScope.launch {
            attempt(sending = true) { it.sendText(text) }
            refresh()
        }
    }

    /** Sends pictures or files other apps handed over, one after the other. */
    fun send(uris: List<Uri>) {
        if (uris.isEmpty()) return
        viewModelScope.launch {
            for (uri in uris) attempt(sending = true) { Outgoing.send(app, it, uri) }
            refresh()
        }
    }

    /** Sends what is on the clipboard: text, a link, or a copied picture. */
    fun paste() {
        val clipboard = app.getSystemService(ClipboardManager::class.java)
        val clip = try {
            clipboard.primaryClip
        } catch (e: SecurityException) {
            null
        }
        if (clip == null || clip.itemCount == 0) {
            flash("Nothing to paste")
            return
        }
        viewModelScope.launch {
            for (index in 0 until clip.itemCount) {
                val entry = clip.getItemAt(index)
                val uri = entry.uri
                if (uri != null) {
                    attempt(sending = true) { Outgoing.send(app, it, uri) }
                } else {
                    val text = entry.coerceToText(app)?.toString().orEmpty()
                    if (text.isNotBlank()) attempt(sending = true) { it.sendText(text) }
                }
            }
            refresh()
        }
    }

    // ---- acting on items ----

    fun copy(item: Item) {
        viewModelScope.launch {
            val text = item.text
            if (item.isText && text != null) {
                setClipboard(ClipData.newPlainText("Fetch", text), "Copied")
                return@launch
            }
            attempt { client ->
                val uri = shareable(client, item)
                val clip = ClipData.newUri(app.contentResolver, item.name ?: "Fetch", uri)
                setClipboard(clip, if (item.isImage) "Picture copied" else "Copied")
            }
        }
    }

    private fun setClipboard(clip: ClipData, confirmation: String) {
        app.getSystemService(ClipboardManager::class.java).setPrimaryClip(clip)
        // Android 13 and later show their own preview of what was copied.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) flash(confirmation)
    }

    /** Pictures go to the gallery (Pictures/Fetch), anything else to Downloads. */
    fun save(item: Item) {
        viewModelScope.launch {
            attempt { client ->
                val resolver = app.contentResolver
                val volume = MediaStore.VOLUME_EXTERNAL_PRIMARY
                val collection = if (item.isImage) {
                    MediaStore.Images.Media.getContentUri(volume)
                } else {
                    MediaStore.Downloads.getContentUri(volume)
                }
                val values = ContentValues().apply {
                    put(MediaStore.MediaColumns.DISPLAY_NAME, fileName(item))
                    item.mime?.let { put(MediaStore.MediaColumns.MIME_TYPE, it) }
                    put(
                        MediaStore.MediaColumns.RELATIVE_PATH,
                        if (item.isImage) Environment.DIRECTORY_PICTURES + "/Fetch" else Environment.DIRECTORY_DOWNLOADS,
                    )
                    put(MediaStore.MediaColumns.IS_PENDING, 1)
                }
                withContext(Dispatchers.IO) {
                    val target = resolver.insert(collection, values)
                        ?: throw IOException("Couldn't create the file on this phone.")
                    try {
                        val stream = resolver.openOutputStream(target)
                            ?: throw IOException("Couldn't create the file on this phone.")
                        stream.use { client.download(item, it) }
                        val done = ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }
                        resolver.update(target, done, null, null)
                    } catch (e: Throwable) {
                        resolver.delete(target, null, null)
                        throw e
                    }
                }
                flash(if (item.isImage) "Saved to Photos" else "Saved to Downloads")
            }
        }
    }

    /** Opens the share sheet, for handing an item to another app. */
    fun share(item: Item) {
        viewModelScope.launch {
            val text = item.text
            if (item.isText && text != null) {
                val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
                launches.trySend(Intent.createChooser(send, null))
                return@launch
            }
            attempt { client ->
                val uri = shareable(client, item)
                val send = Intent(Intent.ACTION_SEND)
                    .setType(item.mime ?: "*/*")
                    .putExtra(Intent.EXTRA_STREAM, uri)
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                // The clip is what carries the read permission through the share sheet.
                send.clipData = ClipData.newRawUri(null, uri)
                launches.trySend(Intent.createChooser(send, null))
            }
        }
    }

    fun togglePin(item: Item) {
        viewModelScope.launch {
            attempt { it.setPinned(item, !item.pinned) }
            refresh()
        }
    }

    fun delete(item: Item) {
        // A swipe can report itself twice; the second time the item is already gone.
        if (items.none { it.id == item.id }) return
        items = items.filterNot { it.id == item.id }
        viewModelScope.launch {
            attempt { it.delete(item) }
            refresh()
        }
    }

    fun clearUnpinned() {
        viewModelScope.launch {
            attempt { it.clearUnpinned() }
            refresh()
        }
    }

    private fun fileName(item: Item): String {
        val fallback = if (item.isText) "text.txt" else "file"
        return File(item.name ?: fallback).name.ifBlank { fallback }
    }

    /**
     * Downloads an item into a file that carries its real name and returns an
     * address other apps are allowed to read it from.
     */
    private suspend fun shareable(client: HubClient, item: Item): Uri {
        val file = withContext(Dispatchers.IO) {
            val folder = File(app.cacheDir, "shared/${item.id}")
            folder.mkdirs()
            File(folder, fileName(item))
        }
        if (!file.isFile || file.length() != item.size) client.download(item, file)
        return FileProvider.getUriForFile(app, "${app.packageName}.files", file)
    }

    // ---- pictures in the feed ----

    // Full photos are several megabytes each; rows only ever hold a
    // screen-sized, decoded copy.
    private val thumbnails = object : LruCache<String, Bitmap>(THUMBNAIL_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.allocationByteCount
    }
    private val thumbnailLoads = Semaphore(3)

    suspend fun thumbnail(item: Item): Bitmap? {
        thumbnails.get(item.id)?.let { return it }
        val client = client ?: return null
        return thumbnailLoads.withPermit {
            try {
                val data = client.raw(item)
                val image = withContext(Dispatchers.Default) { downsample(data, 1400) }
                if (image != null) thumbnails.put(item.id, image)
                image
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                null
            }
        }
    }

    private fun downsample(data: ByteArray, maxPixel: Int): Bitmap? = try {
        ImageDecoder.decodeBitmap(ImageDecoder.createSource(ByteBuffer.wrap(data))) { decoder, info, _ ->
            val longest = maxOf(info.size.width, info.size.height)
            if (longest > maxPixel) {
                val scale = maxPixel.toFloat() / longest
                decoder.setTargetSize(
                    maxOf(1, (info.size.width * scale).roundToInt()),
                    maxOf(1, (info.size.height * scale).roundToInt()),
                )
            }
        }
    } catch (e: IOException) {
        null
    } catch (e: OutOfMemoryError) {
        null
    }

    private fun flash(message: String) {
        toast = message
        toastVisible = true
        toastJob?.cancel()
        toastJob = viewModelScope.launch {
            delay(1600)
            toastVisible = false
        }
    }

    private companion object {
        const val THUMBNAIL_BYTES = 64 * 1024 * 1024
    }
}
