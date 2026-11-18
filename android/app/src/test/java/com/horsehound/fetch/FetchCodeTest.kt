package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The detector is a port of hub/web/fetch-code.js; its results must match the
 * reference's, recorded in spec/code-corpus.json, character for character.
 */
class FetchCodeTest {
    private val corpus = SpecFiles.load("code-corpus.json").obj()

    private fun tokens(any: Any?) = any.arr().map { it.arr().let { t -> t[0].str() to t[1].str() } }
    private fun show(s: String) = Json.quote(s)

    @Test
    fun plainTextIsNotCode() {
        for (text in corpus["plain"].arr()) assertEquals(show(text.str()), "", FetchCode.detect(text.str()))
    }

    @Test
    fun codeIsRecognisedWithItsLanguage() {
        for (c in corpus["code"].arr().map { it.obj() }) assertEquals(show(c["text"].str()), c["lang"], FetchCode.detect(c["text"].str()))
    }

    @Test
    fun detectionMatchesTheReference() {
        for (c in corpus["detect"].arr().map { it.obj() }) assertEquals(show(c["text"].str()), c["lang"], FetchCode.detect(c["text"].str()))
    }

    @Test
    fun highlightingMatchesTheReference() {
        for (c in corpus["highlight"].arr().map { it.obj() }) {
            assertEquals("${c["lang"]}: ${show(c["text"].str())}", tokens(c["tokens"]), FetchCode.highlight(c["text"].str(), c["lang"].str()))
        }
    }

    @Test
    fun mutatedInputsKeepTheirResults() {
        for (c in corpus["fuzz"].arr().map { it.obj() }) {
            val text = c["text"].str()
            val lang = c["lang"].str()
            assertEquals(show(text), lang, FetchCode.detect(text))
            if (lang.isNotEmpty()) assertEquals(show(text), tokens(c["tokens"]), FetchCode.highlight(text, lang))
            assertEquals(show(text), tokens(c["code"]), FetchCode.highlight(text, "code"))
        }
    }

    @Test
    fun theRightPartsAreMarked() {
        for (c in corpus["checks"].arr().map { it.obj() }) {
            val got = FetchCode.highlight(c["text"].str(), c["lang"].str()).groupBy({ it.first }, { it.second })
            for ((cls, token) in c["want"].obj()) {
                assertTrue("${c["lang"]}: $token as $cls", got[cls].orEmpty().contains(token))
            }
        }
    }

    @Test
    fun labelsMatch() {
        val labels = corpus["labels"].obj()
        for ((id, label) in labels) assertEquals(label, FetchCode.label(id))
        assertEquals(labels.keys.sorted(), FetchCode.languages.sorted())
    }

    @Test
    fun tokensAlwaysReassembleTheText() {
        val inputs = corpus["plain"].arr().map { it.str() } + corpus["code"].arr().map { it.obj()["text"].str() } + corpus["edge"].arr().map { it.str() }
        for (lang in FetchCode.languages) {
            for (text in inputs) {
                val t = FetchCode.highlight(text, lang)
                assertEquals("$lang: ${show(text)}", text, t.joinToString("") { it.second })
                assertTrue("$lang: empty token", t.none { it.second.isEmpty() })
            }
        }
    }

    @Test
    fun analyzeLeavesProseAndDocumentsAlone() {
        assertNull(FetchCode.analyze("see you at 5"))
        val a = FetchCode.analyze("sudo pacman -Syu")!!
        assertEquals("bash", a.lang)
        assertEquals("Shell", a.label)
        assertEquals("sudo pacman -Syu", a.tokens.joinToString("") { it.second })
        assertNull(FetchCode.analyze("x".repeat(70000) + "\n;"))
    }
}
