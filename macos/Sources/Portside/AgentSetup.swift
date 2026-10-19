import AppKit
import Observation
import SwiftUI

/// One step of the coding-agent setup, as `setup --json` reports it
/// (internal/agentsetup).
struct AgentSetupItem: Decodable, Identifiable {
    let id: String
    let label: String
    let state: String
    let applicable: Bool
    let command: String
    let detail: String?

    /// The helper can apply it without replacing anything the user wrote.
    var fixable: Bool { applicable && (state == "missing" || state == "outdated") }
}

/// What `setup --apply` did for one item.
struct AgentSetupResult: Decodable {
    let id: String
    let ok: Bool
    let error: String?
    let command: String?
}

/// Loads the setup items through the bundled helper and applies the chosen ones.
@MainActor
@Observable
final class AgentSetupModel {
    enum Phase: Equatable {
        case loading, ready, applying, done
        case failed(String)
    }

    private(set) var phase: Phase = .loading
    /// Items of the agents installed for this account.
    private(set) var items: [AgentSetupItem] = []
    var selected: Set<String> = []
    private(set) var results: [String: AgentSetupResult] = [:]

    /// Something an installed agent lacks.
    var needsAttention: Bool { items.contains { $0.state != "ok" } }

    func load() async {
        phase = .loading
        results = [:]
        do {
            let data = try await DaemonCLI.run(["setup", "--json"])
            items = try JSONDecoder().decode([AgentSetupItem].self, from: data).filter(\.applicable)
            selected = Set(items.filter(\.fixable).map(\.id))
            phase = .ready
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    func connect() async {
        let ids = items.filter { selected.contains($0.id) && $0.fixable }.map(\.id)
        guard !ids.isEmpty else { return }
        phase = .applying
        do {
            // Exit 1 means an item failed; the reply still lists every item.
            let out = try await DaemonCLI.exec(["setup", "--apply", ids.joined(separator: ","), "--yes"])
            guard let list = try? JSONDecoder().decode([AgentSetupResult].self, from: out.stdout) else {
                throw DaemonError.failed(out.stderr.isEmpty ? L.t("Setup failed.", "Konfiguracja nie powiodła się.") : out.stderr)
            }
            results = Dictionary(list.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            phase = .done
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }
}

/// The window that connects Claude Code and Codex: offered once after the
/// first connection and from Settings.
@MainActor
final class AgentSetupWindowController: NSWindowController, NSWindowDelegate {
    let model = AgentSetupModel()

    init() {
        let window = NSWindow(contentRect: .zero, styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = AgentSetupView.title
        window.isReleasedWhenClosed = false
        super.init(window: window)
        window.delegate = self
        let host = NSHostingController(rootView: AgentSetupView(model: model) { [weak window] in window?.close() })
        host.sizingOptions = [.preferredContentSize]
        window.contentViewController = host
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError() }

    /// Shows the window and loads the items again.
    func present() {
        Task { await model.load() }
        show()
    }

    /// Shows the window with items already loaded.
    func show() {
        NSApp.activate()
        window?.center()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }
}

struct AgentSetupView: View {
    static let title = L.t("Connect Coding Agents", "Podłącz agentów kodujących")

    @Bindable var model: AgentSetupModel
    let close: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(L.t("The MCP server and the skill let Claude Code and Codex find, start and debug your local servers.",
                     "Serwer MCP i umiejętność pozwalają Claude Code i Codex znajdować, uruchamiać i debugować lokalne serwery."))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            content
            HStack {
                if model.phase == .applying { ProgressView().controlSize(.small) }
                Spacer()
                buttons
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    @ViewBuilder private var content: some View {
        switch model.phase {
        case .loading:
            ProgressView().controlSize(.small)
        case .failed(let msg):
            Text(msg).foregroundStyle(.red).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        default:
            if model.items.isEmpty {
                Text(L.t("Neither Claude Code nor Codex is installed for this account.",
                         "Claude Code ani Codex nie są zainstalowane na tym koncie."))
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.items) { row($0) }
                }
            }
        }
    }

    @ViewBuilder private func row(_ item: AgentSetupItem) -> some View {
        let label = Self.label(item.label)
        if let r = model.results[item.id] {
            if r.ok {
                status("checkmark.circle.fill", .green, label)
            } else {
                VStack(alignment: .leading, spacing: 3) {
                    status("xmark.circle.fill", .red, label)
                    if let error = r.error, !error.isEmpty { note(error) }
                    if let cmd = r.command { manual(cmd) }
                }
            }
        } else if item.state == "ok" {
            status("checkmark.circle.fill", .green, label)
        } else if item.fixable {
            Toggle(isOn: Binding(
                get: { model.selected.contains(item.id) },
                set: { on in if on { model.selected.insert(item.id) } else { model.selected.remove(item.id) } })) {
                Text(item.state == "outdated" ? label + L.t(" (update)", " (aktualizacja)") : label)
            }
            .toggleStyle(.checkbox)
            .disabled(model.phase != .ready)
        } else {
            VStack(alignment: .leading, spacing: 3) {
                status("exclamationmark.triangle.fill", .orange, label)
                note(item.state == "foreign"
                     ? L.t("A skill with this name exists that \(Brand.name) did not install. To replace it, run:",
                           "Istnieje umiejętność o tej nazwie, której \(Brand.name) nie zainstalował. Aby ją zastąpić, uruchom:")
                     : L.t("The \(item.command.split(separator: " ").first ?? "") command was not found. Run it yourself:",
                           "Nie znaleziono polecenia \(item.command.split(separator: " ").first ?? ""). Uruchom samodzielnie:"))
                manual(item.command)
            }
        }
    }

    private func status(_ symbol: String, _ color: Color, _ text: String) -> some View {
        Label {
            Text(text)
        } icon: {
            Image(systemName: symbol).foregroundStyle(color)
        }
    }

    private func note(_ text: String) -> some View {
        Text(text).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true).padding(.leading, 26)
    }

    private func manual(_ command: String) -> some View {
        Text(command).font(.system(.callout, design: .monospaced)).textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true).padding(.leading, 26)
    }

    @ViewBuilder private var buttons: some View {
        let canConnect = model.items.contains { $0.fixable } && model.results.isEmpty
        if (model.phase == .ready || model.phase == .applying) && canConnect {
            Button(L.t("Not Now", "Nie teraz"), action: close).keyboardShortcut(.cancelAction)
            Button(L.t("Connect", "Połącz")) { Task { await model.connect() } }
                .keyboardShortcut(.defaultAction)
                .disabled(model.phase != .ready || !model.items.contains { $0.fixable && model.selected.contains($0.id) })
        } else {
            Button(L.t("Done", "Gotowe"), action: close).keyboardShortcut(.defaultAction)
        }
    }

    /// The helper's English labels in the app's language.
    static func label(_ s: String) -> String {
        guard L.polish else { return s }
        if s.hasSuffix(" MCP server") { return "Serwer MCP dla " + s.dropLast(" MCP server".count) }
        if s.hasPrefix("Skill for ") {
            return "Umiejętność dla " + s.dropFirst("Skill for ".count).replacingOccurrences(of: " and ", with: " i ")
        }
        return s
    }
}
