// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Native Apple (iOS / macOS) Swift bridge paired 1-to-1 with
// runtime/device/nativebridge/contracts.go for `mygo build ios` (`//mygo:native`).
// Exposes PhotoKit screenshots, Vision on-device OCR, UNUserNotificationCenter
// (Rust GateReceipt-gated), and App Group Share Extension capture queue under
// the uniform `device.ios.*` capability namespace.
//
// All OS boundaries are abstracted behind thin Sendable protocols defaulting to
// real Apple frameworks (PHPhotoLibrary, Vision, UNUserNotificationCenter,
// UserDefaults App Group), enabling deterministic `swift test` execution on
// macOS hosts and CI without requiring an iOS Simulator runtime image.

import Contacts
import Foundation
import Photos
import UserNotifications
import Vision

// MARK: - Typed Bridge Errors

public enum MarlinBridgeError: Error, Equatable, LocalizedError, Sendable {
    case unsupportedMethod(String)
    case invalidArgument(String)
    case assetNotFound(String)
    case notificationFailed(String)
    case ocrFailed(String)

    public var errorDescription: String? {
        switch self {
        case .unsupportedMethod(let method):
            return "nativebridge: unsupported method '\(method)'"
        case .invalidArgument(let detail):
            return "nativebridge: invalid argument: \(detail)"
        case .assetNotFound(let localID):
            return "nativebridge: photo asset not found: \(localID)"
        case .notificationFailed(let detail):
            return "nativebridge: notification delivery failed: \(detail)"
        case .ocrFailed(let detail):
            return "nativebridge: Vision OCR failed: \(detail)"
        }
    }
}

// MARK: - Canonical Wire DTOs (1-to-1 with runtime/device/contracts.go)

public enum OsPermissionStateWire: String, Codable, Equatable, Sendable {
    case granted
    case limited
    case denied
    case prompt
    case unsupported
}

public struct PermissionStateResponse: Codable, Equatable, Sendable {
    public let state: OsPermissionStateWire

    public init(state: OsPermissionStateWire) {
        self.state = state
    }
}

public struct RecentScreenshotsRequest: Codable, Equatable, Sendable {
    public let limit: Int

    public init(limit: Int) {
        self.limit = limit
    }
}

public struct PhotoItemWire: Codable, Equatable, Sendable {
    public let localId: String
    public let createdUnixMs: Int64
    public let width: Int
    public let height: Int
    public let isScreenshot: Bool
    public let mimeType: String

    public init(
        localId: String,
        createdUnixMs: Int64,
        width: Int,
        height: Int,
        isScreenshot: Bool,
        mimeType: String
    ) {
        self.localId = localId
        self.createdUnixMs = createdUnixMs
        self.width = width
        self.height = height
        self.isScreenshot = isScreenshot
        self.mimeType = mimeType
    }
}

public struct RecentScreenshotsResponse: Codable, Equatable, Sendable {
    public let items: [PhotoItemWire]

    public init(items: [PhotoItemWire]) {
        self.items = items
    }
}

public struct ExtractOCRRequest: Codable, Equatable, Sendable {
    public let localId: String

    public init(localId: String) {
        self.localId = localId
    }
}

public struct ExtractOCRResponse: Codable, Equatable, Sendable {
    public let text: String

    public init(text: String) {
        self.text = text
    }
}

public struct NotificationRequestWire: Codable, Equatable, Sendable {
    public let title: String
    public let body: String

    public init(title: String, body: String) {
        self.title = title
        self.body = body
    }
}

public struct AckResponse: Codable, Equatable, Sendable {
    public let ok: Bool

    public init(ok: Bool = true) {
        self.ok = ok
    }
}

public struct CapturePayloadWire: Codable, Equatable, Sendable {
    public let sourceChannel: String
    public let text: String?
    public let ocrText: String?
    public let imageBytes: Data?
    public let mimeType: String?
    public let metadata: [String: String]?

    public init(
        sourceChannel: String,
        text: String? = nil,
        ocrText: String? = nil,
        imageBytes: Data? = nil,
        mimeType: String? = nil,
        metadata: [String: String]? = nil
    ) {
        self.sourceChannel = sourceChannel
        self.text = text
        self.ocrText = ocrText
        self.imageBytes = imageBytes
        self.mimeType = mimeType
        self.metadata = metadata
    }
}

public struct DrainCapturesResponse: Codable, Equatable, Sendable {
    public let captures: [CapturePayloadWire]

    public init(captures: [CapturePayloadWire]) {
        self.captures = captures
    }
}

