// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Portside",
    platforms: [.macOS(.v26)],
    targets: [
        .executableTarget(
            name: "Portside",
            path: "Sources/Portside",
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "PortsideTests",
            dependencies: ["Portside"],
            path: "Tests/PortsideTests",
            swiftSettings: [.swiftLanguageMode(.v5)]
        )
    ]
)
