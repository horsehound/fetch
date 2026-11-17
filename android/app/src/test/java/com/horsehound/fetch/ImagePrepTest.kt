package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ImagePrepTest {
    private fun head(vararg bytes: Int) = ByteArray(12) { if (it < bytes.size) bytes[it].toByte() else 0 }

    @Test
    fun recognisesWhatBrowsersCanShow() {
        assertEquals("image/jpeg", ImagePrep.sniff(head(0xFF, 0xD8, 0xFF, 0xE0))?.mime)
        assertEquals("image/png", ImagePrep.sniff(head(0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A))?.mime)
        assertEquals("image/gif", ImagePrep.sniff(head(0x47, 0x49, 0x46, 0x38, 0x39, 0x61))?.mime)
        val webp = head(0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50)
        assertEquals("image/webp", ImagePrep.sniff(webp)?.mime)
        assertEquals("webp", ImagePrep.sniff(webp)?.ext)
    }

    @Test
    fun leavesEverythingElseToBeConverted() {
        // HEIC: an "ftyp" box, like every photo from a recent phone camera.
        assertNull(ImagePrep.sniff(head(0, 0, 0, 0x18, 0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x63)))
        // A RIFF file that isn't WebP (a WAV recording).
        assertNull(ImagePrep.sniff(head(0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x41, 0x56, 0x45)))
        assertNull(ImagePrep.sniff(byteArrayOf(0xFF.toByte(), 0xD8.toByte(), 0xFF.toByte())))
    }
}