public struct VoiceTranscriptWire: Codable, Equatable, Sendable {
    public let transcriptId: String
    public let locale: String
    public let text: String
    public let durationMs: Int64

    public init(transcriptId: String, locale: String, text: String, durationMs: Int64) {
        self.transcriptId = transcriptId
        self.locale = locale
        self.text = text
        self.durationMs = durationMs
    }
}

public struct ContactSearchRequest: Codable, Equatable, Sendable {
    public let query: String
    public let limit: Int

    public init(query: String, limit: Int = 10) {
        self.query = query
        self.limit = limit
    }
}

public struct ContactSummaryWire: Codable, Equatable, Sendable {
    public let contactId: String
    public let displayName: String
    public let handle: String?

    public init(contactId: String, displayName: String, handle: String? = nil) {
        self.contactId = contactId
        self.displayName = displayName
        self.handle = handle
    }
}

public struct ContactSearchResponse: Codable, Equatable, Sendable {
    public let contacts: [ContactSummaryWire]

    public init(contacts: [ContactSummaryWire]) {
        self.contacts = contacts
    }
}

public struct BridgeCapabilitiesWire: Codable, Equatable, Sendable {
    public let platform: String
    public let families: [String]
    public let methods: [String]

    public init(platform: String, families: [String], methods: [String]) {
        self.platform = platform
        self.families = families
        self.methods = methods
    }
}

// MARK: - Protocol-Injectable OS Boundaries

public protocol PhotoLibraryProviding: Sendable {
    func authorizationStatus() -> PHAuthorizationStatus
    func recentScreenshots(limit: Int) throws -> [PhotoItemWire]
    func extractTextOCR(localID: String) throws -> String
}

public protocol VoiceCaptureProviding: Sendable {
    func latestTranscript() throws -> VoiceTranscriptWire
}

public protocol ContactsProviding: Sendable {
    func searchContacts(query: String, limit: Int) throws -> [ContactSummaryWire]
}

public struct EmptyVoiceCaptureProvider: VoiceCaptureProviding {
    public init() {}
    public func latestTranscript() throws -> VoiceTranscriptWire {
        VoiceTranscriptWire(transcriptId: "", locale: "en-US", text: "", durationMs: 0)
    }
}

public struct EmptyContactsProvider: ContactsProviding {
    public init() {}
    public func searchContacts(query: String, limit: Int) throws -> [ContactSummaryWire] {
        []
    }
}

public protocol NotificationDelivering: Sendable {
    func deliver(request: UNNotificationRequest, completion: @escaping @Sendable (Error?) -> Void)
}

public protocol CaptureQueueStoring: Sendable {
    func enqueue(_ payload: CapturePayloadWire) throws
    func drainAll(maxItems: Int) -> [CapturePayloadWire]
}

// MARK: - Real Apple Framework Implementations

public struct SystemPhotoLibraryProvider: PhotoLibraryProviding {
    public init() {}

    public func authorizationStatus() -> PHAuthorizationStatus {
        PHPhotoLibrary.authorizationStatus(for: .readWrite)
    }

    public func recentScreenshots(limit: Int) throws -> [PhotoItemWire] {
        let boundedLimit = MarlinDeviceBridge.clampScreenshotLimit(limit)
        let options = PHFetchOptions()
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        options.predicate = NSPredicate(
            format: "(mediaSubtype & %d) != 0",
            PHAssetMediaSubtype.photoScreenshot.rawValue
        )
        options.fetchLimit = boundedLimit

        let fetched = PHAsset.fetchAssets(with: .image, options: options)
        var items: [PhotoItemWire] = []
        items.reserveCapacity(min(fetched.count, boundedLimit))
        fetched.enumerateObjects { asset, _, stop in
            if items.count >= boundedLimit {
                stop.pointee = true
                return
            }
            let createdMs = Int64((asset.creationDate?.timeIntervalSince1970 ?? 0) * 1000.0)
            items.append(
                PhotoItemWire(
                    localId: asset.localIdentifier,
                    createdUnixMs: createdMs,
                    width: asset.pixelWidth,
                    height: asset.pixelHeight,
                    isScreenshot: asset.mediaSubtypes.contains(.photoScreenshot),
                    mimeType: "image/png"
                )
            )
        }
        return items
    }

    public func extractTextOCR(localID: String) throws -> String {
        let trimmed = localID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw MarlinBridgeError.invalidArgument("localId must not be empty")
        }
        let fetched = PHAsset.fetchAssets(withLocalIdentifiers: [trimmed], options: nil)
        guard let asset = fetched.firstObject else {
            throw MarlinBridgeError.assetNotFound(trimmed)
        }

