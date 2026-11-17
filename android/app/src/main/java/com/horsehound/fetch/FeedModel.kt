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

/** The feed and everything the screen can do with it. */
class FeedModel(private val app: Application) : AndroidViewModel(app) {
    var items by mutableStateOf(emptyList<Item>())
        private set
    var config by mutableStateOf(HubConfig.load(app))
        private set
    var problem by mutableStateOf<String?>(null)
        private set
    var refreshing by mutableStateOf(false)
        private set

    /** The last short confirmation ("Copied"), and whether it is on screen right now. */
    var toast by mutableStateOf("")
        private set
    var toastVisible by mutableStateOf(false)
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

    // ---- connection ----

    fun apply(new: HubConfig) {
        new.save(app)
        config = new
        items = emptyList()
        problem = null
        thumbnails.evictAll()
        start()
    }

    /** Asks the hub for the feed with [draft]; returns what went wrong, or null if it answered. */
    suspend fun check(draft: HubConfig): String? {
        val client = HubClient.from(draft) ?: return HubError.NotSetUp().message
        return try {
            client.list()
            null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            e.message ?: "Something went wrong."
        }
    }

    /** Loads the feed and keeps it current until [stop]. */
    fun start() {
        stop()
        val client = client ?: return
        liveJob = viewModelScope.launch {
            while (isActive) {
                refresh()
                try {
                    // "hello" only confirms the connection; the feed was just loaded.
                    client.events().collect { event ->
                        if (event != "hello") refresh()
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // Dropped connections are routine (sleep, Wi-Fi changes); retry below.
                }
                delay(3000)
            }
        }
    }

    fun stop() {
        liveJob?.cancel()
        liveJob = null
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

    private suspend fun attempt(work: suspend (HubClient) -> Unit) {
        val client = client
        if (client == null) {
            problem = HubError.NotSetUp().message
            return
        }
        try {
            work(client)
            problem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            problem = e.message ?: "Something went wrong."
        }
    }

    fun send(text: String) {
        if (text.isBlank()) return
        viewModelScope.launch {
            attempt { it.sendText(text) }
            refresh()
        }
    }

    /** Sends pictures or files other apps handed over, one after the other. */
    fun send(uris: List<Uri>) {
        if (uris.isEmpty()) return
        viewModelScope.launch {
            for (uri in uris) attempt { Outgoing.send(app, it, uri) }
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
                    attempt { Outgoing.send(app, it, uri) }
                } else {
                    val text = entry.coerceToText(app)?.toString().orEmpty()
                    if (text.isNotBlank()) attempt { it.sendText(text) }
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
