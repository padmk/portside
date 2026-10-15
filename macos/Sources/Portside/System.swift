import AppKit
import ServiceManagement
import UserNotifications

/// Launch at login through the system's login items.
enum LoginItem {
    static var isEnabled: Bool { SMAppService.mainApp.status == .enabled }

    static func set(_ on: Bool, onError: (String) -> Void) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {
            onError(error.localizedDescription)
        }
        if on, SMAppService.mainApp.status == .requiresApproval {
            SMAppService.openSystemSettingsLoginItems()
        }
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

    static var onPath: Bool {
        let path = ProcessInfo.processInfo.environment["PATH"] ?? ""
        return path.split(separator: ":").contains { $0 == Substring(linkURL.deletingLastPathComponent().path) }
    }
}

/// Native crash notifications with Restart and Show Logs actions.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    var onRestart: ((String) -> Void)?
    var onShow: ((String) -> Void)?

    func setUp() {
        center.delegate = self
        let restart = UNNotificationAction(identifier: "restart", title: L.t("Restart", "Uruchom ponownie"))
        let show = UNNotificationAction(identifier: "show", title: L.t("Show Logs", "Pokaż logi"), options: [.foreground])
        center.setNotificationCategories([UNNotificationCategory(identifier: "crash", actions: [restart, show], intentIdentifiers: [])])
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    func crashed(_ v: Server) {
        let content = UNMutableNotificationContent()
        content.title = L.t("\(v.name) crashed", "\(v.name): awaria")
        content.body = v.note.map(L.note) ?? ""
        content.sound = .default
        content.categoryIdentifier = "crash"
        content.userInfo = ["server": v.name]
        center.add(UNNotificationRequest(identifier: "crash-\(v.name)", content: content, trigger: nil))
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler done: @escaping () -> Void) {
        let name = response.notification.request.content.userInfo["server"] as? String
        let action = response.actionIdentifier
        Task { @MainActor in
            if let name {
                if action == "restart" { self.onRestart?(name) } else { self.onShow?(name) }
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
