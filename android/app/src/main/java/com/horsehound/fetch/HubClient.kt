package com.horsehound.fetch

import java.io.File
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.URLEncoder
import java.net.UnknownHostException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.channels.trySendBlocking
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink

/** Everything that can go wrong talking to the hub, worded for the person holding the phone. */
sealed class HubError(message: String) : Exception(message) {
    class NotSetUp : HubError("Pair this phone in Settings.")
    class WrongToken : HubError("The hub rejected the token. Pair again in Settings.")
    class NotAHub : HubError("Something answered at that address, but it isn't a Fetch hub.")
    class OtherKey : HubError("This phone's key doesn't match the hub's anymore. Pair again in Settings.")
    class AlreadySetUp : HubError("This hub already has a key. Pair from a device that has it, or start over.")
    class NoEncryption : HubError("This hub doesn't support encryption yet. Update it first.")
    class Server(val code: Int, message: String) : HubError(message.ifEmpty { "The hub answered $code." })
    class Unreachable(why: String) : HubError("Can't reach the hub. $why".trim())
}

/** Thrown while uploading when the thing to send can't be read on this device. */
class UnreadableException : IOException("Couldn't read what was shared.")

/**
 * Talks to the hub. Everything is sealed with [key] before it is sent and
 * opened after it arrives; the hub only handles ciphertext.
 */
