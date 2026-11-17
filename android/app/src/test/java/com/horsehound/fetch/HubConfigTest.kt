package com.horsehound.fetch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HubConfigTest {
    private fun url(server: String) = HubConfig(server = server, token = "t").baseUrl?.toString()

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
        assertEquals("https://example.com/fetch", url("example.com/fetch/"))
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
    fun completeNeedsBothAddressAndToken() {
        assertTrue(HubConfig("192.168.1.20:8787", "token").isComplete)
        assertFalse(HubConfig("192.168.1.20:8787", "  ").isComplete)
        assertFalse(HubConfig("", "token").isComplete)
    }
}
