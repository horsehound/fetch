package com.horsehound.fetch

import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.URLEncoder
import java.net.UnknownHostException
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
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONException

/** Everything that can go wrong talking to the hub, worded for the person holding the phone. */
sealed class HubError(message: String) : Exception(message) {
    class NotSetUp : HubError("Add your hub address and token in Settings.")
    class WrongToken : HubError("The hub rejected the token. Check it in Settings.")
    class NotAHub : HubError("Something answered at that address, but it isn't a Fetch hub.")
    class Server(code: Int, message: String) : HubError(message.ifEmpty { "The hub answered $code." })
    class Unreachable(why: String) : HubError("Can't reach the hub. $why".trim())
}

/** Thrown while uploading when the thing to send can't be read on this device. */
class UnreadableException : IOException("Couldn't read what was shared.")

class HubClient private constructor(
    private val base: HttpUrl,
    private val token: String,
    private val device: String,
) {
    // ---- reading ----

    suspend fun list(): List<Item> = perform(request("api/items").build()) { response ->
        try {
            Item.parseList(response.body!!.string())
        } catch (e: JSONException) {
            throw HubError.NotAHub()
        }
    }

    suspend fun raw(item: Item): ByteArray =
        perform(request("api/items/${item.id}/raw").build()) { it.body!!.bytes() }

    /** Streams an item into [target], so large files never sit in memory. */
    suspend fun download(item: Item, target: OutputStream) {
        perform(request("api/items/${item.id}/raw").build()) { response ->
            response.body!!.byteStream().copyTo(target)
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
        perform(request("api/items").post(text.toRequestBody(TEXT)).build()) {}
    }

    suspend fun sendBytes(data: ByteArray, name: String?, mime: String) {
        sendBody(data.toRequestBody(mime.toMediaTypeOrNull() ?: BINARY), name)
    }

    /** Uploads whatever [body] produces; a streaming body keeps large files out of memory. */
    suspend fun sendBody(body: RequestBody, name: String?) {
        val builder = if (name.isNullOrBlank()) request("api/items") else request("api/items", "name" to name)
        perform(builder.post(body).build()) {}
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
            return HubClient(base, config.token.trim(), config.device.trim().ifEmpty { HubConfig.DEFAULT_DEVICE })
        }

        private val TEXT = "text/plain; charset=utf-8".toMediaType()
        private val BINARY = "application/octet-stream".toMediaType()
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
