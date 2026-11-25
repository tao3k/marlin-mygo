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
    ],
    targets: [
        .target(
            name: "MarlinDeviceBridge",
            path: "Sources/MarlinDeviceBridge"
        ),
        .testTarget(
            name: "MarlinDeviceBridgeTests",
            dependencies: ["MarlinDeviceBridge"],
            path: "Tests/MarlinDeviceBridgeTests"
        ),
    ]
)
