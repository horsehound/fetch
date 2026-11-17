package com.horsehound.fetch

import java.util.Base64

/** One entry in the feed, decrypted here from what the hub stores. */
data class Item(
    val id: String,
    val kind: String, // text | image | file
    val text: String? = null,
    val name: String? = null,
    val mime: String? = null,
    val size: Long = 0, // plaintext bytes of the data
    val device: String? = null,
    val created: Long = 0, // unix milliseconds
    val pinned: Boolean = false,
    /** The item's salt; its data can only be opened with it. */
    val salt: ByteArray? = null,
    /** Locked with a key this phone doesn't have (or altered): it can only be deleted. */
    val broken: Boolean = false,
) {
    val isText: Boolean get() = kind == "text" && !broken
    val isImage: Boolean get() = kind == "image" && !broken

    // Equality by what is shown; salts are ByteArrays, compared by content.
    override fun equals(other: Any?): Boolean = other is Item && other.id == id && other.pinned == pinned &&
        other.kind == kind && other.text == text && other.name == name && other.broken == broken
    override fun hashCode(): Int = id.hashCode()

    companion object {
        /** Reads and decrypts the answer of `GET /api/items`. */
        fun parseList(json: String, key: Seal.Key): List<Item> {
            val root = Json.parse(json) as? Map<*, *> ?: throw Json.Invalid("no items")
            val array = root["items"] as? List<*> ?: throw Json.Invalid("no items")
            return array.mapNotNull { (it as? Map<*, *>)?.let { o -> parse(o, key) } }
        }

        private fun parse(o: Map<*, *>, key: Seal.Key): Item? {
            val id = o["id"] as? String ?: return null
            val sealedSize = (o["size"] as? Double)?.toLong() ?: 0
            val created = (o["created"] as? Double)?.toLong() ?: 0
            val pinned = o["pinned"] == true
            val opened = try {
                Seal.openMeta(Base64.getDecoder().decode(o["meta"] as? String ?: ""), key)
            } catch (e: Exception) {
                null
            }
            if (opened == null) {
                return Item(id, kind = "file", size = sealedSize, created = created, pinned = pinned, broken = true)
            }
            val (meta, salt) = opened
            return Item(
                id = id,
                kind = meta.kind,
                text = meta.text,
                name = meta.name,
                mime = meta.mime,
                size = plainSize(sealedSize),
                device = meta.device,
                created = created,
                pinned = pinned,
                salt = salt,
            )
        }

        /** The plaintext size behind [sealed] bytes of data (the inverse of Seal.sealedSize). */
        fun plainSize(sealed: Long): Long {
            if (sealed <= 0) return 0
            val chunks = (sealed + Seal.SEALED_CHUNK - 1) / Seal.SEALED_CHUNK
            return maxOf(0, sealed - chunks * Seal.TAG)
        }
    }
}
