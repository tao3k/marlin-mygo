// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Real Kotlin JVM probe driven by Go `runtime/device/nativebridge`
// cross-language E2E tests. Reads newline-delimited JSON RPC frames on stdin,
// dispatches them through the real Kotlin MarlinDeviceBridge, and writes JSON
// responses on stdout — proving 100% live Go <-> Kotlin wire parity with zero Emulator.

package org.tao3k.marlin.device

import java.io.BufferedReader
import java.io.InputStreamReader

private class ProbeAndroidPhotos : AndroidPhotoLibrary {
    override fun permissionState(): OsPermissionStateWire = OsPermissionStateWire.GRANTED

    override fun recentScreenshots(limit: Int): List<PhotoItemWire> =
        listOf(
            PhotoItemWire(
                localId = "and-media-1",
                createdUnixMs = 1_764_000_000_000L,
                width = 1080,
                height = 2400,
                isScreenshot = true,
                mimeType = "image/png",
            ),
        ).take(limit)

    override fun extractTextOCR(localId: String): String {
        if (localId != "and-media-1") throw MarlinBridgeException.AssetNotFound(localId)
        return "Kotlin OCR: Android Share Intent verified"
    }
}

private class ProbeAndroidVoice : AndroidVoiceProvider {
    override fun latestTranscript(): VoiceTranscriptWire =
        VoiceTranscriptWire(
            transcriptId = "and-voice-1",
            locale = "en-US",
            text = "Sync workspace diff to phone",
            durationMs = 2850L,
        )
}

private class ProbeAndroidContacts : AndroidContactsProvider {
    private val all = listOf(
        ContactSummaryWire("ac-1", "Ada Lovelace", "ada@tao3k.org"),
        ContactSummaryWire("ac-2", "Grace Hopper", "grace@tao3k.org"),
    )

    override fun searchContacts(query: String, limit: Int): List<ContactSummaryWire> {
        val q = query.lowercase()
        val matched = if (q.isEmpty()) all else all.filter { it.displayName.lowercase().contains(q) }
        return matched.take(limit)
    }
}

private class ProbeAndroidNotifications : AndroidNotificationDeliverer {
    override fun deliver(request: NotificationRequestWire) {
        // Accepted by probe harness.
    }
}

fun main() {
    val bridge = MarlinDeviceBridge(
        photos = ProbeAndroidPhotos(),
        notifications = ProbeAndroidNotifications(),
        voice = ProbeAndroidVoice(),
        contacts = ProbeAndroidContacts(),
    )
    bridge.enqueueShareCapture(
        CapturePayloadWire(
            sourceChannel = "android.share-intent",
            text = "Captured from Android Share Intent",
            metadata = mapOf("origin" to "chrome"),
        ),
    )

    val reader = BufferedReader(InputStreamReader(System.`in`))
    while (true) {
        val line = reader.readLine() ?: break
        val trimmed = line.trim()
        if (trimmed.isEmpty()) continue
        try {
            val req = WireJson.parseFlatObject(trimmed)
            val method = req["method"] ?: throw MarlinBridgeException.InvalidArgument("missing method")
            val args = req["args"]
            val out = bridge.dispatch(method, if (args.isNullOrEmpty()) null else args)
            println("""{"ok":true,"data":${WireJson.quote(out)}}""")
        } catch (err: Throwable) {
            println("""{"ok":false,"error":${WireJson.quote(err.message ?: err.javaClass.simpleName)}}""")
        }
        System.out.flush()
    }
}
