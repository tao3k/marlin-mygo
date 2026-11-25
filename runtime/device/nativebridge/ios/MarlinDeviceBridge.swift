// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Reference iOS Swift bridge paired with runtime/device/nativebridge/contracts.go
// for `mygo build ios` (`//mygo:native`). Exposes PhotoKit/Vision OCR,
// UNUserNotificationCenter (GateReceipt-gated), and App Group Share Extension
// capture queue under the uniform `device.ios.*` namespace.

import Foundation
import Photos
import UserNotifications
import Vision

public final class MarlinDeviceBridge {
    private let appGroupID: String

    public init(appGroupID: String = "group.org.tao3k.marlin") {
        self.appGroupID = appGroupID
    }

    public func permissionState() -> String {
        switch PHPhotoLibrary.authorizationStatus(for: .readWrite) {
        case .authorized: return "granted"
        case .limited: return "limited"
        case .denied, .restricted: return "denied"
        case .notDetermined: return "prompt"
        @unknown default: return "unsupported"
        }
    }

    public func sendNotification(title: String, body: String, completion: @escaping (Error?) -> Void) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        let req = UNNotificationRequest(
            identifier: "marlin.gate.\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        UNUserNotificationCenter.current().add(req, withCompletionHandler: completion)
    }

    public func drainShareExtensionQueue() -> [[String: Any]] {
        guard let defaults = UserDefaults(suiteName: appGroupID),
              let queued = defaults.array(forKey: "marlin.pending.captures") as? [[String: Any]]
        else {
            return []
        }
        defaults.removeObject(forKey: "marlin.pending.captures")
        return queued
    }
}
