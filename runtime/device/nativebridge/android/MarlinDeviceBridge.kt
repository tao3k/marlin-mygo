// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Reference Android Kotlin bridge paired with runtime/device/nativebridge/contracts.go
// for `mygo build android` (`//mygo:native`). Exposes MediaStore screenshots,
// NotificationManagerCompat (GateReceipt-gated), and Share Intent queue under
// the uniform `device.android.*` namespace.

package org.tao3k.marlin.device

import android.content.Context
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat

class MarlinDeviceBridge(private val context: Context) {
    private val pendingCaptures = ArrayDeque<Map<String, Any>>()

    fun permissionState(permission: String): String {
        val granted = ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
        return if (granted) "granted" else "prompt"
    }

    fun sendNotification(channelId: String, title: String, body: String) {
        val notification = NotificationCompat.Builder(context, channelId)
            .setContentTitle(title)
            .setContentText(body)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(context).notify(title.hashCode(), notification)
    }

    @Synchronized
    fun enqueueShareCapture(sourceChannel: String, text: String) {
        pendingCaptures.addLast(mapOf("sourceChannel" to sourceChannel, "text" to text))
    }

    @Synchronized
    fun drainPendingCaptures(): List<Map<String, Any>> {
        val out = pendingCaptures.toList()
        pendingCaptures.clear()
        return out
    }
}