        let options = PHImageRequestOptions()
        options.isSynchronous = true
        options.deliveryMode = .highQualityFormat
        options.isNetworkAccessAllowed = false

        var imageData: Data?
        PHImageManager.default().requestImageDataAndOrientation(for: asset, options: options) { data, _, _, _ in
            imageData = data
        }
        guard let data = imageData, !data.isEmpty else {
            throw MarlinBridgeError.ocrFailed("unable to load local image bytes for asset \(trimmed)")
        }
        return try Self.recognizeText(fromImageData: data)
    }

    /// Runs on-device Apple Vision text recognition on raw encoded image bytes (PNG/JPEG).
    public static func recognizeText(fromImageData data: Data) throws -> String {
        guard !data.isEmpty else {
            throw MarlinBridgeError.invalidArgument("image data must not be empty")
        }
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.usesLanguageCorrection = true

        let handler = VNImageRequestHandler(data: data, options: [:])
        do {
            try handler.perform([request])
        } catch {
            throw MarlinBridgeError.ocrFailed(error.localizedDescription)
        }
        let observations = request.results ?? []
        let lines = observations.compactMap { $0.topCandidates(1).first?.string }
        return lines.joined(separator: "\n")
    }
}

public struct SystemNotificationDeliverer: NotificationDelivering {
    public init() {}

    public func deliver(request: UNNotificationRequest, completion: @escaping @Sendable (Error?) -> Void) {
        UNUserNotificationCenter.current().add(request, withCompletionHandler: completion)
    }
}

/// Real Apple Contacts framework adapter returning PII-bounded ContactSummaryWire records.
public struct SystemContactsProvider: ContactsProviding {
    public init() {}

    public func searchContacts(query: String, limit: Int) throws -> [ContactSummaryWire] {
        let bounded = limit <= 0 ? 10 : min(limit, MarlinDeviceBridge.maxContactsLimit)
        let store = CNContactStore()
        let keys: [CNKeyDescriptor] = [
            CNContactIdentifierKey as CNKeyDescriptor,
            CNContactGivenNameKey as CNKeyDescriptor,
            CNContactFamilyNameKey as CNKeyDescriptor,
            CNContactEmailAddressesKey as CNKeyDescriptor,
        ]
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let matched: [CNContact]
        if trimmed.isEmpty {
            var collected: [CNContact] = []
            let req = CNContactFetchRequest(keysToFetch: keys)
            try store.enumerateContacts(with: req) { contact, stop in
                collected.append(contact)
                if collected.count >= bounded {
                    stop.pointee = true
                }
            }
            matched = collected
        } else {
            let predicate = CNContact.predicateForContacts(matchingName: trimmed)
            matched = Array(try store.unifiedContacts(matching: predicate, keysToFetch: keys).prefix(bounded))
        }
        return matched.map { contact in
            let fullName = [contact.givenName, contact.familyName]
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
                .joined(separator: " ")
            let email = contact.emailAddresses.first.map { String($0.value) }
            return ContactSummaryWire(
                contactId: contact.identifier,
                displayName: fullName.isEmpty ? (email ?? contact.identifier) : fullName,
                handle: email
            )
        }
    }
}

public final class AppGroupCaptureStore: CaptureQueueStoring, @unchecked Sendable {
    public static let queueKey = "marlin.pending.captures"
    public static let maxQueuedItems = 64

    private let suiteName: String
    private let lock = NSLock()
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(suiteName: String = "group.org.tao3k.marlin") {
        self.suiteName = suiteName
    }

    public func enqueue(_ payload: CapturePayloadWire) throws {
        let channel = payload.sourceChannel.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !channel.isEmpty else {
            throw MarlinBridgeError.invalidArgument("sourceChannel must not be empty")
        }
        let normalized = CapturePayloadWire(
            sourceChannel: channel,
            text: payload.text,
            ocrText: payload.ocrText,
            imageBytes: payload.imageBytes,
            mimeType: payload.mimeType,
            metadata: payload.metadata
        )
        let encoded = try encoder.encode(normalized)

        lock.lock()
        defer { lock.unlock() }
        guard let defaults = UserDefaults(suiteName: suiteName) else {
            return
        }
        var existing = defaults.array(forKey: Self.queueKey) as? [Data] ?? []
        if existing.count >= Self.maxQueuedItems {
            existing.removeFirst(existing.count - Self.maxQueuedItems + 1)
        }
        existing.append(encoded)
        defaults.set(existing, forKey: Self.queueKey)
    }

