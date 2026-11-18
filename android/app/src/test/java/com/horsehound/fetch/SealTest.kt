package com.horsehound.fetch

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.Base64
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** The phone must read and write exactly what the reference does (spec/vectors.json). */
class SealTest {
    private val v = SpecFiles.load("vectors.json").obj()
    private val key = Seal.Key.parse(v["key"].str())
    private val items = v["items"].arr().map { it.obj() }

    private fun b64(s: Any?) = Base64.getDecoder().decode(s.str())
    private fun hex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    private fun pattern(n: Int) = ByteArray(n) { (it * 7 + 3).toByte() }

    @Test
    fun keyCheckAndLink() {
        assertEquals(v["key_hex"], Seal.hex(key.bytes))
        assertEquals(v["key"], key.text)
        assertEquals(v["check"], key.check)
        assertEquals(v["check"].str().substring(0, 8).uppercase(), key.fingerprint.replace("-", ""))
        assertEquals(v["link"], Seal.Pairing(v["hub"].str(), v["token"].str(), key).link)

        val p = Seal.Pairing.parse("  " + v["link"] + "\n")
        assertEquals(v["hub"], p.hub)
        assertEquals(v["token"], p.token)
        assertEquals(key, p.key)
    }

    @Test
    fun linksThatArentWholeAreRefused() {
        assertNull(Seal.Pairing.parseOrNull("https://example.com"))
        assertNull(Seal.Pairing.parseOrNull("fetchpair://v1?hub=a&token=b"))
        assertNull(Seal.Pairing.parseOrNull("fetchpair://v1?hub=a&token=b&key=short"))
        assertNull(Seal.Pairing.parseOrNull("fetchpair://v1?hub=a&key=${key.text}"))
        assertNull(Seal.Key.parseOrNull("EBESExQVFhcYGRobHB0eHyAhIiMkJSYnKCkqKywtLi+"))
        val local = Seal.Pairing.parse("fetchpair://v1?hub=192.168.1.20%3A8787&token=t&key=${key.text}")
        assertEquals("http://192.168.1.20:8787", local.hub)
        val domain = Seal.Pairing.parse("FETCHPAIR://v1?token=a+b&key=${key.text}&hub=fetch.example.com%2F")
        assertEquals("https://fetch.example.com", domain.hub)
        assertEquals("a b", domain.token)
    }

    @Test
    fun addressesAreNormalisedLikeEverywhereElse() {
        assertEquals("http://192.168.1.20:8787", Seal.normalizeUrl("192.168.1.20:8787"))
        assertEquals("http://proxmox.lan", Seal.normalizeUrl("proxmox.lan/"))
        assertEquals("http://nas", Seal.normalizeUrl("nas"))
        assertEquals("https://fetch.example.com", Seal.normalizeUrl(" fetch.example.com "))
        assertEquals("https://192.168.1.20:8787", Seal.normalizeUrl("HTTPS://192.168.1.20:8787/"))
        assertNull(Seal.normalizeUrl(""))
        assertNull(Seal.normalizeUrl("ftp://example.com"))
        assertNull(Seal.normalizeUrl("not an address"))
    }

    @Test
    fun envelopesAndDataMatchTheReference() {
        for (item in items) {
            val name = item["name"].str()
            val env = b64(item["envelope"])
            val (meta, salt) = Seal.openMeta(env, key)
            assertEquals(name, item["salt"], Seal.hex(salt))
            val want = item["meta"].obj()
            assertEquals(name, want["kind"], meta.kind)
            assertEquals(name, want["device"], meta.device)
            assertEquals(name, want["text"], meta.text)
            assertEquals(name, want["name"], meta.name)
            assertEquals(name, want["mime"], meta.mime)
            assertEquals(name, (want["size"] as Double?)?.toLong(), meta.size)

            // What this side writes opens to the same thing.
            val again = Seal.sealMeta(meta, key, salt)
            assertEquals(name, meta, Seal.openMeta(again.bytes, key).first)

            if ((item["data"] as String?).isNullOrEmpty() && item["data_size"] == 0.0 && meta.kind == "text") continue
            val sealed = b64(item["data"])
            val n = (item["data_size"] as Double).toInt()
            assertArrayEquals(name, pattern(n), Seal.openData(sealed, key, salt))
            assertArrayEquals(name, sealed, Seal.sealData(pattern(n), key, salt))
            assertEquals(name, sealed.size.toLong(), Seal.sealedSize(n.toLong()))
        }
    }

    @Test
    fun alteredItemsDontOpen() {
        for (c in v["corrupt"].arr().map { it.obj() }) {
            val item = items[(c["of"] as Double).toInt()]
            val env = b64(item["envelope"])
            var data = b64(item["data"])
            val op = c["op"].str()
            when {
                op == "flip_envelope" -> env[env.size - 1] = (env[env.size - 1].toInt() xor 1).toByte()
                op == "append_zero" -> data += byteArrayOf(0)
                op == "swap_first_chunks" -> data = data.copyOfRange(65552, 131104) + data.copyOfRange(0, 65552) + data.copyOfRange(131104, data.size)
                op.startsWith("keep:") -> data = data.copyOf(op.removePrefix("keep:").toInt())
                else -> fail("unknown corruption $op")
            }
            try {
                val (_, salt) = Seal.openMeta(env, key)
                Seal.openData(data, key, salt)
                fail("${c["name"]} opened")
            } catch (e: Seal.CannotOpen) {
                // expected
            }
        }
        try {
            Seal.openMeta(b64(items[0]["envelope"]), Seal.Key.new())
            fail("opened with another key")
        } catch (e: Seal.CannotOpen) {
        }
    }

    @Test
    fun streamsRoundTripAtChunkEdges() {
        for (n in listOf(0, 1, 65535, 65536, 65537, 131072, 200_000)) {
            val salt = Seal.randomBytes(Seal.SALT)
            val sealed = ByteArrayOutputStream()
            assertEquals(n.toLong(), Seal.sealData(ByteArrayInputStream(pattern(n)), sealed, key, salt))
            assertEquals("$n bytes", Seal.sealedSize(n.toLong()), sealed.size().toLong())
            val opened = ByteArrayOutputStream()
            Seal.openData(ByteArrayInputStream(sealed.toByteArray()), opened, key, salt)
            assertArrayEquals("$n bytes", pattern(n), opened.toByteArray())
        }
    }

    @Test
    fun uploadsStartWithTheEnvelopesLength() {
        val env = Seal.sealMeta(Seal.Meta("text", device = "Pixel", text = "hi"), key)
        val head = Seal.uploadHead(env)
        assertEquals(env.bytes.size, ((head[0].toInt() and 255) shl 24) or ((head[1].toInt() and 255) shl 16) or ((head[2].toInt() and 255) shl 8) or (head[3].toInt() and 255))
        assertArrayEquals(env.bytes, head.copyOfRange(4, head.size))
        assertNotEquals(Seal.Key.new(), Seal.Key.new())
    }

    @Test
    fun metadataSurvivesAnyText() {
        val text = "quotes \" back\\slash\n\ttab \u0001 grüße 🎾 </script>"
        val env = Seal.sealMeta(Seal.Meta("text", device = "Pixel 8", text = text), key)
        val meta = Seal.openMeta(env.bytes, key).first
        assertEquals(text, meta.text)
        assertEquals("Pixel 8", meta.device)
        assertTrue(Json.parse(Seal.Meta("file", name = "a.pdf", size = 5).toJson()) is Map<*, *>)
    }
}
