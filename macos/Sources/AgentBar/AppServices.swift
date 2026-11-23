import AppKit
import Foundation
import ServiceManagement
import Sparkle
import SwiftUI

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

/// Sparkle 2 auto-updater backed by https://agentbar.fyi/appcast.xml.
@MainActor
final class SparkleUpdater {
    static let shared = SparkleUpdater()

    private lazy var controller = SPUStandardUpdaterController(
        startingUpdater: true,
        updaterDelegate: nil,
        userDriverDelegate: nil
    )

    var automaticallyChecksForUpdates: Bool {
        get { controller.updater.automaticallyChecksForUpdates }
        set { controller.updater.automaticallyChecksForUpdates = newValue }
    }

    static var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    static var buildNumber: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "1"
    }

    func start() {
        _ = controller
    }

    func checkForUpdates() {
        NSApp.activate(ignoringOtherApps: true)
        controller.checkForUpdates(nil)
    }
}

/// Glassy floating About panel matching the AgentBar HUD aesthetic.
@MainActor
final class AboutWindowController {
    static let shared = AboutWindowController()

    private var panel: NSPanel?

    func show() {
        if let panel {
            applyAppearance(to: panel)
            panel.center()
            panel.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        let size = NSSize(width: 312, height: 332)
        let panel = HUDWindow(
            contentRect: NSRect(origin: .zero, size: size),
            styleMask: [.nonactivatingPanel, .borderless],
            backing: .buffered,
            defer: false
        )
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.isMovableByWindowBackground = true
        panel.isReleasedWhenClosed = false

        let container = NSView(frame: NSRect(origin: .zero, size: size))
        container.wantsLayer = true
        container.layer?.backgroundColor = NSColor.clear.cgColor
        container.autoresizingMask = [.width, .height]

        let radius: CGFloat = 16
        let vibrancy = NSVisualEffectView(frame: container.bounds)
        vibrancy.material = .popover
        vibrancy.blendingMode = .behindWindow
        vibrancy.state = .active
        vibrancy.maskImage = Self.cornerMaskImage(radius: radius)
        vibrancy.wantsLayer = true
        vibrancy.layer?.cornerRadius = radius
        vibrancy.layer?.cornerCurve = .continuous
        vibrancy.layer?.masksToBounds = true
        vibrancy.autoresizingMask = [.width, .height]

        let hosting = NSHostingView(rootView: AboutView { [weak self] in
            self?.panel?.orderOut(nil)
        })
        hosting.frame = vibrancy.bounds
        hosting.autoresizingMask = [.width, .height]
        hosting.wantsLayer = true
        hosting.layer?.backgroundColor = NSColor.clear.cgColor
        vibrancy.addSubview(hosting)

        container.addSubview(vibrancy)
        panel.contentView = container
        applyAppearance(to: panel)
        panel.center()
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        self.panel = panel
    }

    private func applyAppearance(to panel: NSPanel) {
        let raw = UserDefaults.standard.string(forKey: ThemeKind.storageKey)
        let kind = raw.flatMap(ThemeKind.init(rawValue:)) ?? .system
        panel.appearance = kind.appearanceName.flatMap { NSAppearance(named: $0) }
    }

    private static func cornerMaskImage(radius: CGFloat) -> NSImage {
        let edge = radius * 2 + 1
        let image = NSImage(size: NSSize(width: edge, height: edge), flipped: false) { rect in
            NSColor.black.setFill()
            NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius).fill()
            return true
        }
        image.capInsets = NSEdgeInsets(top: radius, left: radius, bottom: radius, right: radius)
        image.resizingMode = .stretch
        return image
    }
}

private struct AboutView: View {
    @Environment(\.colorScheme) private var scheme
    @AppStorage(ThemeKind.storageKey) private var themeRaw = ThemeKind.system.rawValue
    let onClose: () -> Void

    private var theme: ThemeKind { ThemeKind(rawValue: themeRaw) ?? .system }
    private var palette: Palette { .resolve(theme, scheme) }

