package com.horsehound.fetch

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.net.Uri
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.core.content.FileProvider
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.ByteArrayOutputStream
import java.io.File
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Drives the real app against a real hub. Run it with the hub's address and
 * token (see ci/device-test.sh):
 *
 *     adb shell am instrument -w -e hubUrl http://10.0.2.2:8787 -e hubToken <token> \
 *         com.horsehound.fetch.test/androidx.test.runner.AndroidJUnitRunner
 *
 * Without them the tests are skipped.
 */
@RunWith(AndroidJUnit4::class)
class HubDeviceTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context: Context = instrumentation.targetContext
    private val arguments = InstrumentationRegistry.getArguments()
    private lateinit var client: HubClient

    /** Stands in for the other devices on the hub. */
    private lateinit var laptop: HubClient

    @Before
    fun connect() {
        val url = arguments.getString("hubUrl")
        val token = arguments.getString("hubToken")
        assumeTrue("needs -e hubUrl and -e hubToken", url != null && token != null)
        val config = HubConfig(server = url!!, token = token!!, key = key.text, device = "Test phone")
        config.save(context)
        client = HubClient.from(config)!!
        laptop = HubClient.from(config.copy(device = "Laptop"))!!
        runBlocking {
            // The hub may be fresh, or set up by an earlier test with another key.
            val check = client.hubCheck()
            if (check != key.check) {
                if (check.isNotEmpty()) client.resetHub()
                client.claim()
            }
            client.list().filter { it.pinned }.forEach { client.setPinned(it, false) }
            client.clearUnpinned()
        }
    }

    @Test
    fun feedShowsWhatOtherDevicesSentAndSendsBack() {
        runBlocking {
            laptop.sendBytes(picture(Bitmap.CompressFormat.PNG), name = null, mime = "image/png")
            laptop.sendBytes("%PDF-1.4 not really".toByteArray(), name = "boarding-pass.pdf", mime = "application/pdf")
            laptop.sendText("https://github.com/horsehound/fetch")
            laptop.sendText("journalctl -u fetch-hub | grep -E 'open|token'")
            laptop.sendText("hello from the laptop")
            val pinned = laptop.list().first { it.text == "hello from the laptop" }
            laptop.setPinned(pinned, true)
        }
        val image = runBlocking { client.list().first { it.isImage } }

        ActivityScenario.launch(MainActivity::class.java).use {
            // The feed loads and shows every kind of item.
            waitFor { compose.onAllNodesWithText("hello from the laptop").fetchSemanticsNodes().isNotEmpty() }
            waitFor { compose.onAllNodesWithText("boarding-pass.pdf").fetchSemanticsNodes().isNotEmpty() }
            waitFor { compose.onAllNodesWithContentDescription(image.name!!).fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithContentDescription("Pinned").assertExists()
            // The shell command is shown as a code block with its language.
            compose.onNodeWithText("SHELL").assertExists()
            compose.onNodeWithText("journalctl -u fetch-hub | grep -E 'open|token'").assertExists()

            // Typing and sending puts a new item on the hub, under this phone's name.
            compose.onNode(hasSetTextAction()).performTextInput("hello from the phone")
            screenshot("typing", settle = 2000) // the keyboard takes a moment to slide in
            compose.onNodeWithContentDescription("Send").performClick()
            val sent = waitForItem { it.text == "hello from the phone" }
            assertEquals("Test phone", sent.device)
            waitFor { compose.onAllNodesWithText("hello from the phone").fetchSemanticsNodes().isNotEmpty() }

            // Something sent from elsewhere while the app is open arrives by itself.
            runBlocking { laptop.sendText("arrived live") }
            waitFor { compose.onAllNodesWithText("arrived live").fetchSemanticsNodes().isNotEmpty() }

            screenshot("feed")

            // Settings opens from the menu with what is saved, and closes again.
            compose.onNodeWithContentDescription("More").performClick()
            compose.onNodeWithText("Settings").performClick()
            waitFor { compose.onAllNodesWithText("This device").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Test phone").assertExists()
            compose.onNodeWithText(key.fingerprint).assertExists()
            screenshot("settings")
            // The pairing code for another device.
            compose.onNodeWithText("Show pairing code").performClick()
            waitFor { compose.onAllNodesWithText("Copy link").fetchSemanticsNodes().isNotEmpty() }
            screenshot("pairing")
            compose.onNodeWithContentDescription("Close").performClick()
            waitFor { compose.onAllNodesWithText("arrived live").fetchSemanticsNodes().isNotEmpty() }
        }
    }

    @Test
    fun shareSheetSendsText() {
        val share = Intent(Intent.ACTION_SEND)
            .setClass(context, ShareActivity::class.java)
            .setType("text/plain")
            .putExtra(Intent.EXTRA_TEXT, "https://example.com/shared-from-a-browser")
        ActivityScenario.launch<ShareActivity>(share)
        val arrived = waitForItem { it.text == "https://example.com/shared-from-a-browser" }
        assertEquals("Test phone", arrived.device)
    }

    @Test
    fun shareSheetSendsPicturesAndFiles() {
        // The app's own file provider stands in for the gallery or a file manager.
        val folder = File(context.cacheDir, "shared/device-test").apply { mkdirs() }
        val photo = File(folder, "photo.webp").apply { writeBytes(picture(Bitmap.CompressFormat.WEBP_LOSSLESS)) }
        val notes = File(folder, "notes.pdf").apply { writeBytes("%PDF-1.4 notes".toByteArray()) }
        val uris = arrayListOf(photo, notes).mapTo(ArrayList()) {
            FileProvider.getUriForFile(context, "${context.packageName}.files", it)
        }
        val share = Intent(Intent.ACTION_SEND_MULTIPLE)
            .setClass(context, ShareActivity::class.java)
            .setType("*/*")
            .putParcelableArrayListExtra(Intent.EXTRA_STREAM, uris)
        ActivityScenario.launch<ShareActivity>(share)

        val picture = waitForItem { it.isImage }
        assertEquals("image/webp", picture.mime)
        assertEquals(photo.length(), picture.size)
        val file = waitForItem { it.name == "notes.pdf" }
        assertEquals("file", file.kind)
        assertEquals(notes.length(), file.size)
    }

    @Test
    fun downloadsMatchWhatWasUploaded() {
        val original = picture(Bitmap.CompressFormat.PNG)
        val item = runBlocking {
            client.sendBytes(original, name = "round-trip.png", mime = "image/png")
            client.list().first { it.name == "round-trip.png" }
        }
        val file = File(context.cacheDir, "shared/device-test-download/round-trip.png")
        file.parentFile!!.mkdirs()
        runBlocking { client.download(item, file) }
        assertTrue(original.contentEquals(file.readBytes()))
        assertTrue(original.contentEquals(runBlocking { client.raw(item) }))
    }

    @Test
    fun aWrongTokenIsReportedAsSuch() {
        val stranger = HubClient.from(HubConfig(arguments.getString("hubUrl")!!, "not-the-token", key.text))!!
        val failure = runCatching { runBlocking { stranger.list() } }.exceptionOrNull()
        assertTrue("got $failure", failure is HubError.WrongToken)
    }

    @Test
    fun withoutTheKeyNothingCanBeRead() {
        runBlocking {
            client.sendText("plain words the hub must not see")
            val item = client.list().first { it.text == "plain words the hub must not see" }
            // Anyone with the token but not the key, the hub included, sees a locked item.
            val listing = HubClient.to(arguments.getString("hubUrl")!!, arguments.getString("hubToken")!!, Seal.Key.new(), "x")!!
                .list().first { it.id == item.id }
            assertTrue(listing.broken)
            assertEquals(null, listing.text)
        }
    }

    @Test
    fun itemsUnderAnotherKeyShowAsLocked() {
        val other = HubClient.to(arguments.getString("hubUrl")!!, arguments.getString("hubToken")!!, Seal.Key.new(), "Old laptop")!!
        runBlocking {
            other.sendText("sealed with a key this phone doesn't have")
            val locked = client.list().single { it.broken }
            assertNull(locked.text)
            // A device with another key is told so instead of sending into the void.
            val failure = runCatching { other.verify() }.exceptionOrNull()
            assertTrue("got $failure", failure is HubError.OtherKey)
        }
        ActivityScenario.launch(MainActivity::class.java).use {
            waitFor { compose.onAllNodesWithText("Locked with a different key", substring = true).fetchSemanticsNodes().isNotEmpty() }
            screenshot("locked")
        }
    }

    @Test
    fun aPairingLinkPairsThePhone() {
        val link = Seal.Pairing(Seal.normalizeUrl(arguments.getString("hubUrl")!!)!!, arguments.getString("hubToken")!!, key).link
        runBlocking { laptop.sendText("waiting for the new phone") }
        // A phone that knows nothing yet.
        HubConfig(device = "Test phone").save(context)
        val open = Intent(Intent.ACTION_VIEW, Uri.parse(link)).setClass(context, MainActivity::class.java)
        ActivityScenario.launch<MainActivity>(open).use {
            waitFor { compose.onAllNodesWithText("Pair with this hub?").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Key ${key.fingerprint}.", substring = true).assertExists()
            screenshot("pair-prompt")
            compose.onNodeWithText("Pair").performClick()
            waitFor { compose.onAllNodesWithText("waiting for the new phone").fetchSemanticsNodes().isNotEmpty() }
        }
        assertEquals(key.text, HubConfig.load(context).key)
    }

    @Test
    fun convertsPicturesBrowsersCannotShow() {
        // Every format Android reads goes through the same decoder as HEIC does.
        val converted = ImagePrep.toJpeg(picture(Bitmap.CompressFormat.PNG))
        assertNotNull(converted)
        assertEquals("image/jpeg", ImagePrep.sniff(converted!!.data)?.mime)
    }

    // ---- helpers ----

    companion object {
        /** The key every test in this run shares, as paired devices would. */
        val key: Seal.Key = Seal.Key.new()
    }

    private fun waitFor(condition: () -> Boolean) = compose.waitUntil(20_000, condition)

    private fun waitForItem(match: (Item) -> Boolean): Item {
        val deadline = System.currentTimeMillis() + 20_000
        while (true) {
            runBlocking { client.list() }.firstOrNull(match)?.let { return it }
            check(System.currentTimeMillis() < deadline) { "the item never reached the hub" }
            Thread.sleep(250)
        }
    }

    private fun picture(format: Bitmap.CompressFormat): ByteArray {
        val bitmap = Bitmap.createBitmap(960, 600, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.drawColor(Color.rgb(24, 24, 28))
        val paint = Paint(Paint.ANTI_ALIAS_FLAG)
        paint.color = Color.WHITE
        canvas.drawCircle(560f, 270f, 190f, paint)
        paint.strokeWidth = 34f
        paint.strokeCap = Paint.Cap.ROUND
        for (i in 0..2) canvas.drawLine(120f + i * 110, 470f - i * 20, 300f + i * 110, 330f - i * 20, paint)
        return ByteArrayOutputStream().also { bitmap.compress(format, 100, it) }.toByteArray()
    }

    /** Saved where ci/device-test.sh collects it, so a person can look at the result. */
    private fun screenshot(name: String, settle: Long = 700) {
        compose.waitForIdle()
        Thread.sleep(settle) // let the last frame reach the display
        val shot = instrumentation.uiAutomation.takeScreenshot() ?: return
        val folder = File(context.filesDir, "screens")
        folder.mkdirs()
        File(folder, "$name.png").outputStream().use { shot.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}
