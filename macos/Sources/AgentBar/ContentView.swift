import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: AgentBarModel
    @Environment(\.colorScheme) private var scheme
    @AppStorage(ThemeKind.storageKey) private var themeRaw = ThemeKind.system.rawValue
    @AppStorage(MenuBarStyle.storageKey) private var menubarStyleRaw = MenuBarStyle.iconOnly.rawValue
    @AppStorage("agentbar.showEmptyProviders") private var showEmptyProviders = false
    @State private var manualCollapse: [String: Bool] = [:]
    @State private var startAtLogin = LaunchAtLogin.isEnabled
    @State private var contentHeight: CGFloat = 0

    private var theme: ThemeKind { ThemeKind(rawValue: themeRaw) ?? .system }
    private var palette: Palette { .resolve(theme, scheme) }

    /// Auto-expand providers that need attention (<50% remaining or non-ok status);
    /// keep healthy providers neatly collapsed unless the user toggles them.
    private func isProviderCollapsed(_ provider: Provider) -> Bool {
        if let override = manualCollapse[provider.id] {
            return override
        }
        if provider.status != "ok" { return false }
        let lowest = provider.groups
            .flatMap(\.windows)
            .map(\.remainingPct)
            .min() ?? 100
        return lowest >= 50
    }

    private func toggleProvider(_ provider: Provider) {
        manualCollapse[provider.id] = !isProviderCollapsed(provider)
    }

    private var visibleProviders: [Provider] {
        model.providers.filter { provider in
            showEmptyProviders || !provider.groups.isEmpty || provider.status != "ok"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            bodyContent
                .padding(.horizontal, 12)
                .padding(.top, 2)
                .padding(.bottom, 12)
        }
        .frame(width: 420)
        .background(panelBackground)
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(hairlineBorder)
        .onAppear { model.refresh(force: false) }
        .onExitCommand { NotificationCenter.default.post(name: .agentBarHideHUD, object: nil) }
    }

    // MARK: - Chrome

    private var panelBackground: some View {
        Group {
            if theme.isSystem {
                Color.clear
            } else {
                // Keep themed glass translucent so the NSVisualEffectView blur shines through.
                palette.background.opacity(scheme == .dark ? 0.38 : 0.48)
            }
        }
    }

    private var hairlineBorder: some View {
        ZStack(alignment: .top) {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .strokeBorder(palette.text.opacity(0.12), lineWidth: 1)
            LinearGradient(
                colors: [Color.white.opacity(0.16), Color.white.opacity(0)],
                startPoint: .leading,
                endPoint: .trailing
            )
            .frame(height: 1)
            .padding(.horizontal, 16)
        }
        .allowsHitTesting(false)
    }

    // MARK: - Header

    private var header: some View {
        HStack(alignment: .top, spacing: 9) {
            icon
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 1) {
                Text("Agents")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(palette.text)
                Text(subtitle)
                    .font(.system(size: 10))
                    .foregroundStyle(palette.muted)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: 8)
            HStack(spacing: 4) {
                refreshButton
                settingsMenu
            }
            .padding(.top, 1)
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 10)
    }

    @ViewBuilder
    private var icon: some View {
        if let image = IconLoader.headerImage() {
            Image(nsImage: image)
                .renderingMode(.template)
                .resizable()
                .interpolation(.high)
                .aspectRatio(contentMode: .fit)
                .foregroundStyle(palette.text)
                .frame(width: 20, height: 20)
        } else {
            Image(systemName: "sparkles")
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(palette.accent)
                .frame(width: 20, height: 20)
        }
    }

    private var subtitle: String {
        statusLine
    }

    private var refreshButton: some View {
        Button { model.refresh(force: true) } label: {
            Image(systemName: "arrow.clockwise")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(model.isRefreshing ? palette.accent : palette.muted)
                .frame(width: 22, height: 22)
                .rotationEffect(.degrees(model.isRefreshing ? 360 : 0))
                .animation(
                    model.isRefreshing
                        ? .linear(duration: 0.9).repeatForever(autoreverses: false)
                        : .easeOut(duration: 0.2),
                    value: model.isRefreshing
                )
                .contentShape(Rectangle())
        }
        .buttonStyle(HoverButtonStyle(palette: palette, radius: 6, horizontal: 0, vertical: 0))
        .help("Refresh quota (⌘R)")
    }

    private var settingsMenu: some View {
        Menu {
            Button("Check for Updates…") { UpdateChecker.checkForUpdates() }
            Toggle("Start at Login", isOn: Binding(
                get: { startAtLogin },
                set: { newValue in
                    LaunchAtLogin.setEnabled(newValue)
                    startAtLogin = LaunchAtLogin.isEnabled
                }
            ))
            Divider()
            Picker("Theme", selection: $themeRaw) {
                ForEach(ThemeKind.allCases) { kind in
                    Text(kind.label).tag(kind.rawValue)
                }
            }
            .pickerStyle(.inline)
            Divider()
            Picker("Menubar", selection: $menubarStyleRaw) {
                ForEach(MenuBarStyle.allCases) { style in
                    Text(style.label).tag(style.rawValue)
                }
            }
            .pickerStyle(.inline)
            Divider()
            Toggle("Show providers without limits", isOn: $showEmptyProviders)
            Divider()
            Button("Open CLI") { TerminalLauncher.openAGY() }
            Button("Quit AgentBar") { NSApp.terminate(nil) }
        } label: {
            Image(systemName: "slider.horizontal.3")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(palette.muted)
                .frame(width: 22, height: 22)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Theme and menubar style")
    }

    // MARK: - Body

    @ViewBuilder
    private var bodyContent: some View {
        switch model.state {
        case .loading:
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Reading quota…")
                    .font(.system(size: 11))
                    .foregroundStyle(palette.muted)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.vertical, 8)

        case .notSignedIn:
            emptyState(
                icon: "person.crop.circle.badge.exclamationmark",
                title: "Not signed in",
                detail: "Sign in with one of your agent CLIs to read quota.",
                actionTitle: "Open CLI"
            )

        case .failed(let message):
            emptyState(
                icon: "exclamationmark.triangle",
                title: "Collector error",
                detail: message,
                actionTitle: "Retry"
            )

        case .ready:
            providerList
        }
    }

    private var providerList: some View {
        ScrollView(.vertical, showsIndicators: true) {
            VStack(alignment: .leading, spacing: 10) {
                ForEach(visibleProviders) { provider in
                    providerCard(provider)
                }
            }
            .background(
                GeometryReader { geo in
                    Color.clear.preference(key: ContentHeightKey.self, value: geo.size.height)
                }
            )
        }
        .onPreferenceChange(ContentHeightKey.self) { height in
            guard abs(height - contentHeight) > 1 else { return }
            contentHeight = height
            // The HUD sizes itself to this view, so ask it to re-measure.
            NotificationCenter.default.post(name: .agentBarContentResize, object: nil)
        }
        .frame(height: min(max(contentHeight, 1), 640))
    }

    private func providerCard(_ provider: Provider) -> some View {
        let isCollapsed = isProviderCollapsed(provider)

        return VStack(alignment: .leading, spacing: 0) {
            providerHeader(provider: provider, isCollapsed: isCollapsed)
            if isCollapsed {
                collapsedProviderSummary(provider: provider)
                    .padding(.horizontal, 10)
                    .padding(.bottom, 9)
            } else if provider.groups.isEmpty {
                Text("No quota limits reported")
                    .font(.system(size: 10))
                    .foregroundStyle(palette.muted)
                    .padding(.horizontal, 10)
                    .padding(.bottom, 10)
            } else {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(provider.groups) { group in
                        groupSection(group, showHeader: provider.groups.count > 1)
                    }
                }
                .padding(.bottom, 4)
            }
        }
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(palette.text.opacity(0.045))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(palette.text.opacity(0.07), lineWidth: 1)
        )
        .animation(.easeInOut(duration: 0.16), value: isCollapsed)
    }

    private func isMonochromeProviderIcon(_ id: String) -> Bool {
        switch id.lowercased() {
        case "opencode", "codex", "cursor", "grok":
            return true
        default:
            return false
        }
    }

    private func providerHeader(provider: Provider, isCollapsed: Bool) -> some View {
        HStack(spacing: 8) {
            Button { toggleProvider(provider) } label: {
                HStack(spacing: 8) {
                    if let img = IconLoader.providerImage(id: provider.id) {
                        if isMonochromeProviderIcon(provider.id) {
                            Image(nsImage: img)
                                .renderingMode(.template)
                                .resizable()
                                .interpolation(.high)
                                .aspectRatio(contentMode: .fit)
                                .foregroundStyle(palette.text.opacity(0.9))
                                .frame(width: 17, height: 17)
                        } else {
                            Image(nsImage: img)
                                .resizable()
                                .interpolation(.high)
                                .aspectRatio(contentMode: .fit)
                                .frame(width: 17, height: 17)
                        }
                    }
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 5) {
                            Text(provider.name)
                                .font(.system(size: 12, weight: .semibold))
                                .foregroundStyle(palette.text)
                            Image(systemName: isCollapsed ? "chevron.right" : "chevron.down")
                                .font(.system(size: 8.5, weight: .semibold))
                                .foregroundStyle(palette.muted.opacity(0.75))
                            if provider.status != "ok" {
                                pill(statusText: provider.status)
                            }
                        }
                        if !provider.account.isEmpty {
                            Text(provider.account)
                                .font(.system(size: 9.5))
                                .foregroundStyle(palette.muted)
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                    }
                    Spacer(minLength: 4)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(isCollapsed ? "Expand \(provider.name)" : "Collapse \(provider.name)")

            Button { TerminalLauncher.open(providerID: provider.id) } label: {
                Text(">_")
                    .font(Typography.mono(10, .semibold))
                    .foregroundStyle(palette.accent)
                    .frame(width: 26, height: 19)
                    .background(
                        RoundedRectangle(cornerRadius: 5, style: .continuous)
                            .fill(palette.text.opacity(0.06))
                    )
                    .contentShape(Rectangle())
            }
            .buttonStyle(HoverButtonStyle(palette: palette, radius: 5, horizontal: 0, vertical: 0))
            .help("Open \(provider.name) in your terminal")
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    private func collapsedProviderSummary(provider: Provider) -> some View {
        HStack(spacing: 6) {
            ForEach(provider.groups) { group in
                if let tightest = group.windows.min(by: { $0.remainingPct < $1.remainingPct }) {
                    HStack(spacing: 4) {
                        Circle()
                            .fill(palette.barColor(remaining: tightest.remainingPct))
                            .frame(width: 6, height: 6)
                        Text(shortGroupName(group.name))
                            .font(.system(size: 10, weight: .medium))
                            .foregroundStyle(palette.muted)
                        Text(String(format: "%.0f%%", tightest.remainingPct))
                            .font(Typography.mono(10, .semibold))
                            .monospacedDigit()
                            .foregroundStyle(palette.text)
                    }
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(
                        Capsule().fill(palette.text.opacity(0.05))
                    )
                }
            }
            Spacer(minLength: 0)
        }
    }

    private func shortGroupName(_ name: String) -> String {
        let lower = name.lowercased()
        if lower.contains("argon") { return "Argon" }
        if lower.contains("gemini") { return "Neon" }
        if lower.contains("claude") { return "Claude" }
        return name.split(separator: " ").first.map(String.init) ?? name
    }

    private func pill(statusText: String) -> some View {
        let tint: Color = statusText == "ok" ? palette.ok : palette.warn
        let label = statusText == "ok" ? "OK" : statusText.uppercased()
        return Text(label)
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(tint)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Capsule().fill(tint.opacity(0.14)))
    }

    /// Extracts the primary model name + secondary models from agy's
    /// comma-separated model list in `groupDescription`.
    private func groupTitleParts(_ group: ModelGroup) -> (primary: String, secondary: String?) {
        var desc = group.groupDescription.trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = "Models within this group:"
        if let range = desc.range(of: prefix, options: .caseInsensitive) {
            desc = String(desc[range.upperBound...]).trimmingCharacters(in: .whitespacesAndNewlines)
        }
        // Only treat as a model list if it doesn't read like a prose sentence (allow version dots like 5.5 / 3.8).
        if !desc.isEmpty, !desc.contains(". "), !desc.hasPrefix("For "), desc.contains(",") {
            let list = desc
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
            if let first = list.first {
                let rest = list.dropFirst()
                    .filter { !$0.localizedCaseInsensitiveContains("orchestrator") }
                    .map { item -> String in
                        item
                            .replacingOccurrences(of: "Gemini 4 ", with: "")
                            .replacingOccurrences(of: "Gemini 3.8 ", with: "")
                            .replacingOccurrences(of: "Claude ", with: "")
                            .replacingOccurrences(of: " 120B", with: "")
                    }
                let secondary = rest.isEmpty ? nil : rest.joined(separator: " · ")
                return (first.uppercased(), secondary)
            }
        }
        return (group.name.uppercased(), nil)
    }

    private func groupSection(_ group: ModelGroup, showHeader: Bool) -> some View {
        let parts = groupTitleParts(group)

        return VStack(alignment: .leading, spacing: showHeader ? 6 : 2) {
            if showHeader {
                HStack(spacing: 6) {
                    Text(parts.primary)
                        .font(.system(size: 9.5, weight: .semibold))
                        .tracking(0.8)
                        .foregroundStyle(palette.text.opacity(0.75))
                    if let secondary = parts.secondary {
                        Text(secondary)
                            .font(.system(size: 9))
                            .foregroundStyle(palette.muted.opacity(0.75))
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                    Spacer(minLength: 0)
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                ForEach(group.sortedWindows) { window in
                    QuotaRow(window: window, palette: palette)
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .help(group.groupDescription)
    }

    private func emptyState(icon: String, title: String, detail: String, actionTitle: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 13))
                    .foregroundStyle(palette.muted)
                Text(title)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(palette.text)
            }
            Text(detail)
                .font(.system(size: 10))
                .foregroundStyle(palette.muted)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: {
                if model.state == .notSignedIn {
                    TerminalLauncher.open(providerID: model.provider?.id ?? "antigravity")
                } else {
                    model.refresh(force: true)
                }
            }) {
                Text(actionTitle)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(Color.white)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(palette.accent)
                    .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
            }
            .buttonStyle(.plain)
        }
        .padding(.vertical, 4)
    }

    private var statusLine: String {
        if model.isRefreshing { return "Refreshing…" }
        guard model.lastUpdated != nil else { return "Reading quota…" }
        let text = model.updatedText
        return text.prefix(1).uppercased() + text.dropFirst()
    }
}

// MARK: - Rows

struct QuotaRow: View {
    let window: QuotaWindow
    let palette: Palette
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 10) {
            Text(window.label)
                .font(.system(size: 11))
                .foregroundStyle(palette.muted)
                .frame(width: 50, alignment: .leading)
            QuotaBar(
                remaining: window.remainingPct,
                pace: window.paceRemainingPct,
                palette: palette
            )
            .frame(height: 6)
            Text(String(format: "%.0f%%", window.remainingPct))
                .font(Typography.mono(11, .semibold))
                .monospacedDigit()
                .foregroundStyle(palette.barColor(remaining: window.remainingPct))
                .frame(width: 38, alignment: .trailing)
            Text(window.resetText)
                .font(.system(size: 10))
                .monospacedDigit()
                .foregroundStyle(palette.muted)
                .frame(width: 62, alignment: .trailing)
        }
        .frame(height: 20)
        .background(
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .fill(palette.text.opacity(hovering ? 0.05 : 0))
                .padding(.horizontal, -6)
                .padding(.vertical, -2)
        )
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .help(rowTooltip)
    }

    private var rowTooltip: String {
        guard let pace = window.paceRemainingPct else {
            return "\(window.rawLabel) — \(String(format: "%.0f%%", window.remainingPct)) left"
        }
        let status = window.remainingPct >= pace ? "under pace" : "burning faster than pace"
        return "\(window.rawLabel) — \(String(format: "%.0f%%", window.remainingPct)) left · \(String(format: "%.0f%%", pace)) of window left (\(status))"
    }
}

