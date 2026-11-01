import AppKit
import SwiftUI

// MARK: - Agent filter

/// A dropdown rather than a row of buttons, so it keeps working as agents are added.
/// Only agents that have sessions are listed, with their counts.
struct AgentFilterMenu: View {
    @ObservedObject var store: Store

    var body: some View {
        Menu {
            Picker("Agent", selection: $store.filter) {
                Text("All agents (\(store.sessions.count))").tag(String?.none)
                ForEach(store.agentCounts, id: \.agent) { item in
                    Text("\(Agents.label(item.agent)) (\(item.count))").tag(Optional(item.agent))
                }
            }
            .pickerStyle(.inline)
        } label: {
            HStack(spacing: 6) {
                Circle()
                    .fill(store.filter.map(Agents.color) ?? Color.secondary)
                    .frame(width: 8, height: 8)
                Text(store.filter.map(Agents.shortLabel) ?? "All agents")
                    .font(.system(size: 12, weight: .medium))
                Image(systemName: "chevron.down").font(.system(size: 8, weight: .bold))
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 7)
            .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(0.07)))
            .foregroundStyle(.primary)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Show sessions from one agent or all of them")
    }
}

// MARK: - Row

struct SessionRow: View {
    let session: SessionItem
    @ObservedObject var store: Store

    private var busy: Bool { store.busy.contains(session.key) }
    private var selected: Bool { store.selection == session.key }
    private var targets: [String] { store.targets(for: session) }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            AgentBadge(agent: session.agent)

            VStack(alignment: .leading, spacing: 3) {
                Text(session.displayTitle)
                    .font(.system(size: 13, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.tail)
                HStack(spacing: 4) {
                    Text(session.project)
                    if let b = session.branch, !b.isEmpty {
                        Text("·")
                        Image(systemName: "arrow.triangle.branch").font(.system(size: 9))
                        Text(b).lineLimit(1)
                    }
                    if let d = session.updatedDate {
                        Text("·")
                        Text(Self.relative.localizedString(for: d, relativeTo: Date()))
                    }
                }
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }

            Spacer(minLength: 4)

            if busy {
                ProgressView().controlSize(.small)
            } else if selected {
                actions.fixedSize().layoutPriority(1)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 7)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(selected ? Color.accentColor.opacity(0.16) : .clear)
        )
        .contentShape(Rectangle())
        // Moving the mouse selects the row, so mouse and keyboard share one selection.
        .onContinuousHover { phase in
            switch phase {
            case .active: store.hoverMoved(over: session)
            case .ended: store.hoverEnded()
            }
        }
        .onTapGesture(count: 2) { store.resume(session) }
        .onTapGesture { store.selection = session.key }
        .contextMenu { menuItems }
        .help(session.cwd.isEmpty ? session.displayTitle : session.cwd)
    }

    private var actions: some View {
        HStack(spacing: 6) {
            Button {
                store.resume(session)
            } label: {
                ActionLabel(title: "Resume", symbol: "play.fill", prominent: true)
            }
            .buttonStyle(.plain)
            .help("Resume in \(Agents.label(session.agent)): opens a new terminal window (↩)")

            Menu {
                Section("Hand off to") {
                    ForEach(targets, id: \.self) { t in
                        Button(Agents.label(t)) { store.handoff(session, to: t) }
                    }
                }
                Section("With a written briefing") {
                    ForEach(targets, id: \.self) { t in
                        Button("\(Agents.label(t)), summarized") {
                            store.handoff(session, to: t, summarize: true)
                        }
                    }
                }
            } label: {
                ActionLabel(title: "Hand off", symbol: nil, prominent: false, chevron: true)
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("Continue this session in another agent. It gets a condensed, secret-masked copy of the conversation.")

            Menu {
                moreItems
            } label: {
                Image(systemName: "ellipsis")
                    .font(.system(size: 12, weight: .bold))
                    .frame(width: 26, height: 24)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.14)))
                    .foregroundStyle(.primary)
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("More: full preview, copy resume command, open the project")
        }
    }

    /// The right-click menu: everything the buttons offer.
    @ViewBuilder
    private var menuItems: some View {
        Button("Resume in \(Agents.label(session.agent))") { store.resume(session) }
        Divider()
        ForEach(targets, id: \.self) { t in
            Button("Hand Off to \(Agents.label(t))") { store.handoff(session, to: t) }
        }
        ForEach(targets, id: \.self) { t in
            Button("Hand Off to \(Agents.label(t)) with Briefing") {
                store.handoff(session, to: t, summarize: true)
            }
        }
        Divider()
        moreItems
    }

    @ViewBuilder
    private var moreItems: some View {
        Button("Continue in a Fresh \(Agents.label(session.agent)) Session") {
            store.handoff(session, to: session.agent)
        }
        Button("Open Full Preview") { store.preview(session) }
        Button("Copy Resume Command") { store.copyResumeCommand(session) }
        Button("Open Project in Finder") { store.openProject(session) }
            .disabled(session.cwd.isEmpty)
    }

    private static let relative: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f
    }()
}

/// A labelled button face: solid so it stays readable on the highlighted row.
struct ActionLabel: View {
    let title: String
    var symbol: String?
    var prominent: Bool
    var chevron = false

