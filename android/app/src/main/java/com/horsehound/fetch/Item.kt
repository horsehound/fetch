package com.horsehound.fetch

import org.json.JSONObject

/** One entry in the feed, as the hub describes it. */
data class Item(
    val id: String,
    val kind: String, // text | image | file
    val text: String? = null,
    val name: String? = null,
    val mime: String? = null,
    val size: Long = 0,
    val device: String? = null,
    val created: Long = 0, // unix milliseconds
    val pinned: Boolean = false,
    // Set by the hub when a text item is code: the language, its display
    // name, and the text split into (highlight class, text) pairs.
    val lang: String? = null,
    val langLabel: String? = null,
    val tokens: List<Pair<String, String>>? = null,
) {
    val isText: Boolean get() = kind == "text"
    val isImage: Boolean get() = kind == "image"

    companion object {
        /** Reads the answer of `GET /api/items`. */
        fun parseList(json: String): List<Item> {
            val array = JSONObject(json).getJSONArray("items")
            return List(array.length()) { parse(array.getJSONObject(it)) }
        }

        private fun parse(o: JSONObject) = Item(
            id = o.getString("id"),
            kind = o.getString("kind"),
            text = o.stringOrNull("text"),
            name = o.stringOrNull("name"),
            mime = o.stringOrNull("mime"),
            size = o.optLong("size"),
            device = o.stringOrNull("device"),
            created = o.optLong("created"),
            pinned = o.optBoolean("pinned"),
            lang = o.stringOrNull("lang"),
            langLabel = o.stringOrNull("lang_label"),
            tokens = o.optJSONArray("tokens")?.let { array ->
                (0 until array.length()).mapNotNull { index ->
                    val pair = array.optJSONArray(index)
                    if (pair != null && pair.length() == 2) pair.getString(0) to pair.getString(1) else null
                }
            },
        )

        private fun JSONObject.stringOrNull(key: String): String? =
            if (has(key) && !isNull(key)) getString(key) else null
    }
}
