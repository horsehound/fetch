package com.horsehound.fetch

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.URI
import java.net.URLDecoder
import java.security.GeneralSecurityException
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * End-to-end encryption, format version 1 (spec/encryption.md). Everything is
 * sealed here before it leaves the phone; the hub only ever stores and relays
 * bytes it can't read. Checked against spec/vectors.json, written by the
 * reference implementation in hub/seal.go.
 */
object Seal {
    const val VERSION: Byte = 1
    const val CHUNK = 64 * 1024
    const val TAG = 16
    const val SEALED_CHUNK = CHUNK + TAG
    const val SALT = 16
    const val CONTENT_TYPE = "application/x-fetch-sealed"

    /** Text up to this size travels inside the metadata; longer text goes as a .txt file. */
    const val MAX_TEXT_IN_META = 256 * 1024

    /** Thrown for anything that can't be opened with this key, or that was altered. */
    class CannotOpen : IOException("This was locked with a different key, or it was changed.")

    class BadLink(message: String) : IllegalArgumentException(message)

    private val random = SecureRandom()

    fun randomBytes(n: Int) = ByteArray(n).also { random.nextBytes(it) }

    // ---- the key ----

    class Key(bytes: ByteArray) {
        val bytes: ByteArray = bytes.copyOf()

        init {
            require(bytes.size == 32) { "A key is 32 bytes." }
        }

        /** Base64url without padding, 43 characters, as in pairing links. */
        val text: String get() = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)

        /** What the hub stores to tell whether a device has the right key. */
        val check: String get() = hex(hkdf(bytes, ByteArray(32), "fetch v1 check", 16))

        /** The first 8 characters of the check, for comparing by eye: "ABCD-EF01". */
        val fingerprint: String
            get() = check.substring(0, 8).uppercase().let { it.substring(0, 4) + "-" + it.substring(4) }

        override fun equals(other: Any?) = other is Key && other.bytes.contentEquals(bytes)
        override fun hashCode() = bytes.contentHashCode()
        override fun toString() = "Key($fingerprint)"

