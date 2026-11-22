import AppKit

// Plain AppKit entry point: the HUD panel and the status item are the only UI,
// so no SwiftUI Scene (and no stray Settings window) is needed.
MainActor.assumeIsolated {
    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.accessory)
    app.run()
}
