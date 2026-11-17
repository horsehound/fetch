package com.horsehound.fetch

import android.graphics.Bitmap
import android.graphics.ColorSpace
import android.graphics.ImageDecoder
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.nio.ByteBuffer

/**
 * Phone cameras save HEIC, which browsers on Linux and Windows cannot show.
 * Anything that is not already a web-friendly format is converted to JPEG
 * before it leaves the phone.
 */
object ImagePrep {
    class Format(val mime: String, val ext: String)

    class Prepared(val data: ByteArray, val mime: String, val ext: String)

    /** The format of a picture every browser can show, judged by its first 12 bytes. */
    fun sniff(head: ByteArray): Format? {
        if (head.size < 12) return null
        fun startsWith(vararg bytes: Int, at: Int = 0) =
            bytes.indices.all { head[at + it] == bytes[it].toByte() }
        return when {
            startsWith(0xFF, 0xD8, 0xFF) -> Format("image/jpeg", "jpg")
            startsWith(0x89, 0x50, 0x4E, 0x47) -> Format("image/png", "png")
            startsWith(0x47, 0x49, 0x46, 0x38) -> Format("image/gif", "gif")
            startsWith(0x52, 0x49, 0x46, 0x46) && startsWith(0x57, 0x45, 0x42, 0x50, at = 8) ->
                Format("image/webp", "webp")
            else -> null
        }
    }

    /** Re-encodes any picture Android can read as JPEG, or returns null if it can't. */
    fun toJpeg(data: ByteArray): Prepared? = try {
        val source = ImageDecoder.createSource(ByteBuffer.wrap(data))
        // The decoder applies the photo's rotation, so the JPEG is upright
        // without needing any metadata.
        val bitmap = ImageDecoder.decodeBitmap(source) { decoder, _, _ ->
            decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            decoder.setTargetColorSpace(ColorSpace.get(ColorSpace.Named.SRGB))
        }
        val output = ByteArrayOutputStream()
        val written = bitmap.compress(Bitmap.CompressFormat.JPEG, 90, output)
        bitmap.recycle()
        if (written) Prepared(output.toByteArray(), "image/jpeg", "jpg") else null
    } catch (e: IOException) {
        null
    } catch (e: OutOfMemoryError) {
        // A picture too large to convert on this phone goes out untouched.
        null
    }
}
