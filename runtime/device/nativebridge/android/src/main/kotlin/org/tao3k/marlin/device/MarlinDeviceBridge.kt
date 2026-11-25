// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Pure Kotlin/JVM core bridge paired 1-to-1 with
// runtime/device/nativebridge/contracts.go for `mygo build android` (`//mygo:native`).
// Exposes MediaStore screenshots, Vision/MLKit OCR, NotificationManager
// (Rust GateReceipt-gated), and Share Intent capture queue under the uniform
// `device.android.*` capability namespace.
//
// All Android framework calls live behind thin interfaces (implemented in
// src/androidMain/kotlin/org/tao3k/marlin/device/AndroidSystemProviders.kt),
// allowing full Kotlin compilation, wire-parity verification, and concurrency
// testing on any standard JVM without requiring an Android Emulator.

package org.tao3k.marlin.device

import java.util.Base64
import java.util.UUID

// MARK: - Typed Bridge Errors

sealed class MarlinBridgeException(message: String) : Exception(message) {
    class UnsupportedMethod(val method: String) :
        MarlinBridgeException("nativebridge: unsupported method '$method'")

    class InvalidArgument(val detail: String) :
        MarlinBridgeException("nativebridge: invalid argument: $detail")

    class AssetNotFound(val localId: String) :
        MarlinBridgeException("nativebridge: photo asset not found: $localId")

    class NotificationFailed(val detail: String) :
        MarlinBridgeException("nativebridge: notification delivery failed: $detail")
}

// MARK: - Canonical Wire DTOs (1-to-1 with runtime/device/contracts.go)

enum class OsPermissionStateWire(val wireValue: String) {
    GRANTED("granted"),
    LIMITED("limited"),
    DENIED("denied"),
    PROMPT("prompt"),
    UNSUPPORTED("unsupported"),
}

data class PhotoItemWire(
    val localId: String,
    val createdUnixMs: Long,
    val width: Int,
    val height: Int,
    val isScreenshot: Boolean,
    val mimeType: String,
)

data class NotificationRequestWire(
    val title: String,
    val body: String,
    val tag: String = "marlin.gate.${UUID.randomUUID()}",
)

data class CapturePayloadWire(
    val sourceChannel: String,
    val text: String? = null,
    val ocrText: String? = null,
    val imageBytes: ByteArray? = null,
    val mimeType: String? = null,
    val metadata: Map<String, String>? = null,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is CapturePayloadWire) return false
        return sourceChannel == other.sourceChannel &&
            text == other.text &&
            ocrText == other.ocrText &&
            ((imageBytes == null && other.imageBytes == null) ||
                (imageBytes != null && other.imageBytes != null && imageBytes.contentEquals(other.imageBytes))) &&
            mimeType == other.mimeType &&
            metadata == other.metadata
    }

    override fun hashCode(): Int {
        var result = sourceChannel.hashCode()
        result = 31 * result + (text?.hashCode() ?: 0)
        result = 31 * result + (ocrText?.hashCode() ?: 0)
        result = 31 * result + (imageBytes?.contentHashCode() ?: 0)
        result = 31 * result + (mimeType?.hashCode() ?: 0)
        result = 31 * result + (metadata?.hashCode() ?: 0)
        return result
    }
}

// MARK: - Protocol-Injectable Android OS Boundaries

interface AndroidPhotoLibrary {
    fun permissionState(): OsPermissionStateWire
    fun recentScreenshots(limit: Int): List<PhotoItemWire>
    fun extractTextOCR(localId: String): String
}

interface AndroidNotificationDeliverer {
    fun deliver(request: NotificationRequestWire)
}

interface AndroidCaptureStore {
    fun enqueue(payload: CapturePayloadWire)
    fun drainAll(maxItems: Int = MarlinDeviceBridge.MAX_QUEUED_CAPTURES): List<CapturePayloadWire>
}

class InMemoryCaptureStore : AndroidCaptureStore {
    private val queue = ArrayDeque<CapturePayloadWire>()

    @Synchronized
    override fun enqueue(payload: CapturePayloadWire) {
        val channel = payload.sourceChannel.trim()
        if (channel.isEmpty()) {
            throw MarlinBridgeException.InvalidArgument("sourceChannel must not be empty")
        }
        val normalized = payload.copy(sourceChannel = channel)
        while (queue.size >= MarlinDeviceBridge.MAX_QUEUED_CAPTURES) {
            queue.removeFirst()
        }
        queue.addLast(normalized)
    }

    @Synchronized
    override fun drainAll(maxItems: Int): List<CapturePayloadWire> {
        val cap = maxItems.coerceIn(1, MarlinDeviceBridge.MAX_QUEUED_CAPTURES)
        if (queue.isEmpty()) return emptyList()
        val out = ArrayList<CapturePayloadWire>(minOf(queue.size, cap))
        while (queue.isNotEmpty() && out.size < cap) {
            out.add(queue.removeFirst())
        }
        queue.clear()
        return out
    }
}

