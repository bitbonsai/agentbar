import Foundation
import SwiftUI

enum LoadState: Equatable, Sendable {
    case loading
    case ready
    case notSignedIn
    case failed(String)
}

@MainActor
final class AgentBarModel: ObservableObject {
    @Published private(set) var snapshot: Snapshot?
    @Published private(set) var state: LoadState = .loading
    @Published private(set) var lastUpdated: Date?
    @Published private(set) var isRefreshing = false

    private var timer: Timer?
    private var inFlight = false

    init() {
        refresh(force: false)
        timer = Timer.scheduledTimer(withTimeInterval: 300, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh(force: true) }
        }
        if let timer { RunLoop.main.add(timer, forMode: .common) }
    }

    deinit { timer?.invalidate() }

    // MARK: - Derived display values

    var providers: [Provider] { snapshot?.providers ?? [] }
    var okProviders: [Provider] { providers.filter { $0.status == "ok" } }

    /// First healthy provider, for headers that need one identity.
    var provider: Provider? { okProviders.first ?? providers.first }
    var groups: [ModelGroup] { provider?.groups ?? [] }

    var account: String {
        if let account = provider?.account, !account.isEmpty { return account }
        return "Antigravity"
    }

    var updatedText: String {
        guard let lastUpdated else { return "waiting for first read" }
        let seconds = Int(Date().timeIntervalSince(lastUpdated))
        switch seconds {
        case ..<60: return "updated now"
        case ..<3600: return "updated \(seconds / 60)m ago"
        default: return "updated \(seconds / 3600)h ago"
        }
    }

    /// Lowest remaining % across every healthy provider, truncated, as `39%`.
    var menuBarText: String {
        let remaining = okProviders
            .flatMap { $0.groups }
            .flatMap { $0.windows }
            .map(\.remainingPct)
        guard let lowest = remaining.min() else { return "--" }
        return "\(Int(lowest))%"
    }

    // MARK: - Loading

    func refresh(force: Bool) {
        guard !inFlight else { return }
        inFlight = true
        isRefreshing = true
        if snapshot == nil { state = .loading }
        Task.detached(priority: .utility) {
            let result = Self.load(force: force)
            await MainActor.run {
                self.apply(result)
                self.inFlight = false
                self.isRefreshing = false
            }
        }
    }

    private func apply(_ result: LoadResult) {
        switch result {
        case .success(let snapshot, let state):
            self.snapshot = snapshot
            self.state = state
            if state == .ready || state == .notSignedIn {
                self.lastUpdated = Date()
            }
        case .failure(let message):
            self.snapshot = nil
            self.state = .failed(message)
        }
    }

    private enum LoadResult: Sendable {
        case success(Snapshot, LoadState)
        case failure(String)
    }

    nonisolated private static func load(force: Bool) -> LoadResult {
        guard let binary = collectorURL() else {
            return .failure("agentbar-collector not found. Run macos/build-app.sh or install it into ~/.local/bin.")
        }

        var args = ["--json"]
        if force { args.append("--refresh") }

        let process = Process()
        process.executableURL = binary
        process.arguments = args
        let stdout = Pipe()
        let stderr = Pipe()
        process.standardOutput = stdout
        process.standardError = stderr

        do {
            try process.run()
        } catch {
            return .failure("could not start collector: \(error.localizedDescription)")
        }
        let data = stdout.fileHandleForReading.readDataToEndOfFile()
        let errData = stderr.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        guard !data.isEmpty else {
            let message = String(data: errData, encoding: .utf8) ?? ""
            return .failure(message.isEmpty ? "collector produced no output" : message)
        }
        do {
            let snapshot = try JSONDecoder().decode(Snapshot.self, from: data)
            guard !snapshot.providers.isEmpty else {
                return .failure("collector returned no providers")
            }
            if snapshot.providers.contains(where: { $0.status == "ok" }) {
                return .success(snapshot, .ready)
            }
            if snapshot.providers.contains(where: { $0.status == "not_signed_in" }) {
                return .success(snapshot, .notSignedIn)
            }
            let message = snapshot.providers.compactMap(\.error).first
                ?? "collector reported an error"
            return .failure(message)
        } catch {
            return .failure("could not decode collector output: \(error.localizedDescription)")
        }
    }

    /// Bundled helper first, then the usual install locations.
    nonisolated private static func collectorURL() -> URL? {
        var candidates: [URL] = []
        if let helpers = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/agentbar-collector") as URL? {
            candidates.append(helpers)
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        candidates.append(home.appendingPathComponent(".local/bin/agentbar-collector"))
        candidates.append(URL(fileURLWithPath: "/opt/homebrew/bin/agentbar-collector"))
        candidates.append(URL(fileURLWithPath: "/usr/local/bin/agentbar-collector"))
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0.path) }
    }
}

// MARK: - Terminal hand-off

enum TerminalLauncher {
    /// CLI to run for each provider id.
    private static let commands = [
        "antigravity": "agy",
        "opencode": "opencode",
        "claude": "claude",
        "codex": "codex"
    ]

    /// Opens a terminal running the provider's CLI so the user can sign in or
    /// use it directly.
    static func open(providerID: String) {
        let command = commands[providerID] ?? "agy"
        let script = "cd ~ && \(command)"
        let workspace = NSWorkspace.shared

        if let url = workspace.urlForApplication(withBundleIdentifier: "com.mitchellh.ghostty") {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/bin/open")
            process.arguments = ["-na", url.path, "--args", "-e", command]
            if (try? process.run()) != nil { return }
        }

        if workspace.urlForApplication(withBundleIdentifier: "com.googlecode.iterm2") != nil {
            let source = """
            tell application "iTerm"
                activate
                set newWindow to (create window with default profile)
                tell current session of newWindow to write text "\(script)"
            end tell
            """
            if runAppleScript(source) { return }
        }

        let source = """
        tell application "Terminal"
            activate
            do script "\(script)"
        end tell
        """
        if runAppleScript(source) { return }
        _ = runAppleScript("tell application \"Terminal\" to activate")
    }

    /// Antigravity is the default hand-off (⌘O).
    static func openAGY() {
        open(providerID: "antigravity")
    }

    @discardableResult
    private static func runAppleScript(_ source: String) -> Bool {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        process.arguments = ["-e", source]
        process.standardError = Pipe()
        do {
            try process.run()
            process.waitUntilExit()
            return process.terminationStatus == 0
        } catch {
            return false
        }
    }
}