    var body: some View {
        ZStack(alignment: .topTrailing) {
            VStack(spacing: 12) {
                appIcon
                    .padding(.top, 18)

                VStack(spacing: 3) {
                    Text("AgentBar")
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(palette.text)

                    Text("v\(SparkleUpdater.currentVersion) (\(SparkleUpdater.buildNumber))")
                        .font(Typography.mono(11, .regular))
                        .foregroundStyle(palette.muted)
                }

                Text("Live AI coding quota HUD for macOS")
                    .font(.system(size: 11.5))
                    .foregroundStyle(palette.muted)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 18)

                VStack(spacing: 9) {
                    HStack(spacing: 9) {
                        linkPill(
                            title: "agentbar.fyi",
                            image: IconLoader.headerImage(),
                            fallbackSymbol: "sparkles",
                            url: "https://agentbar.fyi"
                        )
                        linkPill(
                            title: "GitHub",
                            image: IconLoader.namedTemplateImage("github"),
                            fallbackSymbol: "chevron.left.forwardslash.chevron.right",
                            url: "https://github.com/bitbonsai/agentbar"
                        )
                    }

                    Button {
                        SparkleUpdater.shared.checkForUpdates()
                    } label: {
                        HStack(spacing: 7) {
                            Image(systemName: "arrow.triangle.2.circlepath")
                                .font(.system(size: 11.5, weight: .semibold))
                            Text("Check for Updates")
                                .font(.system(size: 12, weight: .medium))
                        }
                        .foregroundStyle(palette.text)
                        .frame(maxWidth: .infinity)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .background(
                            RoundedRectangle(cornerRadius: 9, style: .continuous)
                                .fill(scheme == .dark ? Color.black.opacity(0.28) : Color.white.opacity(0.65))
                        )
                        .overlay(
                            RoundedRectangle(cornerRadius: 9, style: .continuous)
                                .strokeBorder(
                                    scheme == .dark ? Color.white.opacity(0.06) : Color.black.opacity(0.06),
                                    lineWidth: 0.75
                                )
                        )
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 18)
                .padding(.top, 4)

                Spacer(minLength: 4)

                Text("Made by @bitbonsai · MIT License")
                    .font(.system(size: 10))
                    .foregroundStyle(palette.muted.opacity(0.8))
                    .padding(.bottom, 14)
            }
            .frame(width: 312, height: 332)

            Button(action: onClose) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(palette.muted)
                    .frame(width: 22, height: 22)
                    .background(
                        Circle().fill(
                            scheme == .dark ? Color.white.opacity(0.08) : Color.black.opacity(0.06)
                        )
                    )
            }
            .buttonStyle(.plain)
            .padding(12)
            .help("Close")
        }
        .background(
            theme.isSystem
                ? (scheme == .dark ? Color.black.opacity(0.14) : Color.white.opacity(0.12))
                : palette.background.opacity(scheme == .dark ? 0.55 : 0.62)
        )
        .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(palette.text.opacity(scheme == .dark ? 0.1 : 0.08), lineWidth: 0.75)
                .allowsHitTesting(false)
        )
        .onExitCommand(perform: onClose)
    }

    @ViewBuilder
    private var appIcon: some View {
        if let image = IconLoader.headerImage() {
            Image(nsImage: image)
                .renderingMode(.template)
                .resizable()
                .interpolation(.high)
                .aspectRatio(contentMode: .fit)
                .foregroundStyle(palette.text)
                .frame(width: 56, height: 56)
        } else {
            Image(systemName: "sparkles")
                .font(.system(size: 38, weight: .medium))
                .foregroundStyle(palette.accent)
                .frame(width: 56, height: 56)
        }
    }

    private func linkPill(title: String, image: NSImage?, fallbackSymbol: String, url: String) -> some View {
        Button {
            if let target = URL(string: url) {
                NSWorkspace.shared.open(target)
            }
        } label: {
            HStack(spacing: 7) {
                if let image {
                    Image(nsImage: image)
                        .renderingMode(.template)
                        .resizable()
                        .interpolation(.high)
                        .aspectRatio(contentMode: .fit)
                        .foregroundStyle(palette.text.opacity(0.88))
                        .frame(width: 14, height: 14)
                } else {
                    Image(systemName: fallbackSymbol)
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(palette.text.opacity(0.88))
                }
                Text(title)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(palette.text)
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(
                RoundedRectangle(cornerRadius: 9, style: .continuous)
                    .fill(scheme == .dark ? Color.black.opacity(0.28) : Color.white.opacity(0.65))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 9, style: .continuous)
                    .strokeBorder(
                        scheme == .dark ? Color.white.opacity(0.06) : Color.black.opacity(0.06),
                        lineWidth: 0.75
                    )
            )
        }
        .buttonStyle(.plain)
    }
}
