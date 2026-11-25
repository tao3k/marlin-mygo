// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Self-contained Kotlin/JVM unit, wire-parity, and concurrency stress test
// suite for MarlinDeviceBridge. Compiles and runs directly with `kotlinc` +
// `java` with zero Android Emulator and zero external test runner downloads.

package org.tao3k.marlin.device

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

private class StubPhotoLibrary(
    var state: OsPermissionStateWire = OsPermissionStateWire.GRANTED,
    var screenshots: List<PhotoItemWire> = emptyList(),
    var ocrByLocalId: Map<String, String> = emptyMap(),
) : AndroidPhotoLibrary {
    @Volatile
    var lastRequestedLimit: Int = -1

    override fun permissionState(): OsPermissionStateWire = state

    override fun recentScreenshots(limit: Int): List<PhotoItemWire> {
        lastRequestedLimit = limit
        return screenshots.take(limit)
    }

    override fun extractTextOCR(localId: String): String =
        ocrByLocalId[localId] ?: throw MarlinBridgeException.AssetNotFound(localId)
}

private class RecordingNotificationDeliverer : AndroidNotificationDeliverer {
    val delivered: MutableList<NotificationRequestWire> =
        Collections.synchronizedList(ArrayList())
    var forcedError: Throwable? = null

    override fun deliver(request: NotificationRequestWire) {
        forcedError?.let { throw it }
        delivered.add(request)
    }
}

private class StubVoiceProvider(
    private val transcript: VoiceTranscriptWire,
) : AndroidVoiceProvider {
    override fun latestTranscript(): VoiceTranscriptWire = transcript
}

private class StubContactsProvider(
    private val contacts: List<ContactSummaryWire>,
) : AndroidContactsProvider {
    @Volatile
    var lastQuery: String? = null

    @Volatile
    var lastLimit: Int = -1

    override fun searchContacts(query: String, limit: Int): List<ContactSummaryWire> {
        lastQuery = query
        lastLimit = limit
        val q = query.lowercase()
        val matched = if (q.isEmpty()) contacts else contacts.filter { it.displayName.lowercase().contains(q) }
        return matched.take(limit)
    }
}

private fun assertEquals(expected: Any?, actual: Any?, label: String = "") {
    if (expected != actual) {
        throw AssertionError("FAIL $label: expected=<$expected> actual=<$actual>")
    }
}

private fun assertTrue(cond: Boolean, label: String) {
    if (!cond) throw AssertionError("FAIL $label")
}

private inline fun <reified T : Throwable> assertThrows(label: String, block: () -> Unit): T {
    try {
        block()
    } catch (err: Throwable) {
        if (err is T) return err
        throw AssertionError("FAIL $label: expected ${T::class.simpleName}, got ${err.javaClass.simpleName}: ${err.message}")
    }
    throw AssertionError("FAIL $label: expected ${T::class.simpleName} but nothing was thrown")
}

private fun testPermissionStateWireParityWithGoAdapter() {
    for (state in OsPermissionStateWire.entries) {
        val bridge = MarlinDeviceBridge(
            photos = StubPhotoLibrary(state = state),
            notifications = RecordingNotificationDeliverer(),
        )
        val out = bridge.dispatch("permissionState", """{"platform":"android"}""")
        assertEquals("""{"state":"${state.wireValue}"}""", out, "permissionState($state)")
    }
}

private fun testRecentScreenshotsClampsLimitAndMatchesGoJSONFields() {
    val sample = PhotoItemWire(
        localId = "media-42",
        createdUnixMs = 1_764_000_000_000L,
        width = 1080,
        height = 2400,
        isScreenshot = true,
        mimeType = "image/png",
    )
    val photos = StubPhotoLibrary(screenshots = listOf(sample))
    val bridge = MarlinDeviceBridge(photos = photos, notifications = RecordingNotificationDeliverer())

    bridge.dispatch("photos.recentScreenshots", """{"limit":999}""")
    assertEquals(32, photos.lastRequestedLimit, "clamp >32 to 32")

    val out = bridge.dispatch("photos.recentScreenshots", """{"limit":0}""")
    assertEquals(10, photos.lastRequestedLimit, "default <=0 to 10")
    assertEquals(
        """{"items":[{"createdUnixMs":1764000000000,"height":2400,"isScreenshot":true,"localId":"media-42","mimeType":"image/png","width":1080}]}""",
        out,
        "photos.recentScreenshots wire JSON",
    )
}

