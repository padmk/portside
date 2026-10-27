import AppKit
import ServiceManagement
import UserNotifications

/// Launch at login through the system's login items.
/// The setting is mirrored to the `launchAtLogin` preference, which
/// `portside service install` and `portside doctor` read.
enum LoginItem {
    static var isEnabled: Bool {
        let on = SMAppService.mainApp.status == .enabled
        UserDefaults.standard.set(on, forKey: "launchAtLogin")
        return on
    }

    /// Whether the user has turned it on or off, from the menu or the dashboard;
    /// until then the dashboard's getting-started page proposes it.
    static var decided: Bool { UserDefaults.standard.bool(forKey: "loginItemDecided") }

    static func set(_ on: Bool, onError: (String) -> Void) {
        UserDefaults.standard.set(true, forKey: "loginItemDecided")
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {
            onError(error.localizedDescription)
        }
        if on, SMAppService.mainApp.status == .requiresApproval {
            SMAppService.openSystemSettingsLoginItems()
        }
        _ = isEnabled
    }
}

/// Links the bundled CLI into ~/.local/bin so `portside` works in a terminal
/// and always matches the app's version.
enum CommandLineTool {
    static var linkURL: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".local/bin/portside")
    }

    static func install() throws -> URL {
        let fm = FileManager.default
        let link = linkURL
        try fm.createDirectory(at: link.deletingLastPathComponent(), withIntermediateDirectories: true)
        if (try? fm.destinationOfSymbolicLink(atPath: link.path)) != nil || fm.fileExists(atPath: link.path) {
            // Replaced files go to the Trash so nothing is lost.
            try fm.trashItem(at: link, resultingItemURL: nil)
        }
        try fm.createSymbolicLink(at: link, withDestinationURL: DaemonCLI.helperURL)
        return link
    }

    /// The account's login shell from the user database (an app's SHELL can differ).
    static var loginShell: String {
        if let pw = getpwuid(getuid()), let sh = pw.pointee.pw_shell {
            let s = String(cString: sh)
            if !s.isEmpty { return s }
        }
        return ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
    }

    /// Whether the login shell's PATH, which terminals get, has the link's
    /// directory. The app's own PATH never has it. False when the shell
    /// cannot be read within a few seconds.
    static func onLoginShellPath() async -> Bool {
        let shell = loginShell
        let dir = linkURL.deletingLastPathComponent().path
        return await withCheckedContinuation { cont in
            DispatchQueue.global().async {
                let marker = "__PATH_BEGIN__"
                let p = Process()
                p.executableURL = URL(fileURLWithPath: shell)
                // Interactive mode reads the rc files where most people set
                // PATH; the marker skips anything they print.
                var args = ["-l", "-c", "printf '\(marker)'; command env"]
                if ["zsh", "bash", "fish"].contains((shell as NSString).lastPathComponent) { args.insert("-i", at: 0) }
                p.arguments = args
                let out = Pipe()
                p.standardOutput = out
                p.standardError = FileHandle.nullDevice
                p.standardInput = FileHandle.nullDevice
                // Answered once: by the read, or by the timeout when the shell
                // hangs or a process it started keeps its output open.
                let lock = NSLock()
                var answered = false
                func answer(_ v: Bool) {
                    lock.lock()
                    defer { lock.unlock() }
                    if answered { return }
                    answered = true
                    cont.resume(returning: v)
                }
                do { try p.run() } catch {
                    answer(false)
                    return
                }
                // Interactive shells ignore SIGTERM.
                let timeout = DispatchWorkItem {
                    if p.isRunning { kill(p.processIdentifier, SIGKILL) }
                    answer(false)
                }
                DispatchQueue.global().asyncAfter(deadline: .now() + 5, execute: timeout)
                let data = out.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                timeout.cancel()
                let text = String(decoding: data, as: UTF8.self)
                guard let start = text.range(of: marker) else {
                    answer(false)
                    return
                }
                let path = text[start.upperBound...].split(separator: "\n").first { $0.hasPrefix("PATH=") }?.dropFirst(5) ?? ""
                answer(path.split(separator: ":").contains { $0 == Substring(dir) || $0 == Substring(dir + "/") })
            }
        }
    }

    /// The command that adds ~/.local/bin to PATH for the login shell, or nil
    /// for a shell without a known one.
    static var pathCommand: String? {
        switch (loginShell as NSString).lastPathComponent {
        case "fish": return "fish_add_path ~/.local/bin"
        case "zsh": return #"echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc"#
        case "bash": return #"echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bash_profile"#
        default: return nil
        }
    }
}

