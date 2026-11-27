// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Real Swift executable probe driven by Go `runtime/device/nativebridge`
// cross-language E2E tests. Reads newline-delimited JSON RPC frames on stdin,
// dispatches them through the real MarlinDeviceBridge, and writes JSON responses
// on stdout — proving 100% live Go <-> Swift wire parity with zero Simulator.

import Foundation
import MarlinDeviceBridge
import Photos
import UserNotifications

private final class ProbePhotos: PhotoLibraryProviding, @unchecked Sendable {
    func authorizationStatus() -> PHAuthorizationStatus { .authorized }
    func recentScreenshots(limit: Int) throws -> [PhotoItemWire] {
        Array(
            [
                PhotoItemWire(
                    localId: "ios-ph-1",
                    createdUnixMs: 1_764_000_000_000,
                    width: 1179,
                    height: 2556,
                    isScreenshot: true,
                    mimeType: "image/png"
                )
            ].prefix(limit)
        )
    }
    func extractTextOCR(localID: String) throws -> String {
        guard localID == "ios-ph-1" else {
            throw MarlinBridgeError.assetNotFound(localID)
        }
        return "Swift Vision OCR: Meeting at 15:00"
    }
}

private struct ProbeVoice: VoiceCaptureProviding {
    func latestTranscript() throws -> VoiceTranscriptWire {
        VoiceTranscriptWire(
            transcriptId: "ios-voice-1",
            locale: "en-US",
            text: "Schedule architecture review tomorrow",
            durationMs: 3400
        )
    }
}

private struct ProbeContacts: ContactsProviding {
    func searchContacts(query: String, limit: Int) throws -> [ContactSummaryWire] {
        let all = [
            ContactSummaryWire(contactId: "c-1", displayName: "Ada Lovelace", handle: "ada@tao3k.org"),
            ContactSummaryWire(contactId: "c-2", displayName: "Alan Turing", handle: "alan@tao3k.org"),
        ]
        let q = query.lowercased()
        let matched = q.isEmpty ? all : all.filter { $0.displayName.lowercased().contains(q) }
        return Array(matched.prefix(limit))
    }
}

private final class ProbeNotifications: NotificationDelivering, @unchecked Sendable {
    func deliver(request: UNNotificationRequest, completion: @escaping @Sendable (Error?) -> Void) {
        completion(nil)
    }
}

private struct ProbeRequest: Decodable {
    let method: String
    let args: String?
}

private struct ProbeResponse: Encodable {
    let ok: Bool
    let data: String?
    let error: String?
}

@main
enum MarlinDeviceBridgeProbeMain {
    static func main() async {
        let suite = "org.tao3k.marlin.probe.\(UUID().uuidString)"
        defer { UserDefaults.standard.removePersistentDomain(forName: suite) }

        let bridge = MarlinDeviceBridge(
            appGroupID: suite,
            photos: ProbePhotos(),
            voice: ProbeVoice(),
            contacts: ProbeContacts(),
            notifications: ProbeNotifications()
        )
        try? bridge.enqueueShareExtensionCapture(
            CapturePayloadWire(
                sourceChannel: "ios.share-extension",
                text: "Captured from iOS Share Extension",
                metadata: ["origin": "safari"]
            )
        )

        let decoder = JSONDecoder()
        let encoder = JSONEncoder()
        while let line = readLine() {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.isEmpty { continue }
            do {
                let req = try decoder.decode(ProbeRequest.self, from: Data(trimmed.utf8))
                let argsData = req.args.flatMap { $0.isEmpty ? nil : Data($0.utf8) }
                let outData = try await bridge.dispatch(method: req.method, argsJSON: argsData)
                let resp = ProbeResponse(ok: true, data: String(decoding: outData, as: UTF8.self), error: nil)
                let respBytes = try encoder.encode(resp)
                print(String(decoding: respBytes, as: UTF8.self))
                fflush(stdout)
            } catch {
                let resp = ProbeResponse(ok: false, data: nil, error: error.localizedDescription)
                if let respBytes = try? encoder.encode(resp) {
                    print(String(decoding: respBytes, as: UTF8.self))
                    fflush(stdout)
                }
            }
        }
    }
}
