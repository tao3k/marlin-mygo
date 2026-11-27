// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import Foundation
import Photos
import UserNotifications
import XCTest
@testable import MarlinDeviceBridge

// MARK: - Deterministic Test Doubles

private final class StubPhotoLibraryProvider: PhotoLibraryProviding, @unchecked Sendable {
    var status: PHAuthorizationStatus
    var screenshots: [PhotoItemWire]
    var ocrByLocalID: [String: String]
    private(set) var lastRequestedLimit: Int?

    init(
        status: PHAuthorizationStatus = .authorized,
        screenshots: [PhotoItemWire] = [],
        ocrByLocalID: [String: String] = [:]
    ) {
        self.status = status
        self.screenshots = screenshots
        self.ocrByLocalID = ocrByLocalID
    }

    func authorizationStatus() -> PHAuthorizationStatus {
        status
    }

    func recentScreenshots(limit: Int) throws -> [PhotoItemWire] {
        lastRequestedLimit = limit
        return Array(screenshots.prefix(limit))
    }

    func extractTextOCR(localID: String) throws -> String {
        guard let text = ocrByLocalID[localID] else {
            throw MarlinBridgeError.assetNotFound(localID)
        }
        return text
    }
}

private final class RecordingNotificationDeliverer: NotificationDelivering, @unchecked Sendable {
    private let lock = NSLock()
    private var _delivered: [UNNotificationRequest] = []
    var forcedError: Error?

    var delivered: [UNNotificationRequest] {
        lock.lock()
        defer { lock.unlock() }
        return _delivered
    }

    func deliver(request: UNNotificationRequest, completion: @escaping @Sendable (Error?) -> Void) {
        if let forcedError {
            completion(forcedError)
            return
        }
        lock.lock()
        _delivered.append(request)
        lock.unlock()
        completion(nil)
    }
}

private struct StubVoiceProvider: VoiceCaptureProviding {
    let transcript: VoiceTranscriptWire
    func latestTranscript() throws -> VoiceTranscriptWire { transcript }
}

private final class StubContactsProvider: ContactsProviding, @unchecked Sendable {
    var contacts: [ContactSummaryWire]
    private(set) var lastQuery: String?
    private(set) var lastLimit: Int?

    init(contacts: [ContactSummaryWire]) {
        self.contacts = contacts
    }

    func searchContacts(query: String, limit: Int) throws -> [ContactSummaryWire] {
        lastQuery = query
        lastLimit = limit
        let q = query.lowercased()
        let filtered = q.isEmpty ? contacts : contacts.filter { $0.displayName.lowercased().contains(q) }
        return Array(filtered.prefix(limit))
    }
}

// MARK: - Unit & Go Wire Parity Tests (Runs Natively on macOS — Zero Simulator Required)

final class MarlinDeviceBridgeTests: XCTestCase {
    func testPermissionStatusMappingCoversAllPhotoKitStates() {
        XCTAssertEqual(MarlinDeviceBridge.mapPermissionStatus(.authorized), .granted)
        XCTAssertEqual(MarlinDeviceBridge.mapPermissionStatus(.limited), .limited)
        XCTAssertEqual(MarlinDeviceBridge.mapPermissionStatus(.denied), .denied)
        XCTAssertEqual(MarlinDeviceBridge.mapPermissionStatus(.restricted), .denied)
        XCTAssertEqual(MarlinDeviceBridge.mapPermissionStatus(.notDetermined), .prompt)
    }