        companion object {
            fun new() = Key(randomBytes(32))

            fun parse(text: String): Key {
                val t = text.trim()
                if (t.length != 43 || !t.all { it.isLetterOrDigit() && it.code < 128 || it == '-' || it == '_' }) {
                    throw BadLink("That isn't a Fetch key.")
                }
                return Key(Base64.getUrlDecoder().decode(t))
            }

            fun parseOrNull(text: String?): Key? = try {
                if (text.isNullOrBlank()) null else parse(text)
            } catch (e: IllegalArgumentException) {
                null
            }
        }
    }

    // ---- key derivation (RFC 5869) ----

    fun hkdf(ikm: ByteArray, salt: ByteArray, info: String, length: Int): ByteArray {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(salt, "HmacSHA256"))
        val prk = mac.doFinal(ikm)
        mac.init(SecretKeySpec(prk, "HmacSHA256"))
        val out = ByteArrayOutputStream()
        var previous = ByteArray(0)
        var counter = 1
        while (out.size() < length) {
            mac.update(previous)
            mac.update(info.toByteArray(Charsets.UTF_8))
            mac.update(counter.toByte())
            previous = mac.doFinal()
            out.write(previous)
            counter++
        }
        return out.toByteArray().copyOf(length)
    }

    private fun itemKey(key: Key, salt: ByteArray, what: String) = hkdf(key.bytes, salt, "fetch v1 $what", 32)

    private fun gcm(mode: Int, key: ByteArray, nonce: ByteArray, input: ByteArray, offset: Int = 0, length: Int = input.size): ByteArray {
        // A fresh cipher every time: Java refuses to reuse one with the same nonce.
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(mode, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG * 8, nonce))
        return try {
            cipher.doFinal(input, offset, length)
        } catch (e: GeneralSecurityException) {
            throw CannotOpen()
        }
    }

    // ---- metadata ----

    data class Meta(
        val kind: String, // text | image | file
        val device: String? = null,
        val text: String? = null,
        val name: String? = null,
        val mime: String? = null,
        val size: Long? = null,
    ) {
        fun toJson(): String {
            val fields = mutableListOf("\"v\":1", "\"kind\":" + Json.quote(kind))
            device?.takeIf { it.isNotEmpty() }?.let { fields += "\"device\":" + Json.quote(it) }
            text?.takeIf { it.isNotEmpty() }?.let { fields += "\"text\":" + Json.quote(it) }
            name?.takeIf { it.isNotEmpty() }?.let { fields += "\"name\":" + Json.quote(it) }
            mime?.takeIf { it.isNotEmpty() }?.let { fields += "\"mime\":" + Json.quote(it) }
            size?.takeIf { it > 0 }?.let { fields += "\"size\":$it" }
            return fields.joinToString(",", "{", "}")
        }

        companion object {
            fun fromJson(json: String): Meta {
                val o = Json.parse(json) as? Map<*, *> ?: throw CannotOpen()
                fun str(k: String) = o[k] as? String
                return Meta(
                    kind = str("kind") ?: "file",
                    device = str("device"),
                    text = str("text"),
                    name = str("name"),
                    mime = str("mime"),
                    size = (o["size"] as? Double)?.toLong(),
                )
            }
        }
    }

    class Envelope(val bytes: ByteArray, val salt: ByteArray)

    fun sealMeta(meta: Meta, key: Key, salt: ByteArray = randomBytes(SALT)): Envelope {
        val sealed = gcm(Cipher.ENCRYPT_MODE, itemKey(key, salt, "meta"), ByteArray(12), meta.toJson().toByteArray(Charsets.UTF_8))
        return Envelope(byteArrayOf(VERSION) + salt + sealed, salt)
    }

    /** Opens an envelope; returns the metadata and the item's salt, which its data needs. */
    fun openMeta(envelope: ByteArray, key: Key): Pair<Meta, ByteArray> {
        if (envelope.size < 1 + SALT + TAG || envelope[0] != VERSION) throw CannotOpen()
        val salt = envelope.copyOfRange(1, 1 + SALT)
        val plain = gcm(Cipher.DECRYPT_MODE, itemKey(key, salt, "meta"), ByteArray(12), envelope, 1 + SALT, envelope.size - 1 - SALT)
        val meta = try {
            Meta.fromJson(String(plain, Charsets.UTF_8))
        } catch (e: Json.Invalid) {
            throw CannotOpen()
        }
        return meta to salt
    }

    // ---- data ----

    fun nonce(i: Long, last: Boolean): ByteArray {
        val n = ByteArray(12)
        for (b in 0 until 8) n[10 - b] = (i ushr (8 * b)).toByte()
        n[11] = if (last) 1 else 0
        return n
    }

    /** How many bytes [plain] bytes of data become. */
    fun sealedSize(plain: Long): Long = plain + TAG * maxOf(1L, (plain + CHUNK - 1) / CHUNK)

    /** Reads until [buffer] is full or the stream ends; returns how much was read. */
    private fun fill(input: InputStream, buffer: ByteArray, from: Int = 0): Int {
        var filled = from
        while (filled < buffer.size) {
            val n = input.read(buffer, filled, buffer.size - filled)
            if (n < 0) break
            filled += n
        }
        return filled
    }

    /** Encrypts [input] chunk by chunk into [output], never holding more than two chunks. Returns the plaintext size. */
    fun sealData(input: InputStream, output: OutputStream, key: Key, salt: ByteArray): Long {
        val dataKey = itemKey(key, salt, "data")
        var current = ByteArray(CHUNK)
        var next = ByteArray(CHUNK)
        var have = fill(input, current)
        var counter = 0L
        var total = 0L
        while (true) {
            // A full chunk is the last one only if nothing follows it.
            val following = if (have == CHUNK) fill(input, next) else 0
            val last = following == 0
            output.write(gcm(Cipher.ENCRYPT_MODE, dataKey, nonce(counter, last), current, 0, have))
            total += have
            if (last) return total
            val swap = current
            current = next
            next = swap
            have = following
            counter++
        }
    }

    fun sealData(plain: ByteArray, key: Key, salt: ByteArray): ByteArray {
        val out = ByteArrayOutputStream(sealedSize(plain.size.toLong()).toInt())
        sealData(ByteArrayInputStream(plain), out, key, salt)
        return out.toByteArray()
    }

    /** Decrypts sealed data from [input] into [output]; throws [CannotOpen] if anything is off. */
    fun openData(input: InputStream, output: OutputStream, key: Key, salt: ByteArray) {
        val dataKey = itemKey(key, salt, "data")
        val buffer = ByteArray(SEALED_CHUNK)
        var carried = -1
        var counter = 0L
        while (true) {
            var start = 0
            if (carried >= 0) {
                buffer[0] = carried.toByte()
                start = 1
            }
            val n = fill(input, buffer, start)
            if (n < TAG) throw CannotOpen()
            var last = n < SEALED_CHUNK
            if (!last) {
                carried = input.read()
                last = carried < 0
            }
            output.write(gcm(Cipher.DECRYPT_MODE, dataKey, nonce(counter, last), buffer, 0, n))
            if (last) return
            counter++
        }
    }

    fun openData(sealed: ByteArray, key: Key, salt: ByteArray): ByteArray {
        val out = ByteArrayOutputStream(sealed.size)
        openData(ByteArrayInputStream(sealed), out, key, salt)
        return out.toByteArray()
    }

    /** The start of an upload body: the envelope's length (4 bytes, big-endian) and the envelope. */
    fun uploadHead(envelope: Envelope): ByteArray {
        val n = envelope.bytes.size
        return byteArrayOf((n ushr 24).toByte(), (n ushr 16).toByte(), (n ushr 8).toByte(), n.toByte()) + envelope.bytes
    }

    // ---- pairing ----

    data class Pairing(val hub: String, val token: String, val key: Key) {
        val link: String get() = "fetchpair://v1?hub=${queryEscape(hub)}&key=${key.text}&token=${queryEscape(token)}"

        companion object {
            fun parse(link: String): Pairing {
                val s = link.trim()
                if (!s.lowercase().startsWith("fetchpair://v1?")) throw BadLink("That isn't a Fetch pairing link.")
                val query = mutableMapOf<String, String>()
                for (part in s.substringAfter('?').split('&')) {
                    val name = part.substringBefore('=')
                    val value = part.substringAfter('=', "")
                    query[decode(name)] = decode(value)
                }
                val hub = normalizeUrl(query["hub"].orEmpty())
                val token = query["token"].orEmpty().trim()
                val key = Key.parseOrNull(query["key"])
                if (hub == null || token.isEmpty() || key == null) throw BadLink("That pairing link is incomplete.")
                return Pairing(hub, token, key)
            }

            fun parseOrNull(link: String?): Pairing? = try {
                if (link == null) null else parse(link)
            } catch (e: BadLink) {
                null
            }

            private fun decode(s: String) = try {
                URLDecoder.decode(s, "UTF-8")
            } catch (e: IllegalArgumentException) {
                s
            }
        }
    }

    /** Percent-encodes everything but letters, digits and -._~, as Go's url.QueryEscape does. */
    fun queryEscape(s: String): String {
        val out = StringBuilder()
        for (b in s.toByteArray(Charsets.UTF_8)) {
            val c = b.toInt() and 0xff
            when {
                c in 'a'.code..'z'.code || c in 'A'.code..'Z'.code || c in '0'.code..'9'.code || c.toChar() in "-._~" -> out.append(c.toChar())
                c == ' '.code -> out.append('+')
                else -> out.append('%').append("0123456789ABCDEF"[c shr 4]).append("0123456789ABCDEF"[c and 15])
            }
        }
        return out.toString()
    }

    /**
     * The hub's origin, e.g. "http://192.168.1.20:8787". Fills in the scheme
     * when it is left out: http for an address on a home network
     * ("192.168.1.20:8787", "proxmox.lan"), https for a domain. Every client
     * does the same.
     */
    fun normalizeUrl(input: String): String? {
        var text = input.trim().trimEnd('/')
        if (text.isEmpty()) return null
        if (!text.contains("://")) {
            val host = text.substringBefore('/')
            val isLocal = host.contains(':') || !host.contains('.') ||
                host.endsWith(".local") || host.endsWith(".lan") ||
                host.all { it.isDigit() || it == '.' }
            text = (if (isLocal) "http://" else "https://") + text
        }
        return try {
            val uri = URI(text)
            val scheme = uri.scheme?.lowercase()
            val host = uri.host
            if ((scheme != "http" && scheme != "https") || host.isNullOrEmpty()) return null
            "$scheme://$host" + (if (uri.port >= 0) ":${uri.port}" else "")
        } catch (e: Exception) {
            null
        }
    }

    fun hex(b: ByteArray) = b.joinToString("") { "%02x".format(it) }
}