    public func drainAll(maxItems: Int = maxQueuedItems) -> [CapturePayloadWire] {
        let cap = max(1, min(maxItems, Self.maxQueuedItems))
        lock.lock()
        defer { lock.unlock() }
        guard let defaults = UserDefaults(suiteName: suiteName),
              let rawList = defaults.array(forKey: Self.queueKey)
        else {
            return []
        }
        defaults.removeObject(forKey: Self.queueKey)

        var out: [CapturePayloadWire] = []
        out.reserveCapacity(min(rawList.count, cap))
        for entry in rawList {
            if out.count >= cap {
                break
            }
            if let blob = entry as? Data,
               let decoded = try? decoder.decode(CapturePayloadWire.self, from: blob),
               !decoded.sourceChannel.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            {
                out.append(decoded)
            } else if let dict = entry as? [String: Any],
                      let parsed = Self.decodeLegacyDictionary(dict)
            {
                out.append(parsed)
            }
        }
        return out
    }

    private static func decodeLegacyDictionary(_ dict: [String: Any]) -> CapturePayloadWire? {
        guard let rawChannel = dict["sourceChannel"] as? String else {
            return nil
        }
        let channel = rawChannel.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !channel.isEmpty else {
            return nil
        }
        return CapturePayloadWire(
            sourceChannel: channel,
            text: dict["text"] as? String,
            ocrText: dict["ocrText"] as? String,
            imageBytes: dict["imageBytes"] as? Data,
            mimeType: dict["mimeType"] as? String,
            metadata: dict["metadata"] as? [String: String]
        )
    }
}

// MARK: - MarlinDeviceBridge

public final class MarlinDeviceBridge: Sendable {
    public static let defaultAppGroupID = "group.org.tao3k.marlin"
    public static let maxNotificationTitleChars = 160
    public static let maxNotificationBodyChars = 1024
    public static let defaultScreenshotLimit = 10
    public static let maxScreenshotLimit = 32
    public static let maxContactsLimit = 25

    public static let supportedFamilies = ["photos", "voice", "contacts", "notifications", "capture"]
    public static let supportedMethods = [
        "bridge.capabilities",
        "permissionState",
        "photos.recentScreenshots",
        "vision.extractOCR",
        "voice.latestTranscript",
        "contacts.search",
        "notify.send",
        "capture.drain",
    ]

    private let photos: PhotoLibraryProviding
    private let voice: VoiceCaptureProviding
    private let contacts: ContactsProviding
    private let notifications: NotificationDelivering
    private let captureStore: CaptureQueueStoring

    public init(
        appGroupID: String = defaultAppGroupID,
        photos: PhotoLibraryProviding = SystemPhotoLibraryProvider(),
        voice: VoiceCaptureProviding = EmptyVoiceCaptureProvider(),
        contacts: ContactsProviding = SystemContactsProvider(),
        notifications: NotificationDelivering = SystemNotificationDeliverer(),
        captureStore: CaptureQueueStoring? = nil
    ) {
        self.photos = photos
        self.voice = voice
        self.contacts = contacts
        self.notifications = notifications
        self.captureStore = captureStore ?? AppGroupCaptureStore(suiteName: appGroupID)
    }

    public func capabilities() -> BridgeCapabilitiesWire {
        BridgeCapabilitiesWire(
            platform: "ios",
            families: Self.supportedFamilies,
            methods: Self.supportedMethods
        )
    }

    public func latestTranscript() throws -> VoiceTranscriptWire {
        try voice.latestTranscript()
    }

    public func searchContacts(query: String, limit: Int = 10) throws -> [ContactSummaryWire] {
        let bounded = limit <= 0 ? 10 : min(limit, Self.maxContactsLimit)
        return try contacts.searchContacts(
            query: query.trimmingCharacters(in: .whitespacesAndNewlines),
            limit: bounded
        )
    }

    public static func mapPermissionStatus(_ status: PHAuthorizationStatus) -> OsPermissionStateWire {
        switch status {
        case .authorized:
            return .granted
        case .limited:
            return .limited
        case .denied, .restricted:
            return .denied
        case .notDetermined:
            return .prompt
        @unknown default:
            return .unsupported
        }
    }

    public static func clampScreenshotLimit(_ limit: Int) -> Int {
        if limit <= 0 {
            return defaultScreenshotLimit
        }
        return min(limit, maxScreenshotLimit)
    }

