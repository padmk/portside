import AppKit
import SwiftUI

/// Actions the menu panel asks the app to perform.
@MainActor
struct AppActions {
    let openDashboard: (String?) -> Void
    /// Opens a server in the dashboard on its terminal.
    let openLogs: (String) -> Void
    let openURL: (String) -> Void
    let copy: (String) -> Void
    let installCLI: () -> Void
    let about: () -> Void
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSPopoverDelegate {
    private let store = Store()
    private let notifier = Notifier()
    private var statusItem: NSStatusItem!
    private let popover = NSPopover()
    private var dashboard: DashboardWindowController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // `--appearance=light|dark` pins the look (used for screenshots).
        if let arg = CommandLine.arguments.first(where: { $0.hasPrefix("--appearance=") }) {
            NSApp.appearance = NSAppearance(named: arg.hasSuffix("dark") ? .darkAqua : .aqua)
        }
        AppIconStyle.apply()
        notifier.setUp()
        notifier.onRestart = { [weak self] name in self?.store.perform("restart", name) }
        notifier.onShow = { [weak self] name in self?.openDashboard(name, terminal: true) }

        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.image = StatusIcon.image(for: Store.Summary(offline: true))
        statusItem.button?.action = #selector(togglePopover(_:))
        statusItem.button?.target = self
        statusItem.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])

        let actions = AppActions(
            openDashboard: { [weak self] name in self?.openDashboard(name) },
            openLogs: { [weak self] name in self?.openDashboard(name, terminal: true) },
            openURL: { [weak self] url in
                self?.popover.performClose(nil)
                if let u = URL(string: url) { NSWorkspace.shared.open(u) }
            },
            copy: { text in
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(text, forType: .string)
            },
            installCLI: { [weak self] in self?.installCLI() },
            about: { [weak self] in
                self?.popover.performClose(nil)
                NSApp.activate()
                NSApp.orderFrontStandardAboutPanel(nil)
            })
        popover.behavior = .transient
        popover.animates = false
        popover.delegate = self
        popover.contentViewController = NSHostingController(rootView: MenuPanel(store: store, actions: actions))

        store.onSummaryChange = { [weak self] s in
            self?.statusItem.button?.image = StatusIcon.image(for: s)
            self?.statusItem.button?.setAccessibilityLabel(StatusIcon.accessibility(s))
        }
        store.onCrash = { [weak self] v in self?.notifier.crashed(v) }
        store.start()

        // `--render-panel=PATH` draws the panel offscreen to a PNG once data
        // arrives, then quits (used for screenshots; needs no visible screen).
        if let arg = CommandLine.arguments.first(where: { $0.hasPrefix("--render-panel=") }) {
            let path = String(arg.dropFirst("--render-panel=".count))
            Task { @MainActor in
                for _ in 0..<60 where self.store.connection != .connected || self.store.servers.isEmpty {
                    try? await Task.sleep(nanoseconds: 250_000_000)
                }
                try? await Task.sleep(nanoseconds: 800_000_000)
                PanelRenderer.render(MenuPanel(store: self.store, actions: actions), to: path)
                NSApp.terminate(nil)
            }
        }

        // `--show-menu` opens the panel right away (used for screenshots).
        if CommandLine.arguments.contains("--show-menu") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
                guard let self, let button = self.statusItem.button else { return }
                self.popover.behavior = .applicationDefined
                self.togglePopover(button)
            }
        }

        // First launch from Finder: show the dashboard so the app is discoverable.
        if !UserDefaults.standard.bool(forKey: "launchedBefore") {
            UserDefaults.standard.set(true, forKey: "launchedBefore")
            openDashboard(nil)
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openDashboard(nil)
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    @objc private func togglePopover(_ sender: NSStatusBarButton) {
        if popover.isShown {
            popover.performClose(sender)
        } else {
            popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .minY)
            popover.contentViewController?.view.window?.makeKey()
            NSApp.activate()
        }
    }

    private func openDashboard(_ server: String?, terminal: Bool = false) {
        let target = server.map { terminal ? $0 + "/terminal" : $0 }
        popover.performClose(nil)
        if dashboard == nil {
            let wc = DashboardWindowController()
            wc.onClose = {
                // No window left: back to a menu bar app without a Dock icon.
                NSApp.setActivationPolicy(.accessory)
            }
            wc.onRetry = { [weak self, weak wc] in
                guard let self, let wc, wc.window?.isVisible == true else { return }
                wc.show(api: self.store.api, server: nil)
            }
            dashboard = wc
        }
        NSApp.setActivationPolicy(.regular)
        AppIconStyle.apply()
        dashboard?.show(api: store.api, server: target)
        if store.api == nil {
            // Load the real dashboard as soon as the daemon answers.
            Task { @MainActor in
                for _ in 0..<40 {
                    try? await Task.sleep(nanoseconds: 500_000_000)
                    if let api = store.api {
                        dashboard?.show(api: api, server: target)
                        return
                    }
                }
            }
        }
    }

    private func installCLI() {
        popover.performClose(nil)
        NSApp.activate()
        let alert = NSAlert()
        do {
            let link = try CommandLineTool.install()
            alert.messageText = L.t("Command line tool installed", "Narzędzie wiersza poleceń zainstalowane")
            var text = L.t("`portside` now points to this app: \(link.path)", "`portside` wskazuje teraz na tę aplikację: \(link.path)")
            if !CommandLineTool.onPath {
                text += "\n\n" + L.t("Add ~/.local/bin to your PATH to use it.", "Dodaj ~/.local/bin do PATH, żeby z niego korzystać.")
            }
            alert.informativeText = text
        } catch {
            alert.alertStyle = .warning
            alert.messageText = L.t("Could not install the command line tool", "Nie udało się zainstalować narzędzia")
            alert.informativeText = error.localizedDescription
        }
        alert.runModal()
    }
}

