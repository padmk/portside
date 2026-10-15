import AppKit

/// Menu bar icon: the tugboat from the app icon, with a status light on its mast.
///
/// - something crashed: red light
/// - something starting or stopping: amber light
/// - something running: green light
/// - nothing running: hollow light
/// - daemon unreachable: faded boat, hollow light
///
/// The boat is drawn in the label color at render time, so it follows light
/// and dark menu bars; the light keeps its color in both.
enum StatusIcon {
    static let size = NSSize(width: 24, height: 18)

    static func image(for s: Store.Summary) -> NSImage {
        let light: NSColor? = {
            if s.offline { return nil }
            if s.crashed > 0 { return NSColor(srgbRed: 0.95, green: 0.25, blue: 0.22, alpha: 1) }
            if s.starting > 0 { return NSColor(srgbRed: 0.98, green: 0.65, blue: 0.15, alpha: 1) }
            if s.running > 0 { return NSColor(srgbRed: 0.20, green: 0.80, blue: 0.45, alpha: 1) }
            return nil
        }()
        let img = NSImage(size: size, flipped: false) { _ in
            let body = NSColor.labelColor.withAlphaComponent(s.offline ? 0.45 : 1)
            drawTug(body: body, light: light)
            return true
        }
        img.isTemplate = false
        img.accessibilityDescription = accessibility(s)
        return img
    }

    /// Draws the tug in a 24x18 pt box with the origin at the bottom left.
    /// Gaps (fender line, hollow light) are made by clipping, never by
    /// clearing pixels, so nothing behind the icon is erased.
    static func drawTug(body: NSColor, light: NSColor?) {
        let bounds = NSRect(origin: .zero, size: size)
        let r: CGFloat = 2.1
        let c = NSPoint(x: 15.25, y: 15.8)
        let lampRect = NSRect(x: c.x - r, y: c.y - r, width: 2 * r, height: 2 * r)

        // Everything except the lamp area, so the boat never shows through a hollow light.
        NSGraphicsContext.saveGraphicsState()
        let outsideLamp = NSBezierPath(rect: bounds)
        outsideLamp.append(NSBezierPath(ovalIn: lampRect))
        outsideLamp.windingRule = .evenOdd
        outsideLamp.addClip()
        body.setFill()

        // Hull, minus a thin fender line.
        NSGraphicsContext.saveGraphicsState()
        let fender = NSBezierPath()
        fender.move(to: NSPoint(x: 0, y: 4.85))
        fender.line(to: NSPoint(x: 24, y: 6.5))
        fender.line(to: NSPoint(x: 24, y: 7.5))
        fender.line(to: NSPoint(x: 0, y: 5.85))
        fender.close()
        let outsideFender = NSBezierPath(rect: bounds)
        outsideFender.append(fender)
        outsideFender.windingRule = .evenOdd
        outsideFender.addClip()
        let hull = NSBezierPath()
        hull.move(to: NSPoint(x: 1.2, y: 7.2))
        hull.line(to: NSPoint(x: 22.6, y: 8.6))
        hull.curve(to: NSPoint(x: 17.2, y: 1.2), controlPoint1: NSPoint(x: 22.0, y: 4.6), controlPoint2: NSPoint(x: 20.2, y: 1.2))
        hull.line(to: NSPoint(x: 4.6, y: 1.2))
        hull.curve(to: NSPoint(x: 1.2, y: 5.0), controlPoint1: NSPoint(x: 2.4, y: 1.2), controlPoint2: NSPoint(x: 1.2, y: 2.8))
        hull.close()
        hull.fill()
        NSGraphicsContext.restoreGraphicsState()

        // Two server blocks as cargo.
        NSBezierPath(roundedRect: NSRect(x: 2.6, y: 8.0, width: 6.4, height: 2.6), xRadius: 0.8, yRadius: 0.8).fill()
        NSBezierPath(roundedRect: NSRect(x: 2.6, y: 11.1, width: 6.4, height: 2.6), xRadius: 0.8, yRadius: 0.8).fill()

        // Cabin with a window, roof and mast.
        let cabin = NSBezierPath(roundedRect: NSRect(x: 10.4, y: 8.0, width: 7.4, height: 6.0), xRadius: 0.9, yRadius: 0.9)
        cabin.append(NSBezierPath(roundedRect: NSRect(x: 12.0, y: 9.9, width: 3.0, height: 2.6), xRadius: 0.5, yRadius: 0.5))
        cabin.windingRule = .evenOdd
        cabin.fill()
        NSBezierPath(roundedRect: NSRect(x: 9.7, y: 13.6, width: 8.8, height: 1.4), xRadius: 0.7, yRadius: 0.7).fill()
        NSBezierPath(rect: NSRect(x: 14.6, y: 14.6, width: 1.3, height: 1.2)).fill()
        NSGraphicsContext.restoreGraphicsState()

        // Status light on the mast.
        if let light {
            light.setFill()
            NSBezierPath(ovalIn: lampRect).fill()
        } else {
            let ring = NSBezierPath(ovalIn: lampRect.insetBy(dx: 0.55, dy: 0.55))
            ring.lineWidth = 1.1
            body.setStroke()
            ring.stroke()
        }
    }

    static func accessibility(_ s: Store.Summary) -> String {
        if s.offline { return "\(Brand.name): daemon not running" }
        var parts = ["\(s.running) running"]
        if s.crashed > 0 { parts.append("\(s.crashed) crashed") }
        if s.starting > 0 { parts.append("\(s.starting) starting") }
        return "\(Brand.name): " + parts.joined(separator: ", ")
    }
}
