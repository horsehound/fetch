package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ItemTest {
    // What the hub answers on GET /api/items; it leaves empty fields out.
    private val listing = """
        {"items":[
          {"id":"0193a1","kind":"text","text":"hello\nthere","size":11,"device":"laptop","created":1763360000000,"pinned":true},
          {"id":"0193a0","kind":"image","name":"image-20261117-064500.jpg","mime":"image/jpeg","size":2048576,"created":1763350000000,"pinned":false}
        ]}
    """.trimIndent()

    @Test
    fun readsTheFeed() {
        val items = Item.parseList(listing)
        assertEquals(2, items.size)

        val text = items[0]
        assertTrue(text.isText)
        assertEquals("hello\nthere", text.text)
        assertEquals("laptop", text.device)
        assertEquals(1763360000000L, text.created)
        assertTrue(text.pinned)
        assertNull(text.name)

        val image = items[1]
        assertTrue(image.isImage)
        assertFalse(image.isText)
        assertEquals("image-20261117-064500.jpg", image.name)
        assertEquals("image/jpeg", image.mime)
        assertEquals(2048576L, image.size)
        assertNull(image.text)
        assertNull(image.device)
    }

    @Test
    fun readsWhatTheHubSaysAboutCode() {
        val item = Item.parseList(
            """{"items":[{"id":"1","kind":"text","text":"git status","size":10,"created":1,"pinned":false,
               "lang":"bash","lang_label":"Shell","tokens":[["fn","git"],[""," status"]]}]}""",
        ).single()
        assertEquals("bash", item.lang)
        assertEquals("Shell", item.langLabel)
        assertEquals(listOf("fn" to "git", "" to " status"), item.tokens)
        // The tokens, joined, are exactly the text.
        assertEquals(item.text, item.tokens!!.joinToString("") { it.second })
    }

    @Test
    fun plainTextCarriesNoTokens() {
        assertNull(Item.parseList(listing)[0].tokens)
        assertNull(Item.parseList(listing)[0].lang)
    }

    @Test
    fun anEmptyFeedIsEmpty() {
        assertTrue(Item.parseList("""{"items":[]}""").isEmpty())
    }
}