private fun testVisionOCRSuccessEscapingAndRejectsEmptyLocalId() {
    val photos = StubPhotoLibrary(ocrByLocalId = mapOf("media-42" to "Line 1\n\"Quoted\" \\ ok"))
    val bridge = MarlinDeviceBridge(photos = photos, notifications = RecordingNotificationDeliverer())

    val out = bridge.dispatch("vision.extractOCR", """{"localId":"media-42"}""")
    assertEquals("""{"text":"Line 1\n\"Quoted\" \\ ok"}""", out, "OCR JSON escaping")

    assertThrows<MarlinBridgeException.InvalidArgument>("empty localId") {
        bridge.dispatch("vision.extractOCR", """{"localId":"   "}""")
    }

    assertThrows<MarlinBridgeException.AssetNotFound>("missing localId") {
        bridge.dispatch("vision.extractOCR", """{"localId":"missing-1"}""")
    }
}

private fun testNotificationBuildsGatePrefixedTagAndRejectsEmptyPayload() {
    val deliverer = RecordingNotificationDeliverer()
    val bridge = MarlinDeviceBridge(photos = StubPhotoLibrary(), notifications = deliverer)

    val out = bridge.dispatch("notify.send", """{"title":"Marlin","body":"Approved"}""")
    assertEquals("""{"ok":true}""", out, "notify.send ack")
    assertEquals(1, deliverer.delivered.size, "delivered count")
    val req = deliverer.delivered[0]
    assertTrue(req.tag.startsWith("marlin.gate."), "gate tag prefix")
    assertEquals("Marlin", req.title, "title")
    assertEquals("Approved", req.body, "body")

    assertThrows<MarlinBridgeException.InvalidArgument>("empty notification") {
        bridge.dispatch("notify.send", """{"title":"  ","body":""}""")
    }
}

private fun testCaptureQueueFIFOAtomicDrainAndRingCap() {
    val bridge = MarlinDeviceBridge(
        photos = StubPhotoLibrary(),
        notifications = RecordingNotificationDeliverer(),
    )

    bridge.enqueueShareCapture(
        CapturePayloadWire(
            sourceChannel = "android.share-intent",
            text = "Draft Q4 roadmap",
            metadata = mapOf("origin" to "chrome"),
        ),
    )
    bridge.enqueueShareCapture(
        CapturePayloadWire(
            sourceChannel = "android.quick-tile",
            ocrText = "Invoice #42",
            mimeType = "image/png",
        ),
    )

    val first = bridge.dispatch("capture.drain", null)
    assertEquals(
        """{"captures":[{"metadata":{"origin":"chrome"},"sourceChannel":"android.share-intent","text":"Draft Q4 roadmap"},{"mimeType":"image/png","ocrText":"Invoice #42","sourceChannel":"android.quick-tile"}]}""",
        first,
        "first FIFO drain",
    )

    // Second drain must be empty (atomic consumption prevents duplicate ingest).
    val second = bridge.dispatch("capture.drain", null)
    assertEquals("""{"captures":[]}""", second, "second drain empty")

    // Exceeding MAX_QUEUED_CAPTURES (64) must evict oldest entries cleanly.
    for (i in 0 until 80) {
        bridge.enqueueShareCapture(CapturePayloadWire(sourceChannel = "android.share-intent", text = "item-$i"))
    }
    val drained = bridge.drainPendingCaptures()
    assertEquals(MarlinDeviceBridge.MAX_QUEUED_CAPTURES, drained.size, "ring cap 64")
    assertEquals("item-16", drained.first().text, "oldest evicted")
    assertEquals("item-79", drained.last().text, "newest preserved")

    assertThrows<MarlinBridgeException.InvalidArgument>("empty sourceChannel") {
        bridge.enqueueShareCapture(CapturePayloadWire(sourceChannel = "   ", text = "bad"))
    }
}

