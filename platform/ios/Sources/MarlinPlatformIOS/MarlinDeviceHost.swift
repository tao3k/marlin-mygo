// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import Foundation
#if canImport(Photos)
import Photos
#endif
#if canImport(Vision)
import Vision
#endif
#if canImport(Speech)
import Speech
#endif
#if canImport(UserNotifications)
import UserNotifications
#endif

/// Wire-compatible mirrors of runtime/device/contracts.go for the platform.ios
/// capability adapter (device.ios.photos.recent, device.ios.ocr,
/// device.ios.voice.transcribe, device.ios.share.drain, device.ios.notify.send).
public enum MarlinOSPermissionState: String, Codable, Sendable {
    case granted
    case limited
    case prompt
    case denied
    case unsupported
}

public struct MarlinPhotoItem: Codable, Sendable {
    public let localId: String
    public let createdUnixMs: Int64
    public let width: Int
    public let height: Int
    public let isScreenshot: Bool
    public let mimeType: String
}

public struct MarlinVoiceTranscript: Codable, Sendable {
    public let transcriptId: String
    public let locale: String
    public let text: String
    public let durationMs: Int64
}

public struct MarlinCapturePayload: Codable, Sendable {
    public let sourceChannel: String
    public let text: String?
    public let ocrText: String?
    public let mimeType: String?
    public let metadata: [String: String]?
}

/// platform.ios host adapter bound into marlin-mygo's runtime/device.Bridge.
/// Never exposed directly to third-party Marketplace plugins; every mutating
/// call is gated upstream by Rust's one-shot GateReceipt before crossing FFI.
public final class MarlinIOSDeviceAdapter: @unchecked Sendable {
    private let appGroupID: String
    private let captureQueueFileName = "marlin-capture-queue.json"

    public init(appGroupID: String = "group.org.tao3k.marlin") {
        self.appGroupID = appGroupID
    }

    public func photosPermissionState() -> MarlinOSPermissionState {
        #if canImport(Photos)
        switch PHPhotoLibrary.authorizationStatus(for: .readWrite) {
        case .authorized:
            return .granted
        case .limited:
            return .limited
        case .notDetermined:
            return .prompt
        case .denied, .restricted:
            return .denied
        @unknown default:
            return .unsupported
        }
        #else
        return .unsupported
        #endif
    }

    public func speechPermissionState() -> MarlinOSPermissionState {
        #if canImport(Speech)
        switch SFSpeechRecognizer.authorizationStatus() {
        case .authorized:
            return .granted
        case .notDetermined:
            return .prompt
        case .denied, .restricted:
            return .denied
        @unknown default:
            return .unsupported
        }
        #else
        return .unsupported
        #endif
    }

    /// Reads bounded screenshot metadata matching device.ios.photos.recent.
    public func recentScreenshots(limit: Int) -> [MarlinPhotoItem] {
        #if canImport(Photos)
        let status = photosPermissionState()
        guard status == .granted || status == .limited else { return [] }
        let bounded = max(1, min(limit, 50))
        let opts = PHFetchOptions()
        opts.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        opts.fetchLimit = bounded
        opts.predicate = NSPredicate(
            format: "(mediaSubtype & %d) != 0",
            PHAssetMediaSubtype.photoScreenshot.rawValue
        )
        let fetched = PHAsset.fetchAssets(with: .image, options: opts)
        var out: [MarlinPhotoItem] = []
        out.reserveCapacity(fetched.count)
        fetched.enumerateObjects { asset, _, _ in
            let ts = Int64((asset.creationDate?.timeIntervalSince1970 ?? 0) * 1000)
            out.append(MarlinPhotoItem(
                localId: asset.localIdentifier,
                createdUnixMs: ts,
                width: asset.pixelWidth,
                height: asset.pixelHeight,
                isScreenshot: true,
                mimeType: "image/png"
            ))
        }
        return out
        #else
        return []
        #endif
    }

    /// Drains proactive captures staged by iOS Share Extension or Back-Tap AppIntent
    /// inside the signed App Group container (device.ios.share.drain).
    public func drainPendingCaptures() -> [MarlinCapturePayload] {
        guard let containerURL = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: appGroupID
        ) else {
            return []
        }
        let queueURL = containerURL.appendingPathComponent(captureQueueFileName)
        guard let raw = try? Data(contentsOf: queueURL),
              let items = try? JSONDecoder().decode([MarlinCapturePayload].self, from: raw) else {
            return []
        }
        try? FileManager.default.removeItem(at: queueURL)
        return items
    }

    /// Delivers a gated notification (device.ios.notify.send) ONLY after Rust's
    /// ApprovalGateService minted and runtime/device.Bridge consumed a one-shot GateReceipt.
    public func sendNotification(title: String, body: String) async throws {
        #if canImport(UserNotifications)
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        let request = UNNotificationRequest(
            identifier: UUID().uuidString,
            content: content,
            trigger: nil
        )
        try await UNUserNotificationCenter.current().add(request)
        #endif
    }
}