    func testDispatchPermissionStateWireParityWithGoAdapter() async throws {
        let photos = StubPhotoLibraryProvider(status: .limited)
        let bridge = MarlinDeviceBridge(photos: photos, notifications: RecordingNotificationDeliverer())

        let args = Data(#"{"platform":"ios"}"#.utf8)
        let raw = try await bridge.dispatch(method: "permissionState", argsJSON: args)
        XCTAssertEqual(String(decoding: raw, as: UTF8.self), #"{"state":"limited"}"#)
    }

    func testDispatchRecentScreenshotsClampsLimitAndMatchesGoJSONFields() async throws {
        let sample = PhotoItemWire(
            localId: "ph-1",
            createdUnixMs: 1_764_000_000_000,
            width: 1170,
            height: 2532,
            isScreenshot: true,
            mimeType: "image/png"
        )
        let photos = StubPhotoLibraryProvider(screenshots: [sample])
        let bridge = MarlinDeviceBridge(photos: photos, notifications: RecordingNotificationDeliverer())

        // Limit above 32 must clamp to 32; limit <= 0 must default to 10.
        _ = try await bridge.dispatch(
            method: "photos.recentScreenshots",
            argsJSON: Data(#"{"limit":999}"#.utf8)
        )
        XCTAssertEqual(photos.lastRequestedLimit, 32)

        let raw = try await bridge.dispatch(
            method: "photos.recentScreenshots",
            argsJSON: Data(#"{"limit":0}"#.utf8)
        )
        XCTAssertEqual(photos.lastRequestedLimit, 10)

        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let items = try XCTUnwrap(obj["items"] as? [[String: Any]])
        XCTAssertEqual(items.count, 1)
        XCTAssertEqual(items[0]["localId"] as? String, "ph-1")
        XCTAssertEqual(items[0]["createdUnixMs"] as? Int64, 1_764_000_000_000)
        XCTAssertEqual(items[0]["width"] as? Int, 1170)
        XCTAssertEqual(items[0]["height"] as? Int, 2532)
        XCTAssertEqual(items[0]["isScreenshot"] as? Bool, true)
        XCTAssertEqual(items[0]["mimeType"] as? String, "image/png")
    }

    func testDispatchVisionOCRSuccessAndRejectsEmptyLocalID() async throws {
        let photos = StubPhotoLibraryProvider(ocrByLocalID: ["ph-1": "Meeting at 14:00"])
        let bridge = MarlinDeviceBridge(photos: photos, notifications: RecordingNotificationDeliverer())

        let raw = try await bridge.dispatch(
            method: "vision.extractOCR",
            argsJSON: Data(#"{"localId":"ph-1"}"#.utf8)
        )
        XCTAssertEqual(String(decoding: raw, as: UTF8.self), #"{"text":"Meeting at 14:00"}"#)

        do {
            _ = try await bridge.dispatch(
                method: "vision.extractOCR",
                argsJSON: Data(#"{"localId":"   "}"#.utf8)
            )
            XCTFail("expected invalidArgument for empty localId")
        } catch let err as MarlinBridgeError {
            XCTAssertEqual(err, .invalidArgument("localId must not be empty"))
        }
    }

    func testDispatchNotificationBuildsGatePrefixedRequestAndRejectsEmptyPayload() async throws {
        let deliverer = RecordingNotificationDeliverer()
        let bridge = MarlinDeviceBridge(photos: StubPhotoLibraryProvider(), notifications: deliverer)

        let raw = try await bridge.dispatch(
            method: "notify.send",
            argsJSON: Data(#"{"title":"Marlin","body":"Approved"}"#.utf8)
        )
        XCTAssertEqual(String(decoding: raw, as: UTF8.self), #"{"ok":true}"#)
        XCTAssertEqual(deliverer.delivered.count, 1)
        let req = try XCTUnwrap(deliverer.delivered.first)
        XCTAssertTrue(req.identifier.hasPrefix("marlin.gate."))
        XCTAssertEqual(req.content.title, "Marlin")
        XCTAssertEqual(req.content.body, "Approved")

        do {
            _ = try await bridge.dispatch(
                method: "notify.send",
                argsJSON: Data(#"{"title":"  ","body":""}"#.utf8)
            )
            XCTFail("expected invalidArgument for empty notification")
        } catch let err as MarlinBridgeError {
            XCTAssertEqual(err, .invalidArgument("notification title and body cannot both be empty"))
        }
    }

    func testAppGroupCaptureQueueRealUserDefaultsFIFOAndAtomicDrain() async throws {
        let suite = "org.tao3k.marlin.test.\(UUID().uuidString)"
        defer { UserDefaults.standard.removePersistentDomain(forName: suite) }

        let bridge = MarlinDeviceBridge(
            appGroupID: suite,
            photos: StubPhotoLibraryProvider(),
            notifications: RecordingNotificationDeliverer()
        )

        try bridge.enqueueShareExtensionCapture(
            CapturePayloadWire(
                sourceChannel: "ios.share-extension",
                text: "Draft Q4 roadmap",
                metadata: ["origin": "safari"]
            )
        )
        try bridge.enqueueShareExtensionCapture(
            CapturePayloadWire(
                sourceChannel: "ios.backtap-shortcut",
                ocrText: "Invoice #42",
                mimeType: "image/png"
            )
        )

        let rawFirst = try await bridge.dispatch(method: "capture.drain", argsJSON: nil)
        let decodedFirst = try JSONDecoder().decode(DrainCapturesResponse.self, from: rawFirst)
        XCTAssertEqual(decodedFirst.captures.count, 2)
        XCTAssertEqual(decodedFirst.captures[0].sourceChannel, "ios.share-extension")
        XCTAssertEqual(decodedFirst.captures[0].text, "Draft Q4 roadmap")
        XCTAssertEqual(decodedFirst.captures[0].metadata?["origin"], "safari")
        XCTAssertEqual(decodedFirst.captures[1].sourceChannel, "ios.backtap-shortcut")
        XCTAssertEqual(decodedFirst.captures[1].ocrText, "Invoice #42")

        // Second drain must be empty (atomic consumption prevents duplicate ingest).
        let rawSecond = try await bridge.dispatch(method: "capture.drain", argsJSON: nil)
        let decodedSecond = try JSONDecoder().decode(DrainCapturesResponse.self, from: rawSecond)
        XCTAssertTrue(decodedSecond.captures.isEmpty)
    }

    func testAppGroupCaptureQueueDropsMalformedLegacyEntriesAndCapsBatchSize() throws {
        let suite = "org.tao3k.marlin.test.legacy.\(UUID().uuidString)"
        defer { UserDefaults.standard.removePersistentDomain(forName: suite) }

        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defaults.set(
            [
                ["sourceChannel": ""],
                ["missingChannel": "nope"],
                ["sourceChannel": "ios.share-extension", "text": "Valid legacy capture"],
            ],
            forKey: AppGroupCaptureStore.queueKey
        )

        let store = AppGroupCaptureStore(suiteName: suite)
        let drained = store.drainAll()
        XCTAssertEqual(drained.count, 1)
        XCTAssertEqual(drained[0].sourceChannel, "ios.share-extension")
        XCTAssertEqual(drained[0].text, "Valid legacy capture")
    }

    func testDispatchCapabilitiesVoiceAndContactsWireParity() async throws {
        let voice = StubVoiceProvider(
            transcript: VoiceTranscriptWire(
                transcriptId: "vt-swift-1",
                locale: "en-US",
                text: "Ship Marlin unified UI",
                durationMs: 2150
            )
        )
        let contacts = StubContactsProvider(contacts: [
            ContactSummaryWire(contactId: "c-1", displayName: "Ada Lovelace", handle: "ada@tao3k.org"),
            ContactSummaryWire(contactId: "c-2", displayName: "Grace Hopper", handle: "grace@tao3k.org"),
        ])
        let bridge = MarlinDeviceBridge(
            photos: StubPhotoLibraryProvider(),
            voice: voice,
            contacts: contacts,
            notifications: RecordingNotificationDeliverer()
        )

        let capsRaw = try await bridge.dispatch(method: "bridge.capabilities", argsJSON: nil)
        let caps = try JSONDecoder().decode(BridgeCapabilitiesWire.self, from: capsRaw)
        XCTAssertEqual(caps.platform, "ios")
        XCTAssertEqual(caps.families.count, 5)
        XCTAssertEqual(caps.methods.count, 8)

        let voiceRaw = try await bridge.dispatch(method: "voice.latestTranscript", argsJSON: nil)
        let decodedVoice = try JSONDecoder().decode(VoiceTranscriptWire.self, from: voiceRaw)
        XCTAssertEqual(decodedVoice.transcriptId, "vt-swift-1")
        XCTAssertEqual(decodedVoice.durationMs, 2150)

        let contactsRaw = try await bridge.dispatch(
            method: "contacts.search",
            argsJSON: Data(#"{"query":"  Ada  ","limit":999}"#.utf8)
        )
        XCTAssertEqual(contacts.lastQuery, "Ada")
        XCTAssertEqual(contacts.lastLimit, MarlinDeviceBridge.maxContactsLimit)
        let decodedContacts = try JSONDecoder().decode(ContactSearchResponse.self, from: contactsRaw)
        XCTAssertEqual(decodedContacts.contacts.count, 1)
        XCTAssertEqual(decodedContacts.contacts[0].displayName, "Ada Lovelace")
    }

    func testUnsupportedMethodFailsClosed() async {
        let bridge = MarlinDeviceBridge(
            photos: StubPhotoLibraryProvider(),
            notifications: RecordingNotificationDeliverer()
        )
        do {
            _ = try await bridge.dispatch(method: "native.execShell", argsJSON: nil)
            XCTFail("expected unsupportedMethod")
        } catch let err as MarlinBridgeError {
            XCTAssertEqual(err, .unsupportedMethod("native.execShell"))
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testConcurrentDispatchStress100TasksZeroRaces() async throws {
        let suite = "org.tao3k.marlin.test.stress.\(UUID().uuidString)"
        defer { UserDefaults.standard.removePersistentDomain(forName: suite) }

        let photos = StubPhotoLibraryProvider(
            status: .authorized,
            screenshots: [
                PhotoItemWire(
                    localId: "ph-stress",
                    createdUnixMs: 1_764_000_000_001,
                    width: 1290,
                    height: 2796,
                    isScreenshot: true,
                    mimeType: "image/png"
                )
            ],
            ocrByLocalID: ["ph-stress": "Concurrency Safe"]
        )
        let deliverer = RecordingNotificationDeliverer()
        let bridge = MarlinDeviceBridge(
            appGroupID: suite,
            photos: photos,
            notifications: deliverer
        )

        try await withThrowingTaskGroup(of: Int.self) { group in
            for i in 0..<100 {
                group.addTask {
                    switch i % 4 {
                    case 0:
                        let data = try await bridge.dispatch(method: "permissionState", argsJSON: nil)
                        XCTAssertFalse(data.isEmpty)
                    case 1:
                        let data = try await bridge.dispatch(
                            method: "photos.recentScreenshots",
                            argsJSON: Data(#"{"limit":4}"#.utf8)
                        )
                        XCTAssertFalse(data.isEmpty)
                    case 2:
                        let data = try await bridge.dispatch(
                            method: "vision.extractOCR",
                            argsJSON: Data(#"{"localId":"ph-stress"}"#.utf8)
                        )
                        XCTAssertFalse(data.isEmpty)
                    default:
                        let data = try await bridge.dispatch(
                            method: "notify.send",
                            argsJSON: Data(#"{"title":"Stress","body":"Ping"}"#.utf8)
                        )
                        XCTAssertFalse(data.isEmpty)
                    }
                    return i
                }
            }
            var completed = 0
            for try await _ in group {
                completed += 1
            }
            XCTAssertEqual(completed, 100)
        }
        XCTAssertEqual(deliverer.delivered.count, 25)
    }
}
