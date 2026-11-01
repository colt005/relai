// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "RelaiBar",
    platforms: [.macOS(.v13)], // MenuBarExtra and SMAppService need macOS 13
    targets: [
        .executableTarget(name: "RelaiBar", path: "Sources/RelaiBar")
    ]
)
