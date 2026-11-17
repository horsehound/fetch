package com.horsehound.fetch

import android.content.Context
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Where the hub is and how this device introduces itself to it. */
data class HubConfig(
    val server: String = "",
    val token: String = "",
    val device: String = DEFAULT_DEVICE,
) {
    val isComplete: Boolean get() = baseUrl != null && token.isNotBlank()

    /**
     * Accepts full http(s) URLs, and fills in the scheme when it is left out:
     * http for an address on a home network ("192.168.1.20:8787",
     * "proxmox.lan"), https for a domain ("fetch.example.com").
     */
    val baseUrl: HttpUrl?
        get() {
            var text = server.trim()
            if (text.isEmpty()) return null
            if (!text.contains("://")) {
                val host = text.substringBefore('/')
                val isLocal = host.contains(':') || !host.contains('.') ||
                    host.endsWith(".local") || host.endsWith(".lan") ||
                    host.all { it.isDigit() || it == '.' }
                text = (if (isLocal) "http://" else "https://") + text
            }
            return text.trimEnd('/').toHttpUrlOrNull()
        }

    fun save(context: Context) {
        prefs(context).edit()
            .putString("server", server.trim())
            .putString("token", token.trim())
            .putString("device", device.trim())
            .apply()
    }

    companion object {
        const val DEFAULT_DEVICE = "Android"

        // The app and its share sheet target are one process, so plain
        // preferences are enough for both to see the same settings.
        private fun prefs(context: Context) =
            context.applicationContext.getSharedPreferences("hub", Context.MODE_PRIVATE)

        fun load(context: Context): HubConfig {
            val p = prefs(context)
            return HubConfig(
                server = p.getString("server", null).orEmpty(),
                token = p.getString("token", null).orEmpty(),
                device = p.getString("device", null).orEmpty().ifBlank { DEFAULT_DEVICE },
            )
        }
    }
}
