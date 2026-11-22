import AppKit
import Carbon.HIToolbox
import Combine
import SwiftUI

extension Notification.Name {
    static let agentBarHideHUD = Notification.Name("agentbar.hud.hide")
    static let agentBarContentResize = Notification.Name("agentbar.hud.resize")
}

/// Borderless panel that can take key focus without activating a normal window.
final class HUDWindow: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let model = AgentBarModel()

    private static let panelWidth: CGFloat = 420
    private static let cornerRadius: CGFloat = 14
    private static let animationDuration: TimeInterval = 0.14

    private var panel: HUDWindow?
    private var vibrancy: NSVisualEffectView?
    private var hosting: NSHostingView<AnyView>?
    private var statusItem: NSStatusItem?
    private var hotKeyRef: EventHotKeyRef?
    private var hotKeyHandler: EventHandlerRef?
    private var keyMonitor: Any?
    private var outsideClickMonitor: Any?
    private var observers: [NSObjectProtocol] = []
    private var cancellables = Set<AnyCancellable>()
    private var menuTracking = false
    private var lastOutsideClick: Date?
    /// Read from the nonisolated key monitor, written on the main actor only.
    nonisolated(unsafe) private var hudAcceptsKeys = false

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        buildPanel()
        buildStatusItem()
        applyTheme()
        registerHotKey()
        installKeyMonitor()
        installOutsideClickMonitor()
        observeModel()
        observeDefaults()
        observeMenus()
        Task { @MainActor in self.showHUD() }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showHUD()
        return true
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let hotKeyRef { UnregisterEventHotKey(hotKeyRef) }
        if let hotKeyHandler { RemoveEventHandler(hotKeyHandler) }
        if let keyMonitor { NSEvent.removeMonitor(keyMonitor) }
        if let outsideClickMonitor { NSEvent.removeMonitor(outsideClickMonitor) }
        for observer in observers { NotificationCenter.default.removeObserver(observer) }
    }

    // MARK: - Panel

    private func buildPanel() {
        let size = NSSize(width: Self.panelWidth, height: 340)
        let panel = HUDWindow(
            contentRect: NSRect(origin: .zero, size: size),
            styleMask: [.nonactivatingPanel, .borderless, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .transient]
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.isMovableByWindowBackground = true
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.animationBehavior = .none
        panel.titleVisibility = .hidden
        panel.delegate = self

        let vibrancy = NSVisualEffectView(frame: NSRect(origin: .zero, size: size))
        vibrancy.material = .hudWindow
        vibrancy.blendingMode = .behindWindow
        vibrancy.state = .active
        vibrancy.wantsLayer = true
        vibrancy.layer?.cornerRadius = Self.cornerRadius
        vibrancy.layer?.cornerCurve = .continuous
        vibrancy.layer?.masksToBounds = true
        vibrancy.autoresizingMask = [.width, .height]

        let hosting = NSHostingView(rootView: AnyView(ContentView().environmentObject(model)))
        hosting.frame = vibrancy.bounds
        hosting.autoresizingMask = [.width, .height]
        hosting.layer?.backgroundColor = NSColor.clear.cgColor
        vibrancy.addSubview(hosting)

        panel.contentView = vibrancy

        self.panel = panel
        self.vibrancy = vibrancy
        self.hosting = hosting
    }

    private func resizeToFit() {
        guard let panel, let hosting else { return }
        hosting.frame.size.width = Self.panelWidth
        let height = max(140, hosting.fittingSize.height)
        hosting.frame = NSRect(x: 0, y: 0, width: Self.panelWidth, height: height)
        panel.setContentSize(NSSize(width: Self.panelWidth, height: height))
    }

    private func resizeIfVisible() {
        guard let panel, panel.isVisible else { return }
        resizeToFit()
        panel.setFrameOrigin(targetOrigin(for: panel.frame.size))
    }

    /// Upper-center of the screen holding the pointer, ~18% below the top edge.
    private func targetOrigin(for size: NSSize) -> NSPoint {
        let mouse = NSEvent.mouseLocation
        let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) }
            ?? NSScreen.main
            ?? NSScreen.screens.first
        guard let visible = screen?.visibleFrame else { return NSPoint(x: 0, y: 0) }
        let x = (visible.midX - size.width / 2).rounded()
        let top = visible.maxY - visible.height * 0.18
        // Keep tall content inside the screen instead of running off the bottom.
        let y = max(visible.minY + 8, (top - size.height).rounded())
        return NSPoint(x: x, y: y)
    }

    // MARK: - Show / hide

    func toggleHUD() {
        if panel?.isVisible == true {
            hideHUD()
        } else {
            showHUD()
        }
    }

    func showHUD() {
        guard let panel else { return }
        applyTheme()
        updateStatusItem()
        resizeToFit()
        panel.setFrameOrigin(targetOrigin(for: panel.frame.size))
        panel.alphaValue = 0
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        hudAcceptsKeys = true
        animate(panel, showing: true)
    }

    func hideHUD() {
        guard let panel, panel.isVisible else { return }
        hudAcceptsKeys = false
        animate(panel, showing: false)
    }

    private func animate(_ panel: NSPanel, showing: Bool) {
        let duration = Self.animationDuration
        if let layer = panel.contentView?.layer {
            let scale = CABasicAnimation(keyPath: "transform.scale")
            scale.fromValue = showing ? 0.97 : 1.0
            scale.toValue = showing ? 1.0 : 0.97
            scale.duration = duration
            scale.timingFunction = CAMediaTimingFunction(name: .easeOut)
            layer.add(scale, forKey: showing ? "hud.show" : "hud.hide")
        }
        NSAnimationContext.runAnimationGroup { context in
            context.duration = duration
            context.timingFunction = CAMediaTimingFunction(name: .easeOut)
            panel.animator().alphaValue = showing ? 1 : 0
        } completionHandler: {
            guard !showing else { return }
            Task { @MainActor in
                panel.orderOut(nil)
                panel.alphaValue = 1
            }
        }
    }

    /// Dismiss when the click that stole focus landed outside the panel. A bare
    /// resign (macOS handing activation back to the app behind us) must not close it.
    func windowDidResignKey(_ notification: Notification) {
        guard !menuTracking else { return }
        let clickedOutside = lastOutsideClick.map { Date().timeIntervalSince($0) < 0.8 } ?? false
        guard clickedOutside else { return }
        hideHUD()
    }

    // MARK: - Theme

    private func applyTheme() {
        let kind = currentTheme()
        let appearance = kind.appearanceName.flatMap { NSAppearance(named: $0) }
        panel?.appearance = appearance
        vibrancy?.appearance = appearance
        hosting?.appearance = appearance
        vibrancy?.needsDisplay = true
    }

    private func currentTheme() -> ThemeKind {
        let raw = UserDefaults.standard.string(forKey: ThemeKind.storageKey)
        return raw.flatMap(ThemeKind.init(rawValue:)) ?? .system
    }

    private func currentMenuBarStyle() -> MenuBarStyle {
        let raw = UserDefaults.standard.string(forKey: MenuBarStyle.storageKey)
        return raw.flatMap(MenuBarStyle.init(rawValue:)) ?? .iconOnly
    }

    // MARK: - Global hotkey (⌥⇧A), Carbon so no Accessibility grant is needed

    private func registerHotKey() {
        var eventType = EventTypeSpec(
            eventClass: OSType(kEventClassKeyboard),
            eventKind: UInt32(kEventHotKeyPressed)
        )
        let context = Unmanaged.passUnretained(self).toOpaque()
        InstallEventHandler(
            GetApplicationEventTarget(),
            { _, _, userData in
                guard let userData else { return OSStatus(eventNotHandledErr) }
                let delegate = Unmanaged<AppDelegate>.fromOpaque(userData).takeUnretainedValue()
                Task { @MainActor in delegate.toggleHUD() }
                return noErr
            },
            1,
            &eventType,
            context,
            &hotKeyHandler
        )

        let hotKeyID = EventHotKeyID(signature: OSType(0x4142_4152), id: 1) // 'ABAR'
        RegisterEventHotKey(
            UInt32(kVK_ANSI_A),
            UInt32(optionKey | shiftKey),
            hotKeyID,
            GetApplicationEventTarget(),
            0,
            &hotKeyRef
        )
    }

    // MARK: - Keyboard shortcuts inside the HUD

    private func installKeyMonitor() {
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, self.hudAcceptsKeys else { return event }
            let keyCode = event.keyCode
            let command = event.modifierFlags
                .intersection(.deviceIndependentFlagsMask)
                .contains(.command)
            guard Self.handles(keyCode, command: command) else { return event }
            Task { @MainActor in self.handle(keyCode, command: command) }
            return nil
        }
    }

    private nonisolated static func handles(_ keyCode: UInt16, command: Bool) -> Bool {
        switch keyCode {
        case UInt16(kVK_Escape): return true
        case UInt16(kVK_ANSI_R), UInt16(kVK_ANSI_O), UInt16(kVK_ANSI_Q): return command
        default: return false
        }
    }

    private func handle(_ keyCode: UInt16, command: Bool) {
        switch keyCode {
        case UInt16(kVK_Escape):
            hideHUD()
        case UInt16(kVK_ANSI_R):
            model.refresh(force: true)
        case UInt16(kVK_ANSI_O):
            TerminalLauncher.openAGY()
        case UInt16(kVK_ANSI_Q):
            NSApp.terminate(nil)
        default:
            break
        }
    }

    /// Global monitors only see events that go to other apps, i.e. clicks outside the HUD.
    private func installOutsideClickMonitor() {
        outsideClickMonitor = NSEvent.addGlobalMonitorForEvents(
            matching: [.leftMouseDown, .rightMouseDown, .otherMouseDown]
        ) { [weak self] _ in
            Task { @MainActor in self?.lastOutsideClick = Date() }
        }
    }

    // MARK: - Menubar item

    private func buildStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.target = self
        item.button?.action = #selector(statusItemClicked)
        item.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
        item.button?.font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .medium)
        item.isVisible = currentMenuBarStyle() != .hidden
        statusItem = item
        updateStatusItem()
    }

    private func updateStatusItem() {
        guard let item = statusItem else { return }
        let style = currentMenuBarStyle()
        item.isVisible = style != .hidden
        item.button?.image = IconLoader.menuBarImage()
        item.button?.imagePosition = .imageLeading
        item.button?.title = style == .iconAndPercent ? " " + model.menuBarText : ""
    }

    @objc private func statusItemClicked() {
        let event = NSApp.currentEvent
        let isSecondary = event?.type == .rightMouseUp
            || event?.modifierFlags.contains(.control) == true
        if isSecondary {
            showContextMenu()
        } else {
            toggleHUD()
        }
    }

    private func showContextMenu() {
        guard let item = statusItem else { return }
        item.menu = makeMenu()
        item.button?.performClick(nil)
        item.menu = nil
    }

    private func makeMenu() -> NSMenu {
        let menu = NSMenu()

        let toggle = NSMenuItem(title: "Show HUD", action: #selector(toggleFromMenu), keyEquivalent: "")
        toggle.target = self
        menu.addItem(toggle)

        let refresh = NSMenuItem(title: "Refresh", action: #selector(refreshFromMenu), keyEquivalent: "r")
        refresh.target = self
        menu.addItem(refresh)

        menu.addItem(.separator())

        let themeItem = NSMenuItem(title: "Theme", action: nil, keyEquivalent: "")
        let themeMenu = NSMenu()
        let activeTheme = currentTheme()
        for kind in ThemeKind.allCases {
            let item = NSMenuItem(title: kind.label, action: #selector(selectTheme(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = kind.rawValue
            item.state = kind == activeTheme ? .on : .off
            themeMenu.addItem(item)
        }
        themeItem.submenu = themeMenu
        menu.addItem(themeItem)

        let styleItem = NSMenuItem(title: "Menubar style", action: nil, keyEquivalent: "")
        let styleMenu = NSMenu()
        let activeStyle = currentMenuBarStyle()
        for style in MenuBarStyle.allCases {
            let item = NSMenuItem(title: style.label, action: #selector(selectMenuBarStyle(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = style.rawValue
            item.state = style == activeStyle ? .on : .off
            styleMenu.addItem(item)
        }
        styleItem.submenu = styleMenu
        menu.addItem(styleItem)

        menu.addItem(.separator())

        let quit = NSMenuItem(title: "Quit AgentBar", action: #selector(quitFromMenu), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        return menu
    }

    @objc private func toggleFromMenu() {
        toggleHUD()
    }

    @objc private func refreshFromMenu() {
        model.refresh(force: true)
    }

    @objc private func selectTheme(_ sender: NSMenuItem) {
        guard let raw = sender.representedObject as? String else { return }
        UserDefaults.standard.set(raw, forKey: ThemeKind.storageKey)
        applyTheme()
    }

    @objc private func selectMenuBarStyle(_ sender: NSMenuItem) {
        guard let raw = sender.representedObject as? String else { return }
        UserDefaults.standard.set(raw, forKey: MenuBarStyle.storageKey)
        updateStatusItem()
    }

    @objc private func quitFromMenu() {
        NSApp.terminate(nil)
    }

    // MARK: - Observation

    private func observeModel() {
        model.objectWillChange
            .sink { [weak self] in
                Task { @MainActor in
                    self?.updateStatusItem()
                    self?.resizeIfVisible()
                }
            }
            .store(in: &cancellables)

        observers.append(NotificationCenter.default.addObserver(
            forName: .agentBarHideHUD,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in self?.hideHUD() }
        })

        observers.append(NotificationCenter.default.addObserver(
            forName: .agentBarContentResize,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in self?.resizeIfVisible() }
        })
    }

    private func observeDefaults() {
        observers.append(NotificationCenter.default.addObserver(
            forName: UserDefaults.didChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.applyTheme()
                self?.updateStatusItem()
            }
        })
    }

    private func observeMenus() {
        let center = NotificationCenter.default
        observers.append(center.addObserver(forName: NSMenu.didBeginTrackingNotification, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.menuTracking = true }
        })
        observers.append(center.addObserver(forName: NSMenu.didEndTrackingNotification, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.menuTracking = false }
        })
    }
}
