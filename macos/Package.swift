// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Portside",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "Portside",
            path: "Sources/Portside",
            swiftSettings: [.swiftLanguageMode(.v5)]
        )
    ]
)
