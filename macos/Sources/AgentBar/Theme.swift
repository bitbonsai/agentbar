import SwiftUI
import AppKit

enum ThemeKind: String, CaseIterable, Identifiable {
    case system
    case catppuccinAuto
    case catppuccinMocha
    case catppuccinLatte

    static let storageKey = "agentbar.theme"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .system: return "System"
        case .catppuccinAuto: return "Catppuccin (Auto)"
        case .catppuccinMocha: return "Catppuccin Mocha"
        case .catppuccinLatte: return "Catppuccin Latte"
        }
    }

    /// nil follows macOS; the forced variants pin the window appearance.
    var appearanceName: NSAppearance.Name? {
        switch self {
        case .system, .catppuccinAuto: return nil
        case .catppuccinMocha: return .darkAqua
        case .catppuccinLatte: return .aqua
        }
    }

    var isSystem: Bool { self == .system }
}

enum MenuBarStyle: String, CaseIterable, Identifiable {
    case iconOnly
    case iconAndPercent
    case hidden

    static let storageKey = "agentbar.menubarStyle"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .iconOnly: return "Icon only"
        case .iconAndPercent: return "Icon + %"
        case .hidden: return "Hidden"
        }
    }
}

/// Colors resolved for the active appearance. Catppuccin follows the OS mode:
/// Mocha in dark, Latte in light unless a variant is forced.
struct Palette {
    let background: Color
    let surface: Color
    let text: Color
    let muted: Color
    let divider: Color
    let track: Color
    let ok: Color
    let warn: Color
    let critical: Color
    let accent: Color

    static func resolve(_ kind: ThemeKind, _ scheme: ColorScheme) -> Palette {
        switch kind {
        case .system:
            // Soft desaturated semantic bars tuned for translucent dark/light glass.
            return Palette(
                background: Color(nsColor: .windowBackgroundColor),
                surface: Color(nsColor: .controlBackgroundColor),
                text: Color(nsColor: .labelColor),
                muted: Color(nsColor: .secondaryLabelColor),
                divider: Color(nsColor: .separatorColor),
                track: Color(nsColor: .quaternaryLabelColor).opacity(0.35),
                ok: scheme == .dark ? Color(hex: 0x6FCF97) : Color(hex: 0x2E8B57),
                warn: scheme == .dark ? Color(hex: 0xE5B567) : Color(hex: 0xC4841D),
                critical: scheme == .dark ? Color(hex: 0xE06C75) : Color(hex: 0xC93B48),
                accent: Color(nsColor: .controlAccentColor)
            )
        case .catppuccinAuto, .catppuccinMocha, .catppuccinLatte:
            let dark = kind == .catppuccinMocha || (kind == .catppuccinAuto && scheme == .dark)
            return dark ? Palette(
                background: Color(hex: 0x1E1E2E), // Mocha base
                surface: Color(hex: 0x313244),    // surface0
                text: Color(hex: 0xCDD6F4),       // text
                muted: Color(hex: 0x9399B2),      // overlay2
                divider: Color(hex: 0x313244),
                track: Color(hex: 0x313244),
                ok: Color(hex: 0xA6E3A1),         // green
                warn: Color(hex: 0xFAB387),       // peach
                critical: Color(hex: 0xF38BA8),   // red
                accent: Color(hex: 0xCBA6F7)      // mauve
            ) : Palette(
                background: Color(hex: 0xEFF1F5), // Latte base
                surface: Color(hex: 0xE6E9EF),    // mantle
                text: Color(hex: 0x4C4F69),       // text
                muted: Color(hex: 0x8C8FA1),      // overlay1
                divider: Color(hex: 0xCCD0DA),
                track: Color(hex: 0xCCD0DA),
                ok: Color(hex: 0x40A02B),         // green
                warn: Color(hex: 0xFE640B),       // peach
                critical: Color(hex: 0xD20F39),   // red
                accent: Color(hex: 0x8839EF)      // mauve
            )
        }
    }

    func barColor(remaining: Double) -> Color {
        if remaining < 20 { return critical }
        if remaining < 50 { return warn }
        return ok
    }
}

extension Color {
    init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: 1
        )
    }
}

enum Typography {
    static func mono(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .system(size: size, weight: weight, design: .monospaced)
    }
}
