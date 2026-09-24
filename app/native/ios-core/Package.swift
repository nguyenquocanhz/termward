// swift-tools-version: 5.9
import PackageDescription

// The Go core is a binary target produced by gomobile:
//   node scripts/mobile.mjs core ios   (macOS, from app/)
let package = Package(
    name: "TermwardIosCore",
    platforms: [.iOS(.v15)],
    products: [
        .library(name: "TermwardIosCore", targets: ["TermwardCorePlugin"])
    ],
    dependencies: [
        .package(url: "https://github.com/ionic-team/capacitor-swift-pm.git", from: "8.0.0")
    ],
    targets: [
        .binaryTarget(name: "Termwardcore", path: "Termwardcore.xcframework"),
        .target(
            name: "TermwardCorePlugin",
            dependencies: [
                .product(name: "Capacitor", package: "capacitor-swift-pm"),
                .product(name: "Cordova", package: "capacitor-swift-pm"),
                "Termwardcore",
            ],
            path: "Sources/TermwardCorePlugin"
        ),
    ]
)