// MARK: - MarlinDeviceBridge

class MarlinDeviceBridge(
    private val photos: AndroidPhotoLibrary,
    private val notifications: AndroidNotificationDeliverer,
    private val captureStore: AndroidCaptureStore = InMemoryCaptureStore(),
) {
    companion object {
        const val DEFAULT_SCREENSHOT_LIMIT = 10
        const val MAX_SCREENSHOT_LIMIT = 32
        const val MAX_QUEUED_CAPTURES = 64
        const val MAX_NOTIFICATION_TITLE_CHARS = 160
        const val MAX_NOTIFICATION_BODY_CHARS = 1024

        fun clampScreenshotLimit(limit: Int): Int {
            if (limit <= 0) return DEFAULT_SCREENSHOT_LIMIT
            return minOf(limit, MAX_SCREENSHOT_LIMIT)
        }

        fun normalizeNotification(title: String, body: String): NotificationRequestWire {
            val cleanTitle = title.trim()
            val cleanBody = body.trim()
            if (cleanTitle.isEmpty() && cleanBody.isEmpty()) {
                throw MarlinBridgeException.InvalidArgument("notification title and body cannot both be empty")
            }
            return NotificationRequestWire(
                title = cleanTitle.take(MAX_NOTIFICATION_TITLE_CHARS),
                body = cleanBody.take(MAX_NOTIFICATION_BODY_CHARS),
                tag = "marlin.gate.${UUID.randomUUID()}",
            )
        }
    }

    fun permissionState(): OsPermissionStateWire = photos.permissionState()

    fun recentScreenshots(limit: Int = DEFAULT_SCREENSHOT_LIMIT): List<PhotoItemWire> =
        photos.recentScreenshots(clampScreenshotLimit(limit))

    fun extractTextOCR(localId: String): String {
        val trimmed = localId.trim()
        if (trimmed.isEmpty()) {
            throw MarlinBridgeException.InvalidArgument("localId must not be empty")
        }
        return photos.extractTextOCR(trimmed)
    }

    fun sendNotification(title: String, body: String) {
        val normalized = normalizeNotification(title, body)
        try {
            notifications.deliver(normalized)
        } catch (err: MarlinBridgeException) {
            throw err
        } catch (err: Throwable) {
            throw MarlinBridgeException.NotificationFailed(err.message ?: err.javaClass.simpleName)
        }
    }

    fun enqueueShareCapture(payload: CapturePayloadWire) {
        captureStore.enqueue(payload)
    }

    fun drainPendingCaptures(maxItems: Int = MAX_QUEUED_CAPTURES): List<CapturePayloadWire> =
        captureStore.drainAll(maxItems)

    /** Dispatches a JSON RPC call emitted by Go `runtime/device/nativebridge.Adapter`. */
    fun dispatch(method: String, argsJson: String?): String {
        return when (method) {
            "permissionState" -> {
                val state = permissionState().wireValue
                """{"state":${WireJson.quote(state)}}"""
            }

            "photos.recentScreenshots" -> {
                val limit = if (argsJson.isNullOrBlank()) {
                    DEFAULT_SCREENSHOT_LIMIT
                } else {
                    WireJson.parseFlatObject(argsJson)["limit"]?.toIntOrNull()
                        ?: throw MarlinBridgeException.InvalidArgument("limit must be an integer")
                }
                val items = recentScreenshots(limit)
                val encodedItems = items.joinToString(",") { item ->
                    buildString {
                        append('{')
                        append("\"createdUnixMs\":").append(item.createdUnixMs).append(',')
                        append("\"height\":").append(item.height).append(',')
                        append("\"isScreenshot\":").append(item.isScreenshot).append(',')
                        append("\"localId\":").append(WireJson.quote(item.localId)).append(',')
                        append("\"mimeType\":").append(WireJson.quote(item.mimeType)).append(',')
                        append("\"width\":").append(item.width)
                        append('}')
                    }
                }
                """{"items":[$encodedItems]}"""
            }

            "vision.extractOCR" -> {
                if (argsJson.isNullOrBlank()) {
                    throw MarlinBridgeException.InvalidArgument("vision.extractOCR requires JSON arguments")
                }
                val localId = WireJson.parseFlatObject(argsJson)["localId"]
                    ?: throw MarlinBridgeException.InvalidArgument("localId must not be empty")
                val text = extractTextOCR(localId)
                """{"text":${WireJson.quote(text)}}"""
            }

            "notify.send" -> {
                if (argsJson.isNullOrBlank()) {
                    throw MarlinBridgeException.InvalidArgument("notify.send requires JSON arguments")
                }
                val fields = WireJson.parseFlatObject(argsJson)
                val title = fields["title"] ?: ""
                val body = fields["body"] ?: ""
                sendNotification(title, body)
                """{"ok":true}"""
            }

            "capture.drain" -> {
                val captures = drainPendingCaptures()
                val encoded = captures.joinToString(",") { c ->
                    val parts = ArrayList<String>(6)
                    if (c.imageBytes != null && c.imageBytes.isNotEmpty()) {
                        val b64 = Base64.getEncoder().encodeToString(c.imageBytes)
                        parts.add("\"imageBytes\":${WireJson.quote(b64)}")
                    }
                    if (!c.metadata.isNullOrEmpty()) {
                        val meta = c.metadata.entries.sortedBy { it.key }.joinToString(",") { (k, v) ->
                            "${WireJson.quote(k)}:${WireJson.quote(v)}"
                        }
                        parts.add("\"metadata\":{$meta}")
                    }
                    if (!c.mimeType.isNullOrEmpty()) {
                        parts.add("\"mimeType\":${WireJson.quote(c.mimeType)}")
                    }
                    if (!c.ocrText.isNullOrEmpty()) {
                        parts.add("\"ocrText\":${WireJson.quote(c.ocrText)}")
                    }
                    parts.add("\"sourceChannel\":${WireJson.quote(c.sourceChannel)}")
                    if (!c.text.isNullOrEmpty()) {
                        parts.add("\"text\":${WireJson.quote(c.text)}")
                    }
                    "{${parts.joinToString(",")}}"
                }
                """{"captures":[$encoded]}"""
            }

            else -> throw MarlinBridgeException.UnsupportedMethod(method)
        }
    }
}