/// Native notifications: crashes (Restart, Show Logs), containers that
/// need attention (Show Logs), and devices that pair or connect. While the
/// app is connected the daemon leaves these to it.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    var onRestart: ((String) -> Void)?
    /// Shows a server of this computer: on its terminal, or on its page with its containers.
    var onShow: ((String, _ terminal: Bool) -> Void)?

    func setUp() {
        center.delegate = self
        let restart = UNNotificationAction(identifier: "restart", title: L.t("Restart", "Uruchom ponownie"))
        let show = UNNotificationAction(identifier: "show", title: L.t("Show Logs", "Pokaż logi"), options: [.foreground])
        center.setNotificationCategories([
            UNNotificationCategory(identifier: "crash", actions: [restart, show], intentIdentifiers: []),
            UNNotificationCategory(identifier: "container", actions: [show], intentIdentifiers: []),
        ])
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    func crashed(_ v: Server) {
        let content = UNMutableNotificationContent()
        content.title = L.t("\(v.name) crashed", "\(v.name): awaria")
        content.body = v.note.map(L.note) ?? ""
        content.sound = .default
        content.categoryIdentifier = "crash"
        content.userInfo = ["server": v.name, "terminal": true]
        center.add(UNNotificationRequest(identifier: "crash-\(v.name)", content: content, trigger: nil))
    }

    func containerIssue(_ server: String, _ containers: [Container]) {
        let content = UNMutableNotificationContent()
        content.title = L.t("\(server): container problem", "\(server): problem z kontenerem")
        content.body = containers.map { "\($0.shortName) \($0.problem)" }.joined(separator: ", ")
        content.sound = .default
        content.categoryIdentifier = "container"
        content.userInfo = ["server": server, "terminal": false]
        center.add(UNNotificationRequest(identifier: "container-\(server)", content: content, trigger: nil))
    }

    func devicePaired(_ d: PairedDevice) {
        device(d, id: "paired",
               title: L.t("\(d.name) paired", "Sparowano urządzenie \(d.name)"),
               body: L.t("It can now connect to this computer and control its servers.", "Może teraz łączyć się z tym komputerem i sterować jego serwerami."))
    }

    func deviceConnected(_ d: PairedDevice) {
        device(d, id: "connected",
               title: L.t("\(d.name) connected", "Urządzenie \(d.name) połączyło się"),
               body: L.t("It is using this computer's servers.", "Korzysta z serwerów tego komputera."))
    }

    private func device(_ d: PairedDevice, id: String, title: String, body: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        center.add(UNNotificationRequest(identifier: "device-\(id)-\(d.id)", content: content, trigger: nil))
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler done: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        let name = info["server"] as? String
        let terminal = info["terminal"] as? Bool ?? true
        let action = response.actionIdentifier
        Task { @MainActor in
            if let name {
                if action == "restart" { self.onRestart?(name) } else { self.onShow?(name, terminal) }
            }
            done()
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }
}

/// Light (default, from the bundle's AppIcon.icns) or dark Dock icon.
@MainActor
enum AppIconStyle: String, CaseIterable {
    case light, dark

    private static let key = "appIconStyle"

    static var current: AppIconStyle {
        get { AppIconStyle(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .light }
        set {
            UserDefaults.standard.set(newValue.rawValue, forKey: key)
            apply()
        }
    }

    /// Shows the chosen icon in the Dock and the app switcher while the app runs.
    static func apply() {
        switch current {
        case .light:
            NSApp.applicationIconImage = nil
        case .dark:
            if let url = Bundle.main.url(forResource: "AppIconDark", withExtension: "png"), let img = NSImage(contentsOf: url) {
                NSApp.applicationIconImage = img
            }
        }
    }

    var label: String {
        switch self {
        case .light: return L.t("Light", "Jasna")
        case .dark: return L.t("Dark", "Ciemna")
        }
    }
}