class HubClient(
    private val base: HttpUrl,
    private val token: String,
    val key: Seal.Key,
    private val device: String,
) {
    // ---- spaces ----

    /**
     * Makes a space of your own on this hub, with an invite code or (on a
     * public hub) none, and returns its token. Needs no token itself.
     */
    suspend fun join(code: String): String {
        val body = "{\"invite\":${Json.quote(code)}}".toRequestBody(JSON)
        val request = Request.Builder().url(base.newBuilder().addPathSegments("api/spaces").build()).post(body).build()
        val answer = try {
            perform(request) { it.body!!.string() }
        } catch (e: HubError.Server) {
            if (e.code == 404 || e.code == 405) throw HubError.Server(e.code, "This hub doesn't hand out spaces. It may need an update.")
            throw e
        }
        val o = try {
            Json.parse(answer) as? Map<*, *>
        } catch (e: Json.Invalid) {
            null
        }
        return o?.get("token") as? String ?: throw HubError.NotAHub()
    }

    // ---- the key ----

    /** The check of the key the hub was set up with, or "" if nobody has set it up. */
    suspend fun hubCheck(): String {
        val answer = try {
            perform(request("api/vault").build()) { it.body!!.string() }
        } catch (e: HubError.Server) {
            // A hub from before encryption doesn't know the vault.
            if (e.code == 404) throw HubError.NoEncryption()
            throw e
        }
        val o = try {
            Json.parse(answer) as? Map<*, *>
        } catch (e: Json.Invalid) {
            null
        }
        return o?.get("check") as? String ?: throw HubError.NoEncryption()
    }

    /** Makes this phone's key the hub's. */
    suspend fun claim() {
        val body = "{\"check\":\"${key.check}\"}".toRequestBody(JSON)
        try {
            perform(request("api/vault").put(body).build()) {}
        } catch (e: HubError.Server) {
            if (e.code == 409) throw HubError.AlreadySetUp()
            throw e
        }
    }

    /** Deletes everything on the hub, pinned too, and its key's check. */
    suspend fun resetHub() {
        perform(request("api/vault").delete().build()) {}
    }

    /** Throws unless this phone's key is the hub's. */
    suspend fun verify() {
        if (hubCheck() != key.check) throw HubError.OtherKey()
    }

    // ---- reading ----

    suspend fun list(): List<Item> = perform(request("api/items").build()) { response ->
        try {
            Item.parseList(response.body!!.string(), key)
        } catch (e: Json.Invalid) {
            throw HubError.NotAHub()
        }
    }

    /** An item's data, decrypted. For pictures in the feed; files go through [download]. */
    suspend fun raw(item: Item): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        download(item, out)
        return out.toByteArray()
    }

    /** Streams an item into [target], decrypting as it arrives, so large files never sit in memory. */
    suspend fun download(item: Item, target: OutputStream) {
        val salt = item.salt ?: throw Seal.CannotOpen()
        perform(request("api/items/${item.id}/raw").build()) { response ->
            Seal.openData(response.body!!.byteStream(), target, key, salt)
        }
    }

    /** Downloads an item into [file], which only appears once it is complete. */
    suspend fun download(item: Item, file: File): File {
        val partial = File(file.parentFile, file.name + ".part")
        try {
            partial.outputStream().use { download(item, it) }
            if (!partial.renameTo(file)) throw IOException("Couldn't store ${file.name}.")
        } finally {
            partial.delete()
        }
        return file
    }

    /**
     * A long-lived stream of what the hub announces: "hello" once connected,
     * then one value every time the feed changes.
     */
    fun events(): Flow<String> = callbackFlow {
        val call = streaming.newCall(request("api/events").build())
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                close(readable(e))
            }

            override fun onResponse(call: Call, response: Response) {
                val failure = try {
                    response.use {
                        verify(it)
                        val source = it.body!!.source()
                        while (true) {
                            val line = source.readUtf8Line() ?: break
                            if (line.startsWith("data:")) trySendBlocking(line.removePrefix("data:").trim())
                        }
                    }
                    null
                } catch (e: Exception) {
                    if (e is IOException) readable(e) else e
                }
                close(failure)
            }
        })
        awaitClose { call.cancel() }
    }

    // ---- writing ----

    suspend fun sendText(text: String) {
        val bytes = text.toByteArray(Charsets.UTF_8)
        if (bytes.size > Seal.MAX_TEXT_IN_META) {
            sendBytes(bytes, name = "text-${stamp()}.txt", mime = "text/plain")
            return
        }
        upload(Seal.Meta("text", device = device, text = text)) { _, _ -> }
    }

    suspend fun sendBytes(data: ByteArray, name: String?, mime: String) {
        sendStream(name, mime, data.size.toLong()) { data.inputStream() }
    }

    /**
     * Encrypts and uploads what [open] reads, as it is read, so large files
     * never sit in memory. [size] is the plaintext size if it is known.
     */
    suspend fun sendStream(name: String?, mime: String, size: Long?, open: () -> InputStream) {
        val kind = if (mime in INLINE_IMAGES) "image" else "file"
        val fileName = name?.takeIf { it.isNotBlank() } ?: ("$kind-${stamp()}" + (EXTENSIONS[mime]?.let { ".$it" } ?: ""))
        upload(Seal.Meta(kind, device = device, name = fileName, mime = mime, size = size)) { salt, sink ->
            open().use { Seal.sealData(it, sink.outputStream(), key, salt) }
        }
    }

    private suspend fun upload(meta: Seal.Meta, data: (ByteArray, BufferedSink) -> Unit) {
        val envelope = Seal.sealMeta(meta, key)
        val head = Seal.uploadHead(envelope)
        val body = object : RequestBody() {
            override fun contentType() = SEALED

            // Text travels in the envelope with no data after it, so its
            // length is known; files stream.
            override fun contentLength(): Long = if (meta.kind == "text") head.size.toLong() else -1

            // Sent once: a second attempt would encrypt whatever the source
            // holds by then under the same key and nonces.
            override fun isOneShot() = true

            override fun writeTo(sink: BufferedSink) {
                sink.write(head)
                if (meta.kind != "text") data(envelope.salt, sink)
                sink.flush()
            }
        }
        perform(request("api/items").post(body).build()) {}
    }

    suspend fun delete(item: Item) {
        perform(request("api/items/${item.id}").delete().build()) {}
    }

    suspend fun setPinned(item: Item, on: Boolean) {
        perform(request("api/items/${item.id}/pin", "on" to if (on) "1" else "0").put(EMPTY).build()) {}
    }

    suspend fun clearUnpinned() {
        perform(request("api/items").delete().build()) {}
    }

    // ---- plumbing ----

    private fun request(path: String, vararg query: Pair<String, String>): Request.Builder {
        // Header values have to be plain ASCII; a token that isn't can't be right.
        if (!token.all { it in '!'..'~' }) throw HubError.WrongToken()
        val url = base.newBuilder().addPathSegments(path)
        for ((key, value) in query) url.addQueryParameter(key, value)
        return Request.Builder()
            .url(url.build())
            .header("Authorization", "Bearer $token")
            // Percent-encoded, as the hub expects, so any name survives the trip.
            .header("X-Device", URLEncoder.encode(device, "UTF-8").replace("+", "%20"))
    }

    /** Runs a request and hands the successful response to [read] off the main thread. */
    private suspend fun <T> perform(request: Request, read: (Response) -> T): T {
        val call = http.newCall(request)
        return suspendCancellableCoroutine { continuation ->
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) {
                    continuation.resumeWithException(readable(e))
                }

                override fun onResponse(call: Call, response: Response) {
                    val result = try {
                        response.use {
                            verify(it)
                            read(it)
                        }
                    } catch (e: Exception) {
                        continuation.resumeWithException(if (e is IOException) readable(e) else e)
                        return
                    }
                    continuation.resume(result)
                }
            })
        }
    }

    private fun verify(response: Response) {
        if (response.code == 401) throw HubError.WrongToken()
        if (response.code >= 300) {
            val message = try {
                response.peekBody(300).string().trim()
            } catch (e: IOException) {
                ""
            }
            throw HubError.Server(response.code, message)
        }
    }

    /** Turns transport failures into something readable. */
    private fun readable(e: IOException): Exception = when (e) {
        is UnreadableException -> e
        is Seal.CannotOpen -> e
        is UnknownHostException -> HubError.Unreachable("That address couldn't be found.")
        is SocketTimeoutException -> HubError.Unreachable("It didn't answer in time.")
        is ConnectException -> HubError.Unreachable("Nothing is answering at that address.")
        is SSLException -> HubError.Unreachable("The secure connection failed.")
        else -> HubError.Unreachable(e.message.orEmpty())
    }

    companion object {
        fun from(config: HubConfig): HubClient? {
            if (!config.isComplete) return null
            val base = config.baseUrl ?: return null
            val key = config.sealKey ?: return null
            return HubClient(base, config.token.trim(), key, config.device.trim().ifEmpty { HubConfig.DEFAULT_DEVICE })
        }

        /** For a hub that isn't saved yet: pairing and setting up. */
        fun to(hub: String, token: String, key: Seal.Key, device: String): HubClient? {
            val base = Seal.normalizeUrl(hub)?.let { "$it/".toHttpUrlOrNull() } ?: return null
            return HubClient(base, token.trim(), key, device.trim().ifEmpty { HubConfig.DEFAULT_DEVICE })
        }

        /** Pictures every device can show; anything else goes as a file. */
        val INLINE_IMAGES = setOf("image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp")
        private val EXTENSIONS = mapOf(
            "image/png" to "png", "image/jpeg" to "jpg", "image/gif" to "gif", "image/webp" to "webp",
            "image/avif" to "avif", "image/bmp" to "bmp", "text/plain" to "txt",
        )

        fun stamp(): String = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.ROOT).format(Date())

        private val SEALED = Seal.CONTENT_TYPE.toMediaType()
        private val JSON = "application/json".toMediaType()
        private val EMPTY = ByteArray(0).toRequestBody()

        private val http: OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .build()

        // The hub pings every 20 s, so 90 s of silence means the link is dead.
        private val streaming: OkHttpClient = http.newBuilder()
            .readTimeout(90, TimeUnit.SECONDS)
            .build()
    }
}
