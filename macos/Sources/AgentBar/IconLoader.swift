import AppKit
import SwiftUI

/// Optional custom artwork. Drop `macos/Resources/MenuBarIcon.png` (or `.svg`)
/// into the bundle, which build-app.sh copies, and it replaces the SF Symbol.
enum IconLoader {
    private static let resourceName = "MenuBarIcon"

    static func menuBarImage() -> NSImage {
        if let custom = customImage() {
            custom.isTemplate = true
            custom.size = fitted(custom.size, height: 18, maxWidth: 24)
            return custom
        }
        let symbol = NSImage(systemSymbolName: "sparkles", accessibilityDescription: "AgentBar")
            ?? NSImage(size: NSSize(width: 18, height: 18))
        symbol.isTemplate = true
        return symbol
    }

    static func headerImage() -> NSImage? {
        guard let img = customImage() else { return nil }
        img.isTemplate = true
        return img
    }

    static func namedTemplateImage(_ name: String) -> NSImage? {
        for ext in ["png", "svg"] {
            if let url = Bundle.main.url(forResource: name, withExtension: ext),
               let img = NSImage(contentsOf: url) {
                img.isTemplate = true
                return img
            }
        }
        return nil
    }

    static func providerImage(id: String) -> NSImage? {
        let slug = id.lowercased()
        if let url = Bundle.main.url(forResource: slug, withExtension: "png", subdirectory: "providers") {
            return NSImage(contentsOf: url)
        }
        return nil
    }

    private static func customImage() -> NSImage? {
        for fileExtension in ["png", "svg"] {
            if let url = Bundle.main.url(forResource: resourceName, withExtension: fileExtension),
               let image = NSImage(contentsOf: url) {
                return image
            }
        }
        return nil
    }

    private static func fitted(_ size: NSSize, height: CGFloat, maxWidth: CGFloat) -> NSSize {
        guard size.height > 0, size.width > 0 else { return NSSize(width: height, height: height) }
        let scale = min(height / size.height, maxWidth / size.width)
        return NSSize(width: (size.width * scale).rounded(), height: (size.height * scale).rounded())
    }
}
