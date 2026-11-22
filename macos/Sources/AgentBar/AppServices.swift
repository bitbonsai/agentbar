import AppKit
import Foundation
import ServiceManagement

/// Native macOS 13+ Login Item toggle via SMAppService.
enum LaunchAtLogin {
    static var isEnabled: Bool {
        SMAppService.mainApp.status == .enabled
    }

    static func setEnabled(_ enabled: Bool) {
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            let alert = NSAlert()
            alert.messageText = "Could not update Start at Login"
            alert.informativeText = error.localizedDescription
            alert.alertStyle = .warning
            alert.runModal()
        }
    }
}

/// Checks GitHub Releases when `AgentBarUpdateRepo` (e.g. "owner/agentbar") is
/// configured in Info.plist.
@MainActor
enum UpdateChecker {
    private struct GitHubRelease: Decodable {
        let tagName: String
        let htmlURL: String

        enum CodingKeys: String, CodingKey {
            case tagName = "tag_name"
            case htmlURL = "html_url"
        }
    }

    static var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    static var repoSlug: String? {
        guard let raw = Bundle.main.object(forInfoDictionaryKey: "AgentBarUpdateRepo") as? String else {
            return nil
        }
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    static func checkForUpdates() {
        guard let repo = repoSlug,
              let url = URL(string: "https://api.github.com/repos/\(repo)/releases/latest") else {
            showAlert(
                title: "AgentBar \(currentVersion)",
                message: "No GitHub release feed is configured yet. Set AgentBarUpdateRepo (e.g. owner/agentbar) in Info.plist once published.",
                openURL: nil
            )
            return
        }

        Task {
            do {
                var request = URLRequest(url: url)
                request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
                request.setValue("AgentBar/\(currentVersion)", forHTTPHeaderField: "User-Agent")
                request.timeoutInterval = 10

                let (data, response) = try await URLSession.shared.data(for: request)
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                    showAlert(
                        title: "Could not check for updates",
                        message: "GitHub returned an unexpected response for \(repo).",
                        openURL: nil
                    )
                    return
                }

                let release = try JSONDecoder().decode(GitHubRelease.self, from: data)
                let latest = release.tagName.trimmingCharacters(in: CharacterSet(charactersIn: "vV"))
                if isNewer(latest, than: currentVersion) {
                    showAlert(
                        title: "Update Available — v\(latest)",
                        message: "You are running AgentBar v\(currentVersion).",
                        openURL: URL(string: release.htmlURL)
                    )
                } else {
                    showAlert(
                        title: "You’re up to date",
                        message: "AgentBar v\(currentVersion) is the latest version.",
                        openURL: nil
                    )
                }
            } catch {
                showAlert(
                    title: "Could not check for updates",
                    message: error.localizedDescription,
                    openURL: nil
                )
            }
        }
    }

    private static func isNewer(_ a: String, than b: String) -> Bool {
        let pa = a.split(separator: ".").compactMap { Int($0) }
        let pb = b.split(separator: ".").compactMap { Int($0) }
        let n = max(pa.count, pb.count)
        for i in 0..<n {
            let va = i < pa.count ? pa[i] : 0
            let vb = i < pb.count ? pb[i] : 0
            if va != vb { return va > vb }
        }
        return false
    }

    private static func showAlert(title: String, message: String, openURL: URL?) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .informational
        if openURL != nil {
            alert.addButton(withTitle: "Open Release")
            alert.addButton(withTitle: "Later")
        } else {
            alert.addButton(withTitle: "OK")
        }
        let resp = alert.runModal()
        if resp == .alertFirstButtonReturn, let openURL {
            NSWorkspace.shared.open(openURL)
        }
    }
}
