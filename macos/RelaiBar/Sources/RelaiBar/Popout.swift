import AppKit
import SwiftUI

/// A floating preview that appears beside the menu bar popover while a session is
/// hovered. It never takes focus or mouse events, so it can't disturb the popover.
@MainActor
enum PreviewPopout {
    private static var panel: NSPanel?
    private static let size = NSSize(width: 420, height: 340)

    static func show(store: Store, session: SessionItem) {
        hide()
        guard let host = hostWindow() else { return }

        let p = NSPanel(contentRect: NSRect(origin: .zero, size: size),
                        styleMask: [.borderless, .nonactivatingPanel],
                        backing: .buffered, defer: false)
        p.isOpaque = false
        p.backgroundColor = .clear
        p.hasShadow = false
        p.ignoresMouseEvents = true
        p.level = .popUpMenu
        p.collectionBehavior = [.transient, .ignoresCycle, .fullScreenAuxiliary]
        p.contentView = NSHostingView(rootView: PopoutView(store: store, session: session))

        // Prefer the left of the popover (the menu bar icon sits at the right edge of
        // the screen); fall back to the right. Align with the pointer, kept on screen.
        let screen = host.screen?.visibleFrame ?? NSScreen.main?.visibleFrame ?? .zero
        var x = host.frame.minX - size.width - 6
        if x < screen.minX { x = host.frame.maxX + 6 }
        x = min(x, screen.maxX - size.width)
        var y = NSEvent.mouseLocation.y - size.height / 2
        y = min(max(y, screen.minY), screen.maxY - size.height)
        p.setFrameOrigin(NSPoint(x: x, y: y))
        p.orderFrontRegardless()
        panel = p
    }

    static func hide() {
        panel?.orderOut(nil)
        panel = nil
    }

    static var isShowing: Bool { panel != nil }

    private static func hostWindow() -> NSWindow? {
        if let key = NSApp.keyWindow, key is NSPanel { return key }
        return NSApp.windows.first { $0 is NSPanel && $0.isVisible && $0 !== panel }
    }
}

struct PopoutView: View {
    @ObservedObject var store: Store
    let session: SessionItem

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                AgentBadge(agent: session.agent).scaleEffect(0.8).frame(width: 22, height: 22)
                VStack(alignment: .leading, spacing: 1) {
                    Text(session.displayTitle)
                        .font(.system(size: 12, weight: .semibold))
                        .lineLimit(1)
                    Text("Latest in this session")
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            Divider()
            Text(rendered)
                .font(.system(size: 11.5))
                .lineSpacing(2)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .padding(14)
        .frame(width: 420, height: 340)
        .background(RoundedRectangle(cornerRadius: 12).fill(.regularMaterial))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.12)))
    }

    private var rendered: AttributedString {
        guard let text = store.previewText(for: session) else { return AttributedString("Loading…") }
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: Self.tail(of: text), options: opts)) ?? AttributedString(text)
    }

    /// The end of the transcript: where the session left off.
    static func tail(of text: String, limit: Int = 2400) -> String {
        var body = text
        if let r = text.range(of: "## Condensed transcript") {
            body = String(text[r.upperBound...])
        }
        body = body.trimmingCharacters(in: .whitespacesAndNewlines)
        guard body.count > limit else { return body }
        var tail = String(body.suffix(limit))
        if let nl = tail.firstIndex(of: "\n") { tail = String(tail[tail.index(after: nl)...]) }
        return "…\n" + tail
    }
}
