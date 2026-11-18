package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HubConfigTest {
    private val key = Seal.Key.new().text

    private fun url(server: String) = HubConfig(server = server, token = "t", key = key).baseUrl?.toString()

    @Test
    fun addressesOnAHomeNetworkGetHttp() {
        assertEquals("http://192.168.1.20:8787/", url("192.168.1.20:8787"))
        assertEquals("http://192.168.1.20/", url("192.168.1.20"))
        assertEquals("http://proxmox.lan/", url("proxmox.lan"))
        assertEquals("http://nas.local/", url("nas.local"))
        assertEquals("http://nas/", url("nas"))
        assertEquals("http://fetch.example.com:8787/", url("fetch.example.com:8787"))
    }

    @Test
    fun domainsGetHttps() {
        assertEquals("https://fetch.example.com/", url("fetch.example.com"))
        // Every client talks to the hub's origin.
        assertEquals("https://example.com/", url("example.com/fetch/"))
    }

    @Test
    fun aSchemeThatWasTypedIsKept() {
        assertEquals("https://192.168.1.20:8787/", url(" https://192.168.1.20:8787/ "))
        assertEquals("http://fetch.example.com/", url("http://fetch.example.com"))
    }

    @Test
    fun nonsenseIsNotAnAddress() {
        assertNull(url(""))
        assertNull(url("   "))
        assertNull(url("ftp://example.com"))
        assertNull(url("not an address"))
    }

    @Test
    fun completeNeedsAddressTokenAndKey() {
        assertTrue(HubConfig("192.168.1.20:8787", "token", key).isComplete)
        assertFalse(HubConfig("192.168.1.20:8787", "  ", key).isComplete)
        assertFalse(HubConfig("", "token", key).isComplete)
        assertFalse(HubConfig("192.168.1.20:8787", "token", "").isComplete)
        assertFalse(HubConfig("192.168.1.20:8787", "token", "not-a-key").isComplete)
    }

    @Test
    fun aPairedConfigMakesItsOwnLink() {
        val config = HubConfig(" 192.168.1.20:8787/ ", " abc ", key)
        val pairing = Seal.Pairing.parse(config.pairing!!.link)
        assertEquals("http://192.168.1.20:8787", pairing.hub)
        assertEquals("abc", pairing.token)
        assertEquals(key, pairing.key.text)
    }
}
