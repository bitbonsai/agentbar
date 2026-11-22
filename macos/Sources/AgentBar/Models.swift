import Foundation

// Mirror of the collector's normalized schema (collector/internal/schema).

struct Snapshot: Codable, Sendable {
    let updatedAt: String
    let providers: [Provider]

    var primaryProvider: Provider? {
        providers.first { $0.status == "ok" }
    }
}

struct Provider: Codable, Identifiable, Sendable {
    let id: String
    let name: String
    let account: String
    let status: String
    let error: String?
    let groups: [ModelGroup]
}

struct ModelGroup: Codable, Identifiable, Sendable {
    let id: String
    let name: String
    let groupDescription: String
    let windows: [QuotaWindow]

    enum CodingKeys: String, CodingKey {
        case id, name, windows
        case groupDescription = "description"
    }

    /// Five-hour first, then weekly, then monthly, matching the collector.
    private static let windowRank = ["five_hour": 0, "weekly": 1, "monthly": 2]

    var sortedWindows: [QuotaWindow] {
        windows.sorted { lhs, rhs in
            let left = Self.windowRank[lhs.id] ?? Self.windowRank.count
            let right = Self.windowRank[rhs.id] ?? Self.windowRank.count
            if left != right { return left < right }
            return lhs.label < rhs.label
        }
    }
}

struct QuotaWindow: Codable, Identifiable, Sendable {
    let id: String
    let label: String
    let rawLabel: String
    let remainingPct: Double
    let usedPct: Double
    let statusText: String
    let resetsInMinutes: Int?
    let windowMinutes: Int
    let paceRemainingPct: Double?

    var resetText: String {
        guard let minutes = resetsInMinutes else { return "Ready" }
        return QuotaWindow.format(minutes)
    }

    static func format(_ minutes: Int) -> String {
        guard minutes > 0 else { return "Ready" }
        let days = minutes / (24 * 60)
        let hours = (minutes % (24 * 60)) / 60
        let mins = minutes % 60
        if days > 0 { return "\(days)d \(hours)h" }
        if hours > 0 && mins > 0 { return "\(hours)h \(mins)m" }
        if hours > 0 { return "\(hours)h" }
        return "\(mins)m"
    }
}