private fun testCapabilitiesVoiceAndContactsWireParity() {
    val voice = StubVoiceProvider(
        VoiceTranscriptWire(
            transcriptId = "vt-kt-1",
            locale = "en-US",
            text = "Ship \"Marlin\" mobile",
            durationMs = 1920L,
        ),
    )
    val contacts = StubContactsProvider(
        listOf(
            ContactSummaryWire("ac-1", "Ada Lovelace", "ada@tao3k.org"),
            ContactSummaryWire("ac-2", "Alan Turing", "alan@tao3k.org"),
        ),
    )
    val bridge = MarlinDeviceBridge(
        photos = StubPhotoLibrary(),
        notifications = RecordingNotificationDeliverer(),
        voice = voice,
        contacts = contacts,
    )

    val caps = bridge.dispatch("bridge.capabilities", null)
    assertTrue(caps.contains(""""platform":"android""""), "android platform")
    assertTrue(caps.contains(""""voice.latestTranscript""""), "voice method advertised")
    assertTrue(caps.contains(""""contacts.search""""), "contacts method advertised")

    val voiceOut = bridge.dispatch("voice.latestTranscript", null)
    assertEquals(
        """{"durationMs":1920,"locale":"en-US","text":"Ship \"Marlin\" mobile","transcriptId":"vt-kt-1"}""",
        voiceOut,
        "voice.latestTranscript wire JSON",
    )

    val contactsOut = bridge.dispatch("contacts.search", """{"query":"  Ada  ","limit":999}""")
    assertEquals("Ada", contacts.lastQuery, "trimmed query")
    assertEquals(MarlinDeviceBridge.MAX_CONTACTS_LIMIT, contacts.lastLimit, "clamped limit to 25")
    assertEquals(
        """{"contacts":[{"contactId":"ac-1","displayName":"Ada Lovelace","handle":"ada@tao3k.org"}]}""",
        contactsOut,
        "contacts.search wire JSON",
    )
}

private fun testUnsupportedMethodFailsClosed() {
    val bridge = MarlinDeviceBridge(
        photos = StubPhotoLibrary(),
        notifications = RecordingNotificationDeliverer(),
    )
    val err = assertThrows<MarlinBridgeException.UnsupportedMethod>("native.* forbidden") {
        bridge.dispatch("native.execShell", null)
    }
    assertEquals("native.execShell", err.method, "method name")
}

private fun testConcurrentDispatchStress100TasksZeroRaces() {
    val photos = StubPhotoLibrary(
        state = OsPermissionStateWire.GRANTED,
        screenshots = listOf(
            PhotoItemWire(
                localId = "media-stress",
                createdUnixMs = 1_764_000_000_001L,
                width = 1440,
                height = 3120,
                isScreenshot = true,
                mimeType = "image/png",
            ),
        ),
        ocrByLocalId = mapOf("media-stress" to "Concurrency Safe"),
    )
    val deliverer = RecordingNotificationDeliverer()
    val bridge = MarlinDeviceBridge(photos = photos, notifications = deliverer)

    val pool = Executors.newFixedThreadPool(8)
    val latch = CountDownLatch(100)
    val failures = AtomicInteger(0)

    try {
        for (i in 0 until 100) {
            pool.submit {
                try {
                    val out = when (i % 4) {
                        0 -> bridge.dispatch("permissionState", null)
                        1 -> bridge.dispatch("photos.recentScreenshots", """{"limit":4}""")
                        2 -> bridge.dispatch("vision.extractOCR", """{"localId":"media-stress"}""")
                        else -> bridge.dispatch("notify.send", """{"title":"Stress","body":"Ping"}""")
                    }
                    assertTrue(out.isNotEmpty(), "non-empty dispatch")
                } catch (err: Throwable) {
                    err.printStackTrace()
                    failures.incrementAndGet()
                } finally {
                    latch.countDown()
                }
            }
        }
        assertTrue(latch.await(15, TimeUnit.SECONDS), "100 concurrent tasks finish within 15s")
    } finally {
        pool.shutdownNow()
    }

    assertEquals(0, failures.get(), "zero concurrent failures")
    assertEquals(25, deliverer.delivered.size, "25 notifications delivered")
}

fun main() {
    val tests = listOf(
        "testPermissionStateWireParityWithGoAdapter" to ::testPermissionStateWireParityWithGoAdapter,
        "testRecentScreenshotsClampsLimitAndMatchesGoJSONFields" to ::testRecentScreenshotsClampsLimitAndMatchesGoJSONFields,
        "testVisionOCRSuccessEscapingAndRejectsEmptyLocalId" to ::testVisionOCRSuccessEscapingAndRejectsEmptyLocalId,
        "testNotificationBuildsGatePrefixedTagAndRejectsEmptyPayload" to ::testNotificationBuildsGatePrefixedTagAndRejectsEmptyPayload,
        "testCaptureQueueFIFOAtomicDrainAndRingCap" to ::testCaptureQueueFIFOAtomicDrainAndRingCap,
        "testCapabilitiesVoiceAndContactsWireParity" to ::testCapabilitiesVoiceAndContactsWireParity,
        "testUnsupportedMethodFailsClosed" to ::testUnsupportedMethodFailsClosed,
        "testConcurrentDispatchStress100TasksZeroRaces" to ::testConcurrentDispatchStress100TasksZeroRaces,
    )

    val started = System.nanoTime()
    for ((name, fn) in tests) {
        val t0 = System.nanoTime()
        fn()
        val ms = (System.nanoTime() - t0) / 1_000_000.0
        println("PASS $name (%.2f ms)".format(ms))
    }
    val totalMs = (System.nanoTime() - started) / 1_000_000.0
    println("MarlinDeviceBridgeTest: ${tests.size}/${tests.size} passed in %.2f ms".format(totalMs))
}
