// swift-tools-version: 5.9
// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import PackageDescription

let package = Package(
    name: "MarlinDeviceBridge",
    platforms: [
        .iOS(.v16),
        .macOS(.v13),
    ],
    products: [
        .library(
            name: "MarlinDeviceBridge",
            targets: ["MarlinDeviceBridge"]
        ),
        .executable(
            name: "MarlinDeviceBridgeProbe",
            targets: ["MarlinDeviceBridgeProbe"]
        ),
    ],
    targets: [
        .target(
            name: "MarlinDeviceBridge",
            path: "Sources/MarlinDeviceBridge"
        ),
        .executableTarget(
            name: "MarlinDeviceBridgeProbe",
            dependencies: ["MarlinDeviceBridge"],
            path: "Sources/MarlinDeviceBridgeProbe"
        ),
        .testTarget(
            name: "MarlinDeviceBridgeTests",
            dependencies: ["MarlinDeviceBridge"],
            path: "Tests/MarlinDeviceBridgeTests"
        ),
    ]
)