struct QuotaBar: View {
    let remaining: Double
    let pace: Double?
    let palette: Palette

    private let barHeight: CGFloat = 6

    var body: some View {
        GeometryReader { geo in
            let width = max(1, geo.size.width)
            let fillWidth = max(2, width * clamp(remaining))
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(palette.track)
                    .frame(height: barHeight)
                Capsule()
                    .fill(palette.barColor(remaining: remaining))
                    .frame(width: fillWidth, height: barHeight)
                // Inverse pace marker: dark tick over colored fill, light/foreground tick over empty track.
                if let pace {
                    let x = clamp(pace) * width
                    let onFill = pace <= remaining
                    RoundedRectangle(cornerRadius: 1, style: .continuous)
                        .fill(onFill ? Color.black.opacity(0.72) : palette.text.opacity(0.65))
                        .frame(width: 2, height: barHeight)
                        .offset(x: min(max(0, x - 1), width - 2))
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        }
        .frame(height: barHeight)
    }

    private func clamp(_ pct: Double) -> Double {
        max(0, min(1, pct / 100))
    }
}

// MARK: - Chrome helpers

struct HoverButtonStyle: ButtonStyle {
    let palette: Palette
    var radius: CGFloat = 6
    var horizontal: CGFloat = 4
    var vertical: CGFloat = 4
    @State private var hovering = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.horizontal, horizontal)
            .padding(.vertical, vertical)
            .background(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .fill(palette.text.opacity(hovering ? 0.09 : 0))
            )
            .opacity(configuration.isPressed ? 0.7 : 1)
            .onHover { hovering = $0 }
    }
}

/// Reports the provider list's natural height so the HUD can size to content
/// and only scroll once it reaches its cap.
private struct ContentHeightKey: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = max(value, nextValue())
    }
}
