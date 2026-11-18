package com.horsehound.fetch

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The code detector against spec/code-corpus.json on a real device. Android's
 * regular expressions are ICU's, not the JVM's the unit tests run on, so the
 * port has to give the reference's results here too.
 */
@RunWith(AndroidJUnit4::class)
class CodeCorpusDeviceTest {
    @Suppress("UNCHECKED_CAST")
    private val corpus: Map<String, Any?> = InstrumentationRegistry.getInstrumentation().context.assets
        .open("code-corpus.json").use { Json.parse(it.readBytes().toString(Charsets.UTF_8)) } as Map<String, Any?>

    @Suppress("UNCHECKED_CAST")
    private fun list(key: String) = corpus[key] as List<Map<String, Any?>>

    @Suppress("UNCHECKED_CAST")
    private fun tokens(any: Any?) = (any as List<List<String>>).map { it[0] to it[1] }

    @Test
    fun detectionAndHighlightingMatchTheReference() {
        for (text in corpus["plain"] as List<String>) assertEquals(Json.quote(text), "", FetchCode.detect(text))
        for (c in list("detect") + list("code")) {
            assertEquals(Json.quote(c["text"] as String), c["lang"], FetchCode.detect(c["text"] as String))
        }
        for (c in list("highlight")) {
            assertEquals(Json.quote(c["text"] as String), tokens(c["tokens"]), FetchCode.highlight(c["text"] as String, c["lang"] as String))
        }
    }

    @Test
    fun mutatedInputsKeepTheirResults() {
        for (c in list("fuzz")) {
            val text = c["text"] as String
            val lang = c["lang"] as String
            assertEquals(Json.quote(text), lang, FetchCode.detect(text))
            if (lang.isNotEmpty()) assertEquals(Json.quote(text), tokens(c["tokens"]), FetchCode.highlight(text, lang))
            assertEquals(Json.quote(text), tokens(c["code"]), FetchCode.highlight(text, "code"))
        }
    }
}
