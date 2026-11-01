import AppKit
import Foundation
import ServiceManagement
import SwiftUI

enum Agents {
    /// Every agent relai knows, in the order they are offered.
    static let known = ["claude", "codex", "gemini", "cursor"]

    static func label(_ agent: String) -> String {
        switch agent {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "gemini": return "Gemini CLI"
        case "cursor": return "Cursor"
        default: return agent.capitalized
        }
    }

    static func shortLabel(_ agent: String) -> String {
        switch agent {
        case "claude": return "Claude"
        case "gemini": return "Gemini"
        default: return label(agent)
        }
    }

    /// Two-letter badge text.
    static func initials(_ agent: String) -> String {
        switch agent {
        case "claude": return "CC"
        case "codex": return "Cx"
        case "gemini": return "Gm"
        case "cursor": return "Cu"
        default: return String(agent.prefix(2)).capitalized
        }
    }

    /// The command that starts the agent, used to tell which ones are installed.
    static func binary(_ agent: String) -> String { agent == "cursor" ? "cursor-agent" : agent }

    static func color(_ agent: String) -> Color {
        switch agent {
        case "claude": return Color(red: 0.80, green: 0.40, blue: 0.28)
        case "codex": return Color(red: 0.22, green: 0.47, blue: 0.88)
        case "gemini": return Color(red: 0.42, green: 0.36, blue: 0.80)
        case "cursor": return Color(red: 0.16, green: 0.50, blue: 0.56)
        default: return .gray
        }
    }

    /// Sort key so known agents keep a stable order and unknown ones follow.
    static func order(_ agent: String) -> Int { known.firstIndex(of: agent) ?? known.count }
}

@MainActor
final class Store: ObservableObject {
    @Published var sessions: [SessionItem] = []
    @Published var query = ""
    /// Agent to show, or nil for all of them.
    @Published var filter: String?
    @Published var selection: String?
    @Published var showSettings = false
    /// True when the relai command couldn't be found: shows the first-run screen.
    @Published var cliMissing = false
    @Published var installed: Set<String> = []
    @Published var showPreview: Bool = UserDefaults.standard.object(forKey: "showPreview") as? Bool ?? true {
        didSet { UserDefaults.standard.set(showPreview, forKey: "showPreview") }
    }
    @Published var previews: [String: String] = [:]
    @Published var lastTarget: String? = UserDefaults.standard.string(forKey: "lastHandoffTarget") {
        didSet { UserDefaults.standard.set(lastTarget, forKey: "lastHandoffTarget") }
    }
    @Published var loading = false
    @Published var error: String?
    @Published var notice: String?
    /// Sessions with an action in flight (e.g. a summarized handoff), by key.
    @Published var busy: Set<String> = []
    @Published var lastRefresh: Date?

    @Published var terminal: String = UserDefaults.standard.string(forKey: "terminal") ?? "Terminal" {
        didSet {
            UserDefaults.standard.set(terminal, forKey: "terminal")
            cli.terminal = terminal
        }
    }
    @Published var relaiPath: String = UserDefaults.standard.string(forKey: "relaiPath") ?? "" {
        didSet {
            UserDefaults.standard.set(relaiPath, forKey: "relaiPath")
            cli.overridePath = relaiPath
        }
    }

    let cli = RelaiCLI()
    private var noticeTask: Task<Void, Never>?

    init() {
        cli.terminal = terminal
        cli.overridePath = relaiPath
        cli.prepare()
    }

    /// Agents that have at least one session, with their counts.
    var agentCounts: [(agent: String, count: Int)] {
        Dictionary(grouping: sessions, by: \.agent)
            .map { (agent: $0.key, count: $0.value.count) }
            .sorted { Agents.order($0.agent) < Agents.order($1.agent) }
    }

    /// Where a session can be handed off to: every other agent that is installed
    /// or already has sessions, falling back to all known agents.
    func targets(for s: SessionItem) -> [String] {
        let present = installed.union(sessions.map(\.agent))
        var list = Agents.known.filter { $0 != s.agent && present.contains($0) }
        if list.isEmpty { list = Agents.known.filter { $0 != s.agent } }
        return list
    }

    /// The target ⌘↩ uses: the last one picked, else the first available.
    func defaultTarget(for s: SessionItem) -> String? {
        let list = targets(for: s)
        if let last = lastTarget, list.contains(last) { return last }
        return list.first
    }

    var selectedSession: SessionItem? { filtered.first { $0.key == selection } }

