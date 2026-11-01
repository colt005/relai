import AppKit
import SwiftUI

struct ContentView: View {
    @ObservedObject var store: Store
    @FocusState private var searchFocused: Bool
    @State private var keyMonitor: Any?

    var body: some View {
        VStack(spacing: 0) {
            if store.showSettings {
                SettingsPanel(store: store)
            } else if store.cliMissing {
                FirstRunView(store: store)
            } else {
                header
                Divider()
                banner
                sessionList
                keyHints
            }
            Divider()
            footer
        }
        .frame(width: 480, height: 580)
        .task { await store.refresh() }
        // The popover's window is reused, so refresh and refocus whenever it's shown again.
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didBecomeKeyNotification)) { note in
            guard (note.object as? NSWindow) is NSPanel else { return }
            Task { await store.refresh() }
            searchFocused = true
        }
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didResignKeyNotification)) { _ in
            store.hidePopout()
        }
        .onAppear {
            searchFocused = true
            installKeyMonitor()
        }
        .onChange(of: store.filtered.map(\.key)) { _ in store.ensureSelection() }
    }

    // MARK: keyboard

    /// Arrow keys, Return and ⌘-shortcuts work while the search field has focus, so
    /// they are handled before the text field sees them. Only the popover's panel is
    /// affected; the preview window and other windows keep normal behaviour.
    private func installKeyMonitor() {
        guard keyMonitor == nil else { return }
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [store] event in
            guard event.window is NSPanel, !store.showSettings, !store.cliMissing else { return event }
            let cmd = event.modifierFlags.contains(.command)
            switch event.keyCode {
            case 125: store.moveSelection(1); return nil          // ↓
            case 126: store.moveSelection(-1); return nil         // ↑
            case 36, 76:                                          // ↩ / enter
                guard let s = store.selectedSession else { return event }
                if cmd {
                    if let t = store.defaultTarget(for: s) { store.handoff(s, to: t) }
                } else {
                    store.resume(s)
                }
                return nil
            case 53:                                              // esc: clear search first
                if PreviewPopout.isShowing {
                    store.hidePopout()
                    return nil
                }
                if !store.query.isEmpty || store.filter != nil {
                    store.clearFilters()
                    return nil
                }
                return event
            default:
                if cmd, event.charactersIgnoringModifiers == "p" {
                    store.togglePopoutForSelection()
                    return nil
                }
                return event
            }
        }
    }

    // MARK: header

    private var header: some View {
        HStack(spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("Search by title, project or path", text: $store.query)
                    .textFieldStyle(.plain)
                    .font(.system(size: 13))
                    .focused($searchFocused)
                if store.loading {
                    ProgressView().controlSize(.small)
                } else if !store.query.isEmpty {
                    Button {
                        store.query = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary)
                    }
                    .buttonStyle(.plain)
                    .help("Clear search (esc)")
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(0.07)))

            if store.agentCounts.count > 1 {
                AgentFilterMenu(store: store)
            }
        }
        .padding(12)
    }

    // MARK: status banner

    @ViewBuilder
    private var banner: some View {
        if let err = store.error {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text(err).font(.system(size: 12)).textSelection(.enabled)
                Spacer(minLength: 0)
                Button {
                    store.error = nil
                } label: {
                    Image(systemName: "xmark").font(.system(size: 10, weight: .bold))
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .help("Dismiss")
            }
            .padding(10)
            .background(Color.orange.opacity(0.12))
        } else if let note = store.notice {
            HStack(spacing: 8) {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                Text(note).font(.system(size: 12))
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Color.green.opacity(0.10))
        }
    }

    // MARK: list

    @ViewBuilder
    private var sessionList: some View {
        let items = store.filtered
        if items.isEmpty {
            if store.sessions.isEmpty {
                if store.loading {
                    VStack { Spacer(); ProgressView(); Spacer() }.frame(maxWidth: .infinity)
                } else {
                    NoSessionsView(store: store)
                }
            } else {
                NoMatchesView(store: store)
            }
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(items, id: \.key) { s in
                            SessionRow(session: s, store: store)
                                .id(s.key)
                        }
                    }
                    .padding(6)
                }
                .onChange(of: store.selection) { key in
                    if let key { withAnimation(.easeOut(duration: 0.12)) { proxy.scrollTo(key) } }
                }
            }
        }
    }

    @ViewBuilder
    private var keyHints: some View {
        if let s = store.selectedSession {
            HStack(spacing: 10) {
                hint("↑↓", "select")
                hint("↩", "resume")
                if let t = store.defaultTarget(for: s) {
                    hint("⌘↩", "hand off to \(Agents.shortLabel(t))")
                }
                hint("⌘P", "peek")
                Spacer()
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 5)
            .background(Color.primary.opacity(0.04))
        }
    }

    private func hint(_ keys: String, _ what: String) -> some View {
        HStack(spacing: 4) {
            Text(keys).font(.system(size: 10, weight: .semibold, design: .rounded))
                .padding(.horizontal, 4)
                .background(RoundedRectangle(cornerRadius: 3).fill(Color.primary.opacity(0.12)))
            Text(what).font(.system(size: 10))
        }
        .foregroundStyle(.secondary)
    }

    // MARK: footer

    private var footer: some View {
        HStack(spacing: 14) {
            Button {
                Task { await store.refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .help("Refresh sessions (⌘R)")
            .keyboardShortcut("r")

            Button {
                store.revealHandoffs()
            } label: {
                Image(systemName: "folder")
            }
            .help("Open the handoffs folder in Finder")

            if !store.showSettings && !store.cliMissing {
                Button {
                    store.showPreview.toggle()
                } label: {
                    Image(systemName: store.showPreview ? "eye" : "eye.slash")
                        .foregroundStyle(store.showPreview ? Color.accentColor : .secondary)
                }
                .help(store.showPreview
                      ? "Preview on hover is on: rest the pointer on a session for 1.5 s. Click to turn off."
                      : "Preview on hover is off. Click to turn on.")
            }

            Spacer()

            if !store.cliMissing {
                Text("\(store.filtered.count) \(store.filtered.count == 1 ? "session" : "sessions")")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Button {
                store.showSettings.toggle()
                if !store.showSettings { searchFocused = true }
            } label: {
                Image(systemName: store.showSettings ? "list.bullet" : "gearshape")
            }
            .help(store.showSettings ? "Back to sessions" : "Settings")

            Button {
                NSApplication.shared.terminate(nil)
            } label: {
                Image(systemName: "power")
            }
            .help("Quit RelaiBar (⌘Q)")
            .keyboardShortcut("q")
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }
}