    public static func buildNotificationRequest(
        title: String,
        body: String,
        identifier: String = "marlin.gate.\(UUID().uuidString)"
    ) throws -> UNNotificationRequest {
        let cleanTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanBody = body.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanTitle.isEmpty || !cleanBody.isEmpty else {
            throw MarlinBridgeError.invalidArgument("notification title and body cannot both be empty")
        }
        let content = UNMutableNotificationContent()
        content.title = String(cleanTitle.prefix(maxNotificationTitleChars))
        content.body = String(cleanBody.prefix(maxNotificationBodyChars))
        content.sound = .default
        return UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
    }

    public func permissionState() -> OsPermissionStateWire {
        Self.mapPermissionStatus(photos.authorizationStatus())
    }

    public func recentScreenshots(limit: Int = defaultScreenshotLimit) throws -> [PhotoItemWire] {
        try photos.recentScreenshots(limit: Self.clampScreenshotLimit(limit))
    }

    public func extractTextOCR(localID: String) throws -> String {
        let trimmed = localID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw MarlinBridgeError.invalidArgument("localId must not be empty")
        }
        return try photos.extractTextOCR(localID: trimmed)
    }

    public func sendNotification(
        title: String,
        body: String,
        completion: @escaping @Sendable (Error?) -> Void
    ) {
        let request: UNNotificationRequest
        do {
            request = try Self.buildNotificationRequest(title: title, body: body)
        } catch {
            completion(error)
            return
        }
        notifications.deliver(request: request, completion: completion)
    }

    public func enqueueShareExtensionCapture(_ payload: CapturePayloadWire) throws {
        try captureStore.enqueue(payload)
    }

    public func drainShareExtensionQueue(maxItems: Int = AppGroupCaptureStore.maxQueuedItems) -> [CapturePayloadWire] {
        captureStore.drainAll(maxItems: maxItems)
    }

    /// Dispatches a JSON RPC call emitted by Go `runtime/device/nativebridge.Adapter`.
    public func dispatch(method: String, argsJSON: Data?) async throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let decoder = JSONDecoder()

        switch method {
        case "bridge.capabilities":
            return try encoder.encode(capabilities())

        case "permissionState":
            return try encoder.encode(PermissionStateResponse(state: permissionState()))

        case "photos.recentScreenshots":
            let limit: Int
            if let argsJSON, !argsJSON.isEmpty {
                let req = try decoder.decode(RecentScreenshotsRequest.self, from: argsJSON)
                limit = req.limit
            } else {
                limit = Self.defaultScreenshotLimit
            }
            let items = try recentScreenshots(limit: limit)
            return try encoder.encode(RecentScreenshotsResponse(items: items))

        case "vision.extractOCR":
            guard let argsJSON, !argsJSON.isEmpty else {
                throw MarlinBridgeError.invalidArgument("vision.extractOCR requires JSON arguments")
            }
            let req = try decoder.decode(ExtractOCRRequest.self, from: argsJSON)
            let text = try extractTextOCR(localID: req.localId)
            return try encoder.encode(ExtractOCRResponse(text: text))

        case "voice.latestTranscript":
            let tr = try latestTranscript()
            return try encoder.encode(tr)

        case "contacts.search":
            let query: String
            let limit: Int
            if let argsJSON, !argsJSON.isEmpty {
                let req = try decoder.decode(ContactSearchRequest.self, from: argsJSON)
                query = req.query
                limit = req.limit
            } else {
                query = ""
                limit = 10
            }
            let items = try searchContacts(query: query, limit: limit)
            return try encoder.encode(ContactSearchResponse(contacts: items))

        case "notify.send":
            guard let argsJSON, !argsJSON.isEmpty else {
                throw MarlinBridgeError.invalidArgument("notify.send requires JSON arguments")
            }
            let req = try decoder.decode(NotificationRequestWire.self, from: argsJSON)
            let unRequest = try Self.buildNotificationRequest(title: req.title, body: req.body)
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                notifications.deliver(request: unRequest) { error in
                    if let error {
                        continuation.resume(throwing: MarlinBridgeError.notificationFailed(error.localizedDescription))
                    } else {
                        continuation.resume(returning: ())
                    }
                }
            }
            return try encoder.encode(AckResponse(ok: true))

        case "capture.drain":
            let captures = drainShareExtensionQueue()
            return try encoder.encode(DrainCapturesResponse(captures: captures))

        default:
            throw MarlinBridgeError.unsupportedMethod(method)
        }
    }
}
