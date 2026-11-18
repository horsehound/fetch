package com.horsehound.fetch

import android.content.Context
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Which hub this phone is paired with, its key, and the name it sends under. */
data class HubConfig(
    val server: String = "",
    val token: String = "",
    val key: String = "",
    val device: String = DEFAULT_DEVICE,
) {
    val isComplete: Boolean get() = baseUrl != null && token.isNotBlank() && sealKey != null

    /** The hub's origin; see [Seal.normalizeUrl] for how a typed address is read. */
    val baseUrl: HttpUrl? get() = Seal.normalizeUrl(server)?.let { "$it/".toHttpUrlOrNull() }

    val sealKey: Seal.Key? get() = Seal.Key.parseOrNull(key)

    /** The link that pairs another device with the same hub and key. */
    val pairing: Seal.Pairing?
        get() {
            val hub = Seal.normalizeUrl(server) ?: return null
            val key = sealKey ?: return null
            if (token.isBlank()) return null
            return Seal.Pairing(hub, token.trim(), key)
        }

    fun save(context: Context) {
        prefs(context).edit()
            .putString("server", server.trim())
            .putString("token", token.trim())
            .putString("key", key.trim())
            .putString("device", device.trim())
            .apply()
    }

    companion object {
        const val DEFAULT_DEVICE = "Android"
        const val MAX_DEVICE_NAME = 40

        // The app and its share sheet target are one process, so plain
        // preferences are enough for both to see the same settings. They
        // hold the key, which is why backups are off (see the manifest).
        private fun prefs(context: Context) =
            context.applicationContext.getSharedPreferences("hub", Context.MODE_PRIVATE)

        fun load(context: Context): HubConfig {
            val p = prefs(context)
            return HubConfig(
                server = p.getString("server", null).orEmpty(),
                token = p.getString("token", null).orEmpty(),
                key = p.getString("key", null).orEmpty(),
                device = p.getString("device", null).orEmpty().ifBlank { DEFAULT_DEVICE },
            )
        }
    }
}
