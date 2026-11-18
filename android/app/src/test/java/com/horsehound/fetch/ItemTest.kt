package com.horsehound.fetch

import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ItemTest {
    private val key = Seal.Key.new()

    private fun entry(id: String, meta: Seal.Meta, dataSize: Long, pinned: Boolean = false, with: Seal.Key = key): String {
        val env = Base64.getEncoder().encodeToString(Seal.sealMeta(meta, with).bytes)
        return """{"id":"$id","meta":"$env","size":${Seal.sealedSize(dataSize).takeIf { meta.kind != "text" } ?: 0},"created":1763360000000,"pinned":$pinned}"""
    }

    @Test
    fun readsAndDecryptsTheFeed() {
        val json = """{"items":[
            ${entry("0193a1", Seal.Meta("text", device = "laptop", text = "hello\nthere"), 0, pinned = true)},
            ${entry("0193a0", Seal.Meta("image", device = "iPhone", name = "shot.jpg", mime = "image/jpeg", size = 2048576), 2048576)}
        ]}"""
        val items = Item.parseList(json, key)
        assertEquals(2, items.size)

        val text = items[0]
        assertTrue(text.isText)
        assertEquals("hello\nthere", text.text)
        assertEquals("laptop", text.device)
        assertEquals(1763360000000L, text.created)
        assertTrue(text.pinned)
        assertNull(text.name)
        assertFalse(text.broken)

        val image = items[1]
        assertTrue(image.isImage)
        assertEquals("shot.jpg", image.name)
        assertEquals("image/jpeg", image.mime)
        assertEquals(2048576L, image.size)
        assertEquals(Seal.SALT, image.salt!!.size)
    }

    @Test
    fun itemsUnderAnotherKeyAreMarkedNotDropped() {
        val json = """{"items":[${entry("x", Seal.Meta("text", text = "secret"), 0, with = Seal.Key.new())}]}"""
        val item = Item.parseList(json, key).single()
        assertTrue(item.broken)
        assertFalse(item.isText)
        assertNull(item.text)
    }

    @Test
    fun plaintextSizesComeBackFromSealedSizes() {
        for (n in listOf(0L, 1L, 65535L, 65536L, 65537L, 131072L, 10_000_000L)) {
            assertEquals(n, Item.plainSize(Seal.sealedSize(n)))
        }
    }

    @Test
    fun anEmptyFeedIsEmpty() {
        assertTrue(Item.parseList("""{"items":[]}""", key).isEmpty())
    }
}
