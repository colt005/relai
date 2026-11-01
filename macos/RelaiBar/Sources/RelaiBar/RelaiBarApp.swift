import AppKit
import SwiftUI

@main
struct RelaiBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var store = Store()

    var body: some Scene {
        MenuBarExtra("relai", systemImage: "arrow.left.arrow.right.circle") {
            ContentView(store: store)
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // Menu bar only: no Dock icon, even when run without the app bundle (swift run).
        NSApp.setActivationPolicy(.accessory)
    }
}
