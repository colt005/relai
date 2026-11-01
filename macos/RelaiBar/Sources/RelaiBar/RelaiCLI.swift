import Foundation

/// One session as reported by `relai list --json` (schema 1).
struct SessionItem: Decodable, Hashable {
    let agent: String
    let id: String
    let path: String
    let cwd: String
    let title: String
    let branch: String?
    let started: String
    let updated: String
    let hidden: Bool?
    let resume_cmd: String

    /// "agent:id", the reference relai accepts everywhere.
    var key: String { "\(agent):\(id)" }
    var project: String { cwd.isEmpty ? "?" : (cwd as NSString).lastPathComponent }
    var updatedDate: Date? { RelaiCLI.parseDate(updated) }
    var displayTitle: String { title.isEmpty ? "(untitled)" : title }
}

private struct ListResponse: Decodable {
    let schema: Int
    let sessions: [SessionItem]
}

struct CLIResult {
    let status: Int32
    let stdout: Data
    let stderr: String
    var ok: Bool { status == 0 }
    var stdoutText: String { String(decoding: stdout, as: UTF8.self) }
}

enum RelaiError: LocalizedError {
    case notFound
    case failed(String)
    case unsupportedSchema(Int)

    var errorDescription: String? {
        switch self {
        case .notFound:
            return "Couldn't find the relai command. Install it (go install ./cmd/relai) or set its path in Settings."
        case .failed(let msg):
            return msg
        case .unsupportedSchema(let v):
            return "This version of RelaiBar doesn't understand relai's output (schema \(v)). Update RelaiBar."
        }
    }
}

/// Runs the relai CLI. GUI apps don't inherit the shell's PATH, so the user's
/// login-shell PATH is captured once and passed to every relai invocation; relai
/// needs it to find `claude` and `codex`.
final class RelaiCLI {
    static let supportedSchema = 1

    var overridePath: String = ""
    var terminal: String = "Terminal"

    // The login shell is slow to start, so its PATH is captured once, lazily,
    // off the main thread.
    private let pathQueue = DispatchQueue(label: "relai.shell-path")
    private var cachedPATH: String??

    var shellPATH: String? {
        pathQueue.sync {
            if let cached = cachedPATH { return cached }
            let p = Self.captureLoginShellPATH()
            cachedPATH = .some(p)
            return p
        }
    }

    /// Warm the PATH cache in the background at launch.
    func prepare() {
        DispatchQueue.global(qos: .utility).async { _ = self.shellPATH }
    }

    // MARK: locating relai

    /// Call off the main thread: may need the login shell's PATH.
    func resolveBinary(_ overridePath: String) -> String? {
        let fm = FileManager.default
        if !overridePath.isEmpty {
            let p = (overridePath as NSString).expandingTildeInPath
            return fm.isExecutableFile(atPath: p) ? p : nil
        }
        let home = NSHomeDirectory()
        var candidates = [
            "\(home)/go/bin/relai",
            "/opt/homebrew/bin/relai",
            "/usr/local/bin/relai",
            "\(home)/.local/bin/relai",
        ]
        for dir in (shellPATH ?? "").split(separator: ":") {
            candidates.append("\(dir)/relai")
        }
        return candidates.first { fm.isExecutableFile(atPath: $0) }
    }

    private func environment(terminal: String) -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        let base = shellPATH ?? env["PATH"] ?? "/usr/bin:/bin"
        env["PATH"] = base + ":/opt/homebrew/bin:/usr/local/bin:\(NSHomeDirectory())/go/bin"
        env["RELAI_TERMINAL"] = terminal
        return env
    }

    /// Agents whose command is on the user's PATH.
    func installedAgents() async -> Set<String> {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .utility).async {
                let fm = FileManager.default
                let home = NSHomeDirectory()
                var dirs = (self.shellPATH ?? "").split(separator: ":").map(String.init)
                dirs += ["/opt/homebrew/bin", "/usr/local/bin", "\(home)/.local/bin", "\(home)/.claude/local"]
                let found = Agents.known.filter { agent in
                    dirs.contains { fm.isExecutableFile(atPath: "\($0)/\(Agents.binary(agent))") }
                }
                cont.resume(returning: Set(found))
            }
        }
    }

    // MARK: commands

    func listSessions(limit: Int = 300) async throws -> [SessionItem] {
        let r = try await run(["list", "--json", "-n", String(limit)])
        guard r.ok else { throw RelaiError.failed(Self.clean(r.stderr)) }
        let resp = try JSONDecoder().decode(ListResponse.self, from: r.stdout)
        guard resp.schema == Self.supportedSchema else { throw RelaiError.unsupportedSchema(resp.schema) }
        return resp.sessions
    }

    func run(_ args: [String]) async throws -> CLIResult {
        let override = overridePath, term = terminal
        return try await withCheckedThrowingContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async {
                guard let bin = self.resolveBinary(override) else {
                    cont.resume(throwing: RelaiError.notFound)
                    return
                }
                cont.resume(returning: Self.runSync(bin, args, env: self.environment(terminal: term), timeout: 600))
            }
        }
    }

    // MARK: helpers

    static func runSync(_ bin: String, _ args: [String], env: [String: String]?, timeout: TimeInterval) -> CLIResult {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: bin)
        p.arguments = args
        if let env { p.environment = env }
        p.currentDirectoryURL = URL(fileURLWithPath: NSHomeDirectory())
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = FileHandle.nullDevice
        do {
            try p.run()
        } catch {
            return CLIResult(status: -1, stdout: Data(), stderr: error.localizedDescription)
        }
        let timer = DispatchWorkItem { if p.isRunning { p.terminate() } }
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: timer)

        // Drain both pipes concurrently so a chatty process can't block on a full pipe.
        var errData = Data()
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            errData = err.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        let outData = out.fileHandleForReading.readDataToEndOfFile()
        group.wait()
        p.waitUntilExit()
        timer.cancel()
        return CLIResult(status: p.terminationStatus, stdout: outData,
                         stderr: String(decoding: errData, as: UTF8.self))
    }

    /// Asks the user's login shell for its PATH (as set up in .zprofile/.zshrc).
    static func captureLoginShellPATH() -> String? {
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        let marker = "__RELAI_PATH__"
        let r = runSync(shell, ["-ilc", "printf '\(marker)%s\(marker)' \"$PATH\""], env: nil, timeout: 5)
        let text = r.stdoutText
        guard let start = text.range(of: marker),
              let end = text.range(of: marker, range: start.upperBound..<text.endIndex) else { return nil }
        let path = String(text[start.upperBound..<end.lowerBound])
        return path.isEmpty ? nil : path
    }

    /// Go writes RFC 3339 with up to nanosecond precision; drop the fraction
    /// so ISO8601DateFormatter accepts it.
    static func parseDate(_ s: String) -> Date? {
        let trimmed = s.replacingOccurrences(of: #"\.\d+"#, with: "", options: .regularExpression)
        guard let d = isoFormatter.date(from: trimmed), d.timeIntervalSince1970 > 0 else { return nil }
        return d
    }

    private static let isoFormatter = ISO8601DateFormatter()

    /// Strips the "relai: " prefix from error messages.
    static func clean(_ stderr: String) -> String {
        let lines = stderr.split(separator: "\n").map { line -> String in
            line.hasPrefix("relai: ") ? String(line.dropFirst(7)) : String(line)
        }
        let msg = lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        return msg.isEmpty ? "relai failed" : msg
    }
}
