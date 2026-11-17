package com.horsehound.fetch

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import java.io.InputStream
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

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

        // Pictures other devices can open go out exactly as they are, under a fresh name.
        val friendly = ImagePrep.sniff(head)
        if (friendly != null) {
            client.sendStream(name = null, mime = friendly.mime, size = null) { open(resolver, uri) }
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
        // Everything else is a file: videos, PDFs, archives, documents. A
        // picture that isn't one of the shared formats (and couldn't be
        // converted) goes as a file too, so nobody expects it inline.
        val type = (mime ?: "application/octet-stream").takeUnless { it in HubClient.INLINE_IMAGES } ?: "application/octet-stream"
        client.sendStream(name = name ?: "file-${HubClient.stamp()}", mime = type, size = null) { open(resolver, uri) }
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
}
