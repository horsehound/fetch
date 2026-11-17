package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Json follows JSON.parse, which decides what the detector calls JSON. */
class JsonTest {
    @Test
    fun acceptsWhatJsonParseAccepts() {
        for (s in listOf("{}", "[]", " [1, -2.5e+3, 0.1, true, false, null] ", """{"a":{"b":[{"c":"\u00e9\n\/"}]}}""", "\"x\"", "0", "[[[]]]")) {
            assertTrue(s, Json.isValid(s))
        }
        assertTrue(Json.isValid("[".repeat(100_000) + "]".repeat(100_000)))
    }

    @Test
    fun refusesWhatJsonParseRefuses() {
        for (s in listOf("", "{", "[1,]", "{\"a\":1,}", "{'a':1}", "[01]", "[1.]", "[.5]", "[+1]", "{a:1}", "[\"tab\there\"]", "[1] x", "NaN", "[\"\\x\"]", "[\"\\u12\"]", "{\"a\" 1}", "[1 2]")) {
            assertFalse(s, Json.isValid(s))
        }
    }

    @Test
    fun readsValues() {
        val v = Json.parse("""{"k":"v","n":12,"l":[true,null],"k":"last"}""") as Map<*, *>
        assertEquals("last", v["k"])
        assertEquals(12.0, v["n"])
        assertEquals(listOf(true, null), v["l"])
    }
}