    var filtered: [SessionItem] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return sessions.filter { s in
            if let a = filter, s.agent != a { return false }
            if q.isEmpty { return true }
            return s.title.lowercased().contains(q)
                || s.project.lowercased().contains(q)
                || s.cwd.lowercased().contains(q)
                || s.id.lowercased().hasPrefix(q)
        }
    }

    // MARK: loading

    func refresh() async {
        guard !loading else { return }
        loading = true
        defer { loading = false }
        do {
            sessions = try await cli.listSessions()
            error = nil
            cliMissing = false
            lastRefresh = Date()
            if let f = filter, !sessions.contains(where: { $0.agent == f }) { filter = nil }
            if installed.isEmpty { installed = await cli.installedAgents() }
        } catch RelaiError.notFound {
            cliMissing = true
            sessions = []
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: selection and keyboard

    func ensureSelection() {
        let items = filtered
        if let s = selection, items.contains(where: { $0.key == s }) { return }
        selection = items.first?.key
    }

    func moveSelection(_ delta: Int) {
        hidePopout()
        let items = filtered
        guard !items.isEmpty else { return }
        let i = items.firstIndex { $0.key == selection } ?? (delta > 0 ? -1 : items.count)
        selection = items[min(max(i + delta, 0), items.count - 1)].key
    }

    func clearFilters() {
        query = ""
        filter = nil
    }

    // MARK: hover preview

    private var dwellTask: Task<Void, Never>?
    private var hoveredKey: String?
    private var previewTask: Task<Void, Never>?

    /// Called as the pointer moves over a row. The preview appears after the pointer
    /// has rested on the same row for 1.5 seconds.
    func hoverMoved(over s: SessionItem) {
        guard hoveredKey != s.key else { return }
        hoveredKey = s.key
        selection = s.key
        dwellTask?.cancel()
        PreviewPopout.hide()
        guard showPreview else { return }
        dwellTask = Task {
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            if Task.isCancelled { return }
            showPopout(for: s)
        }
    }

    func hoverEnded() {
        hoveredKey = nil
        dwellTask?.cancel()
        PreviewPopout.hide()
    }

    func hidePopout() {
        dwellTask?.cancel()
        PreviewPopout.hide()
    }

    /// ⌘P: show or hide the preview for the selected session right away.
    func togglePopoutForSelection() {
        if PreviewPopout.isShowing { hidePopout(); return }
        if let s = selectedSession { showPopout(for: s) }
    }

    private func showPopout(for s: SessionItem) {
        loadPreview(s)
        PreviewPopout.show(store: self, session: s)
    }

    private func loadPreview(_ s: SessionItem) {
        let cacheKey = s.key + "@" + s.updated
        if previews[cacheKey] != nil { return }
        previewTask?.cancel()
        previewTask = Task {
            do {
                let r = try await cli.run(["show", s.key, "--max-chars", "8000"])
                previews[cacheKey] = r.ok ? r.stdoutText : "Couldn't load a preview: \(RelaiCLI.clean(r.stderr))"
            } catch {
                previews[cacheKey] = "Couldn't load a preview: \(error.localizedDescription)"
            }
        }
    }

    func previewText(for s: SessionItem) -> String? { previews[s.key + "@" + s.updated] }

    func runDiagnostics() {
        Task {
            do {
                let r = try await cli.run(["doctor"])
                let body = r.stdoutText + (r.stderr.isEmpty ? "" : "\n" + r.stderr)
                PreviewWindow.show(title: "relai doctor", markdown: body)
            } catch {
                cliMissing = true
            }
        }
    }

    func chooseRelaiBinary() {
        let panel = NSOpenPanel()
        panel.message = "Choose the relai command"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.directoryURL = URL(fileURLWithPath: NSHomeDirectory() + "/go/bin")
        if panel.runModal() == .OK, let url = panel.url {
            relaiPath = url.path
            Task { await refresh() }
        }
    }

    // MARK: actions

    func resume(_ s: SessionItem) {
        perform(s, ["resume", s.key, "--new-window"],
                success: "Resuming in \(Agents.label(s.agent))")
    }

    func handoff(_ s: SessionItem, to target: String, summarize: Bool = false) {
        if target != s.agent { lastTarget = target }
        var args = ["handoff", s.key, "--to", target, "--new-window"]
        if summarize { args.append("--summarize") }
        let what = target == s.agent ? "a fresh \(Agents.label(target)) session" : Agents.label(target)
        perform(s, args, success: "Handed off to \(what)",
                progress: summarize ? "Writing a briefing…" : nil)
    }

    func preview(_ s: SessionItem) {
        Task {
            busy.insert(s.key)
            defer { busy.remove(s.key) }
            do {
                let r = try await cli.run(["show", s.key, "--max-chars", "40000"])
                guard r.ok else { throw RelaiError.failed(RelaiCLI.clean(r.stderr)) }
                PreviewWindow.show(title: "\(Agents.shortLabel(s.agent)) · \(s.displayTitle)", markdown: r.stdoutText)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    func copyResumeCommand(_ s: SessionItem) {
        let cmd = s.cwd.isEmpty ? s.resume_cmd : "cd \(shellQuote(s.cwd)) && \(s.resume_cmd)"
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(cmd, forType: .string)
        flash("Copied resume command")
    }

    func openProject(_ s: SessionItem) {
        guard !s.cwd.isEmpty else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: s.cwd))
    }

    func revealHandoffs() {
        let dir = (ProcessInfo.processInfo.environment["RELAI_HOME"] ?? "\(NSHomeDirectory())/.relai") + "/handoffs"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        NSWorkspace.shared.open(URL(fileURLWithPath: dir))
    }

    private func perform(_ s: SessionItem, _ args: [String], success: String, progress: String? = nil) {
        hidePopout()
        Task {
            busy.insert(s.key)
            if let progress { notice = progress }
            defer { busy.remove(s.key) }
            do {
                let r = try await cli.run(args)
                guard r.ok else { throw RelaiError.failed(RelaiCLI.clean(r.stderr)) }
                error = nil
                flash(success)
                await refresh()
            } catch {
                notice = nil
                self.error = error.localizedDescription
            }
        }
    }

    func flash(_ message: String) {
        notice = message
        noticeTask?.cancel()
        noticeTask = Task {
            try? await Task.sleep(nanoseconds: 3_000_000_000)
            if !Task.isCancelled { notice = nil }
        }
    }

    private func shellQuote(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    // MARK: launch at login

    var launchAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue {
                    try SMAppService.mainApp.register()
                } else {
                    try SMAppService.mainApp.unregister()
                }
            } catch {
                self.error = "Couldn't change Launch at Login: \(error.localizedDescription)"
            }
            objectWillChange.send()
        }
    }
}
