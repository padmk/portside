import AppKit
import SwiftUI

/// Actions the menu panel asks the app to perform.
@MainActor
struct AppActions {
    let openDashboard: (String?) -> Void
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
        AppIconStyle.apply()
        notifier.setUp()
        notifier.onRestart = { [weak self] name in self?.store.perform("restart", name) }
        notifier.onShow = { [weak self] name in self?.openDashboard(name) }

        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.image = StatusIcon.image(for: Store.Summary(offline: true))
        statusItem.button?.action = #selector(togglePopover(_:))
        statusItem.button?.target = self
        statusItem.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])

        let actions = AppActions(
            openDashboard: { [weak self] name in self?.openDashboard(name) },
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

    private func openDashboard(_ server: String?) {
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
        dashboard?.show(api: store.api, server: server)
        if store.api == nil {
            // Load the real dashboard as soon as the daemon answers.
            Task { @MainActor in
                for _ in 0..<40 {
                    try? await Task.sleep(nanoseconds: 500_000_000)
                    if let api = store.api {
                        dashboard?.show(api: api, server: server)
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

@main
struct PortsideApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    var body: some Scene {
        Settings { EmptyView() }
    }
}