    var body: some View {
        HStack(spacing: 4) {
            if let symbol { Image(systemName: symbol).font(.system(size: 9, weight: .bold)) }
            Text(title).font(.system(size: 12, weight: .semibold)).lineLimit(1).fixedSize()
            if chevron { Image(systemName: "chevron.down").font(.system(size: 8, weight: .bold)) }
        }
        .padding(.horizontal, 9)
        .frame(height: 24)
        .foregroundStyle(prominent ? Color.white : Color.primary)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(prominent ? Color.accentColor : Color.primary.opacity(0.14))
        )
    }
}

struct AgentBadge: View {
    let agent: String
    var body: some View {
        Text(Agents.initials(agent))
            .font(.system(size: 10, weight: .bold, design: .rounded))
            .foregroundStyle(.white)
            .frame(width: 26, height: 26)
            .background(RoundedRectangle(cornerRadius: 7).fill(Agents.color(agent)))
            .help(Agents.label(agent))
    }
}

// MARK: - Empty and first-run states

struct StateCard<Actions: View>: View {
    let symbol: String
    let title: String
    let message: String
    @ViewBuilder var actions: Actions

    var body: some View {
        VStack(spacing: 10) {
            Spacer()
            Image(systemName: symbol).font(.system(size: 30)).foregroundStyle(.tertiary)
            Text(title).font(.system(size: 14, weight: .semibold))
            Text(message)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
            actions
            Spacer()
        }
        .padding(.horizontal, 28)
        .frame(maxWidth: .infinity)
    }
}

struct NoMatchesView: View {
    @ObservedObject var store: Store

    var body: some View {
        StateCard(symbol: "magnifyingglass", title: "No matching sessions",
                  message: "Nothing matches your search or the agent filter.") {
            Button("Clear search and filter") { store.clearFilters() }
                .help("Show all sessions again (esc)")
        }
    }
}

struct NoSessionsView: View {
    @ObservedObject var store: Store

    var body: some View {
        StateCard(symbol: "tray", title: "No sessions yet",
                  message: "Start a session in Claude Code, Codex, Gemini CLI or Cursor and it will show up here.") {
            if !store.installed.isEmpty {
                VStack(spacing: 6) {
                    Text("Found on this Mac").font(.system(size: 11)).foregroundStyle(.secondary)
                    HStack(spacing: 8) {
                        ForEach(Agents.known.filter(store.installed.contains), id: \.self) { a in
                            HStack(spacing: 5) {
                                AgentBadge(agent: a).scaleEffect(0.8).frame(width: 22, height: 22)
                                Text(Agents.shortLabel(a)).font(.system(size: 12))
                            }
                        }
                    }
                }
                .padding(.vertical, 4)
            } else {
                Text("None of the supported agents were found on your PATH.")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
            HStack {
                Button("Run diagnostics") { store.runDiagnostics() }
                    .help("Run relai doctor to see which agents and session folders relai finds")
                Button("Refresh") { Task { await store.refresh() } }
                    .help("Look for sessions again")
            }
        }
    }
}

/// Shown when the relai command can't be found.
struct FirstRunView: View {
    @ObservedObject var store: Store
    private let installCommand = "go install github.com/colt005/relai/cmd/relai@latest"

    var body: some View {
        StateCard(symbol: "terminal", title: "relai isn't installed yet",
                  message: "RelaiBar shows the sessions that the relai command finds. Install it, then try again.") {
            VStack(alignment: .leading, spacing: 8) {
                Text("1. Install it in Terminal").font(.system(size: 12, weight: .semibold))
                HStack {
                    Text(installCommand)
                        .font(.system(size: 11, design: .monospaced))
                        .textSelection(.enabled)
                        .lineLimit(2)
                    Spacer(minLength: 4)
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(installCommand, forType: .string)
                        store.flash("Copied install command")
                    } label: {
                        Image(systemName: "doc.on.doc")
                    }
                    .buttonStyle(.plain)
                    .help("Copy the install command")
                }
                .padding(8)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.07)))

                Text("2. Already installed somewhere unusual?").font(.system(size: 12, weight: .semibold))
                    .padding(.top, 4)
                HStack {
                    Button("Choose relai…") { store.chooseRelaiBinary() }
                        .help("Pick the relai command from disk")
                    Button("Try again") { Task { await store.refresh() } }
                        .help("Look for relai again")
                }
            }
            .padding(.top, 6)
        }
    }
}

// MARK: - Settings

struct SettingsPanel: View {
    @ObservedObject var store: Store

    var body: some View {
        Form {
            Section {
                Picker("Open sessions in", selection: $store.terminal) {
                    Text("Terminal").tag("Terminal")
                    Text("iTerm2").tag("iTerm")
                    if Store.cmuxInstalled || store.terminal == "cmux" {
                        Text("cmux").tag("cmux")
                    }
                }
                if store.terminal == "cmux" {
                    Text("cmux only accepts commands from other apps when Settings > Automation allows external access. Otherwise sessions won't open.")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                Toggle("Launch at login", isOn: Binding(
                    get: { store.launchAtLogin },
                    set: { store.launchAtLogin = $0 }
                ))
                Toggle("Preview a session when hovering it for 1.5 seconds", isOn: $store.showPreview)
            }
            Section {
                TextField("relai path", text: $store.relaiPath, prompt: Text("Auto-detect"))
                Text("Leave empty to find relai automatically (~/go/bin, Homebrew, or your shell PATH).")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            Section {
                Text("Handoffs and secrets masking are handled by the relai CLI. Handoff files live in ~/.relai/handoffs.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .frame(maxHeight: .infinity)
    }
}
