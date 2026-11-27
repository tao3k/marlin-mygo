// swift-tools-version: 5.10
// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import PackageDescription

let package = Package(
    name: "MarlinPlatformIOS",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(
            name: "MarlinPlatformIOS",
            targets: ["MarlinPlatformIOS"]
        ),
    ],
    targets: [
        .target(
            name: "MarlinPlatformIOS",
            path: "Sources/MarlinPlatformIOS"
        ),
    ]
)
