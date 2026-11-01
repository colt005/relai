import AppKit
import SwiftUI

/// A plain window showing a session's condensed transcript (`relai show`).
enum PreviewWindow {
    private static var open: [NSWindow] = []

    @MainActor
    static func show(title: String, markdown: String) {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 640),
            styleMask: [.titled, .closable, .resizable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = title
        window.isReleasedWhenClosed = false
        window.contentViewController = NSHostingController(rootView: PreviewView(markdown: markdown))
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        open.append(window)
        NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: window, queue: .main) { _ in
            open.removeAll { $0 === window }
        }
    }
}

struct PreviewView: View {
    let markdown: String

    var body: some View {
        ScrollView {
            Text(rendered)
                .font(.system(size: 12.5))
                .lineSpacing(3)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(20)
        }
        .frame(minWidth: 480, minHeight: 360)
    }

    /// Inline markdown (bold, code) rendered; block structure kept as plain text.
    private var rendered: AttributedString {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: markdown, options: opts)) ?? AttributedString(markdown)
    }
}
