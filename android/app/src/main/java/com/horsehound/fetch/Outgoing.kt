package com.horsehound.fetch

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import java.io.InputStream
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody
import okio.BufferedSink
import okio.source

/**
 * Sends whatever a content URI points at. The photo picker, the clipboard and
 * the share sheet all hand over URIs, so all three end up here.
 */
object Outgoing {
    suspend fun send(context: Context, client: HubClient, uri: Uri) {
        val resolver = context.contentResolver
        val (name, mime, head) = withContext(Dispatchers.IO) {
            val name = displayName(resolver, uri)
            Triple(name, mimeType(resolver, uri, name), head(resolver, uri))
        }

        // Pictures other devices can open go out exactly as they are; the hub names them.
        val friendly = ImagePrep.sniff(head)
        if (friendly != null) {
            client.sendBody(UriBody(resolver, uri, friendly.mime.toMediaTypeOrNull()), name = null)
            return
        }
        if (mime?.startsWith("image/") == true) {
            val converted = withContext(Dispatchers.Default) {
                read(resolver, uri)?.let { ImagePrep.toJpeg(it) }
            }
            if (converted != null) {
                client.sendBytes(converted.data, name = null, mime = converted.mime)
                return
            }
        }
        // Everything else is a file: videos, PDFs, archives, documents.
        client.sendBody(UriBody(resolver, uri, (mime ?: "application/octet-stream").toMediaTypeOrNull()), name)
    }

    private fun displayName(resolver: ContentResolver, uri: Uri): String? {
        var name: String? = null
        if (uri.scheme == ContentResolver.SCHEME_CONTENT) {
            try {
                resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
                    val column = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    if (cursor.moveToFirst() && column >= 0 && !cursor.isNull(column)) {
                        name = cursor.getString(column)
                    }
                }
            } catch (e: Exception) {
                // Not every app that shares something answers questions about it.
            }
        }
        return name?.takeIf { it.isNotBlank() } ?: uri.lastPathSegment?.substringAfterLast('/')
    }

    private fun mimeType(resolver: ContentResolver, uri: Uri, name: String?): String? {
        val declared = try {
            resolver.getType(uri)
        } catch (e: Exception) {
            null
        }
        if (declared != null && declared != "application/octet-stream") return declared
        val extension = name?.substringAfterLast('.', "")?.lowercase().orEmpty()
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension) ?: declared
    }

    private fun open(resolver: ContentResolver, uri: Uri): InputStream {
        val stream = try {
            resolver.openInputStream(uri)
        } catch (e: Exception) {
            null
        }
        return stream ?: throw UnreadableException()
    }

    /** The first 12 bytes, which is where picture formats identify themselves. */
    private fun head(resolver: ContentResolver, uri: Uri): ByteArray = open(resolver, uri).use { stream ->
        val buffer = ByteArray(12)
        var filled = 0
        while (filled < buffer.size) {
            val count = stream.read(buffer, filled, buffer.size - filled)
            if (count < 0) break
            filled += count
        }
        buffer.copyOf(filled)
    }

    private fun read(resolver: ContentResolver, uri: Uri): ByteArray? = try {
        open(resolver, uri).use { it.readBytes() }
    } catch (e: OutOfMemoryError) {
        null
    }

    /**
     * Streams straight from the other app. The length is left open on purpose:
     * what apps report as a file's size isn't reliable enough to promise the hub.
     */
    private class UriBody(
        private val resolver: ContentResolver,
        private val uri: Uri,
        private val type: MediaType?,
    ) : RequestBody() {
        override fun contentType(): MediaType? = type

        override fun writeTo(sink: BufferedSink) {
            open(resolver, uri).source().use { sink.writeAll(it) }
        }
    }
}