/// AppKit entry point. A SwiftUI App with only a Settings scene would open an
/// empty settings window, so the app sets up its menu and delegate itself.
@main
enum PortsideMain {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.mainMenu = MainMenu.build()
        withExtendedLifetime(delegate) { app.run() }
    }
}

/// The menu bar shown while the dashboard window is open. Edit commands make
/// copy and paste work in the web view.
enum MainMenu {
    static func build() -> NSMenu {
        let main = NSMenu()
        let name = Brand.name

        let appMenu = NSMenu()
        appMenu.addItem(withTitle: L.t("About \(name)", "O aplikacji \(name)"), action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: L.t("Hide \(name)", "Ukryj \(name)"), action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: L.t("Quit \(name)", "Zakończ \(name)"), action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        add(appMenu, title: name, to: main)

        let edit = NSMenu(title: L.t("Edit", "Edycja"))
        edit.addItem(withTitle: L.t("Undo", "Cofnij"), action: Selector(("undo:")), keyEquivalent: "z")
        let redo = edit.addItem(withTitle: L.t("Redo", "Powtórz"), action: Selector(("redo:")), keyEquivalent: "z")
        redo.keyEquivalentModifierMask = [.command, .shift]
        edit.addItem(.separator())
        edit.addItem(withTitle: L.t("Cut", "Wytnij"), action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: L.t("Copy", "Kopiuj"), action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: L.t("Paste", "Wklej"), action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: L.t("Select All", "Zaznacz wszystko"), action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        add(edit, title: edit.title, to: main)

        let window = NSMenu(title: L.t("Window", "Okno"))
        window.addItem(withTitle: L.t("Close", "Zamknij"), action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        window.addItem(withTitle: L.t("Minimize", "Minimalizuj"), action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        window.addItem(withTitle: L.t("Zoom", "Powiększ"), action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        add(window, title: window.title, to: main)
        NSApp.windowsMenu = window
        return main
    }

    private static func add(_ menu: NSMenu, title: String, to main: NSMenu) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.submenu = menu
        main.addItem(item)
    }
}

/// Renders a SwiftUI view in an offscreen window at 2x into a PNG.
@MainActor
enum PanelRenderer {
    static func render<V: View>(_ view: V, to path: String) {
        let host = NSHostingView(rootView: view)
        let size = host.fittingSize
        let window = NSWindow(contentRect: NSRect(x: -20000, y: -20000, width: size.width, height: size.height),
                              styleMask: [.borderless], backing: .buffered, defer: false)
        window.appearance = NSApp.appearance
        window.contentView = host
        window.orderFrontRegardless()
        host.frame = NSRect(origin: .zero, size: size)
        host.layoutSubtreeIfNeeded()
        host.display()
        let rect = host.bounds
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(rect.width * 2), pixelsHigh: Int(rect.height * 2),
                                         bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                         colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return }
        rep.size = rect.size
        host.cacheDisplay(in: rect, to: rep)
        window.orderOut(nil)
        if let data = rep.representation(using: .png, properties: [:]) {
            try? data.write(to: URL(fileURLWithPath: path))
        }
    }
}