// MARK: - Zero-Dependency Deterministic Wire JSON Codec

internal object WireJson {
    fun quote(value: String): String {
        val sb = StringBuilder(value.length + 2)
        sb.append('"')
        for (ch in value) {
            when (ch) {
                '"' -> sb.append("\\\"")
                '\\' -> sb.append("\\\\")
                '\b' -> sb.append("\\b")
                '\u000C' -> sb.append("\\f")
                '\n' -> sb.append("\\n")
                '\r' -> sb.append("\\r")
                '\t' -> sb.append("\\t")
                else -> {
                    if (ch.code < 0x20) {
                        sb.append("\\u%04x".format(ch.code))
                    } else {
                        sb.append(ch)
                    }
                }
            }
        }
        sb.append('"')
        return sb.toString()
    }

    /** Parses a flat JSON object (`{"k":"v","n":10}`) produced by Go `nativebridge.Adapter`. */
    fun parseFlatObject(raw: String): Map<String, String> {
        val s = raw.trim()
        if (s.length < 2 || s.first() != '{' || s.last() != '}') {
            throw MarlinBridgeException.InvalidArgument("expected JSON object")
        }
        val out = LinkedHashMap<String, String>()
        var i = 1
        val end = s.length - 1

        fun skipWs() {
            while (i < end && s[i].isWhitespace()) i++
        }

        fun readString(): String {
            if (i >= end || s[i] != '"') {
                throw MarlinBridgeException.InvalidArgument("expected JSON string at index $i")
            }
            i++
            val sb = StringBuilder()
            while (i < end) {
                val ch = s[i++]
                if (ch == '"') return sb.toString()
                if (ch == '\\') {
                    if (i >= end) throw MarlinBridgeException.InvalidArgument("unterminated escape")
                    when (val esc = s[i++]) {
                        '"', '\\', '/' -> sb.append(esc)
                        'b' -> sb.append('\b')
                        'f' -> sb.append('\u000C')
                        'n' -> sb.append('\n')
                        'r' -> sb.append('\r')
                        't' -> sb.append('\t')
                        'u' -> {
                            if (i + 4 > end) throw MarlinBridgeException.InvalidArgument("invalid unicode escape")
                            val code = s.substring(i, i + 4).toIntOrNull(16)
                                ?: throw MarlinBridgeException.InvalidArgument("invalid unicode hex")
                            sb.append(code.toChar())
                            i += 4
                        }
                        else -> throw MarlinBridgeException.InvalidArgument("unsupported escape \\$esc")
                    }
                } else {
                    sb.append(ch)
                }
            }
            throw MarlinBridgeException.InvalidArgument("unterminated JSON string")
        }

        fun readPrimitive(): String {
            val start = i
            while (i < end && s[i] != ',' && !s[i].isWhitespace()) i++
            return s.substring(start, i)
        }

        skipWs()
        while (i < end) {
            val key = readString()
            skipWs()
            if (i >= end || s[i] != ':') {
                throw MarlinBridgeException.InvalidArgument("expected ':' after key '$key'")
            }
            i++
            skipWs()
            val value = if (i < end && s[i] == '"') readString() else readPrimitive()
            out[key] = value
            skipWs()
            if (i < end) {
                if (s[i] != ',') throw MarlinBridgeException.InvalidArgument("expected ',' at index $i")
                i++
                skipWs()
            }
        }
        return out
    }
}
