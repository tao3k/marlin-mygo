// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Thin real Android SDK adapter implementing AndroidPhotoLibrary and
// AndroidNotificationDeliverer against standard `android.*` framework APIs
// (API 26+ / Android 8.0+ through Android 15+). Compiled directly against
// `$ANDROID_HOME/platforms/android-*/android.jar` in CI with zero Emulator or
// third-party Gradle/Maven downloads required.

package org.tao3k.marlin.device

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.provider.ContactsContract
import android.provider.MediaStore
import android.speech.SpeechRecognizer
import java.util.Locale

class SystemAndroidPhotoLibrary(
    private val context: Context,
    private val ocrEngine: (localId: String) -> String = { localId ->
        throw MarlinBridgeException.AssetNotFound(localId)
    },
) : AndroidPhotoLibrary {
    override fun permissionState(): OsPermissionStateWire {
        val permission = if (Build.VERSION.SDK_INT >= 33) {
            Manifest.permission.READ_MEDIA_IMAGES
        } else {
            Manifest.permission.READ_EXTERNAL_STORAGE
        }
        val granted = context.checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED
        return if (granted) OsPermissionStateWire.GRANTED else OsPermissionStateWire.PROMPT
    }

    override fun recentScreenshots(limit: Int): List<PhotoItemWire> {
        val bounded = MarlinDeviceBridge.clampScreenshotLimit(limit)
        val projection = arrayOf(
            MediaStore.Images.Media._ID,
            MediaStore.Images.Media.DATE_ADDED,
            MediaStore.Images.Media.WIDTH,
            MediaStore.Images.Media.HEIGHT,
            MediaStore.Images.Media.MIME_TYPE,
            MediaStore.Images.Media.BUCKET_DISPLAY_NAME,
        )
        val selection = "${MediaStore.Images.Media.BUCKET_DISPLAY_NAME} LIKE ?"
        val selectionArgs = arrayOf("%Screenshot%")
        val sortOrder = "${MediaStore.Images.Media.DATE_ADDED} DESC"

        val out = ArrayList<PhotoItemWire>(bounded)
        context.contentResolver.query(
            MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
            projection,
            selection,
            selectionArgs,
            sortOrder,
        )?.use { cursor ->
            val idIdx = cursor.getColumnIndexOrThrow(MediaStore.Images.Media._ID)
            val dateIdx = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.DATE_ADDED)
            val widthIdx = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.WIDTH)
            val heightIdx = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.HEIGHT)
            val mimeIdx = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.MIME_TYPE)

            while (cursor.moveToNext() && out.size < bounded) {
                val id = cursor.getLong(idIdx).toString()
                val createdUnixMs = cursor.getLong(dateIdx) * 1000L
                val width = cursor.getInt(widthIdx)
                val height = cursor.getInt(heightIdx)
                val mime = cursor.getString(mimeIdx) ?: "image/png"
                out.add(
                    PhotoItemWire(
                        localId = id,
                        createdUnixMs = createdUnixMs,
                        width = width,
                        height = height,
                        isScreenshot = true,
                        mimeType = mime,
                    ),
                )
            }
        }
        return out
    }

    override fun extractTextOCR(localId: String): String = ocrEngine(localId)
}

class SystemAndroidNotificationDeliverer(
    private val context: Context,
    private val channelId: String = "marlin.gated.notifications",
    private val channelName: String = "Marlin Gated Notifications",
) : AndroidNotificationDeliverer {
    override fun deliver(request: NotificationRequestWire) {
        val manager = context.getSystemService(NotificationManager::class.java)
            ?: throw MarlinBridgeException.NotificationFailed("NotificationManager unavailable")

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            if (manager.getNotificationChannel(channelId) == null) {
                val channel = NotificationChannel(
                    channelId,
                    channelName,
                    NotificationManager.IMPORTANCE_HIGH,
                )
                manager.createNotificationChannel(channel)
            }
        }

        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(context, channelId)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(context)
        }

        val notification = builder
            .setContentTitle(request.title)
            .setContentText(request.body)
            .setAutoCancel(true)
            .build()

        manager.notify(request.tag, request.tag.hashCode(), notification)
    }
}

class SystemAndroidContactsProvider(
    private val context: Context,
) : AndroidContactsProvider {
    override fun searchContacts(query: String, limit: Int): List<ContactSummaryWire> {
        val bounded = if (limit <= 0) 10 else minOf(limit, MarlinDeviceBridge.MAX_CONTACTS_LIMIT)
        val granted = context.checkSelfPermission(Manifest.permission.READ_CONTACTS) == PackageManager.PERMISSION_GRANTED
        if (!granted) return emptyList()

        val projection = arrayOf(
            ContactsContract.CommonDataKinds.Email.CONTACT_ID,
            ContactsContract.CommonDataKinds.Email.DISPLAY_NAME_PRIMARY,
            ContactsContract.CommonDataKinds.Email.ADDRESS,
        )
        val trimmed = query.trim()
        val selection = if (trimmed.isEmpty()) {
            null
        } else {
            "${ContactsContract.CommonDataKinds.Email.DISPLAY_NAME_PRIMARY} LIKE ?"
        }
        val selectionArgs = if (trimmed.isEmpty()) null else arrayOf("%$trimmed%")

        val out = ArrayList<ContactSummaryWire>(bounded)
        context.contentResolver.query(
            ContactsContract.CommonDataKinds.Email.CONTENT_URI,
            projection,
            selection,
            selectionArgs,
            "${ContactsContract.CommonDataKinds.Email.DISPLAY_NAME_PRIMARY} ASC",
        )?.use { cursor ->
            val idIdx = cursor.getColumnIndexOrThrow(ContactsContract.CommonDataKinds.Email.CONTACT_ID)
            val nameIdx = cursor.getColumnIndexOrThrow(ContactsContract.CommonDataKinds.Email.DISPLAY_NAME_PRIMARY)
            val addrIdx = cursor.getColumnIndexOrThrow(ContactsContract.CommonDataKinds.Email.ADDRESS)
            while (cursor.moveToNext() && out.size < bounded) {
                val id = cursor.getString(idIdx) ?: continue
                val name = cursor.getString(nameIdx) ?: id
                val addr = cursor.getString(addrIdx)
                out.add(ContactSummaryWire(contactId = id, displayName = name, handle = addr))
            }
        }
        return out
    }
}

class SystemAndroidVoiceProvider(
    private val context: Context,
) : AndroidVoiceProvider {
    @Volatile
    private var latest: VoiceTranscriptWire = VoiceTranscriptWire(
        transcriptId = "",
        locale = Locale.getDefault().toLanguageTag(),
        text = "",
        durationMs = 0L,
    )

    fun isRecognitionAvailable(): Boolean {
        val granted = context.checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED
        return granted && SpeechRecognizer.isRecognitionAvailable(context)
    }

    fun recordTranscript(transcript: VoiceTranscriptWire) {
        latest = transcript
    }

    override fun latestTranscript(): VoiceTranscriptWire = latest
}
