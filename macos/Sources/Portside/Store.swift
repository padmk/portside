import Foundation
import Observation

/// Live daemon state for the menu bar and window, fed by the events WebSocket.
@MainActor
@Observable
final class Store {
    enum Connection: Equatable {
        case connecting
        case connected
        case offline(String)
    }

    private(set) var connection: Connection = .connecting
    private(set) var servers: [Server] = []
    private(set) var groups: [String] = []
    private(set) var configError: String?
    private(set) var daemonVersion: String?
    private(set) var bundledVersion: String?
    private(set) var baseURL: URL?
    private(set) var token: String?
    var lastError: String?
    /// True after the user stopped the daemon; the app then waits instead of restarting it.
    private(set) var daemonStopped = false

    /// Called when a server crashes (it was alive and is now crashed).
    var onCrash: ((Server) -> Void)?
    /// Called whenever the summary used by the status item changes.
    var onSummaryChange: ((Summary) -> Void)?

    private var task: URLSessionWebSocketTask?
    private var loop: Task<Void, Never>?
    private var previous: [String: String] = [:]

    struct Summary: Equatable {
        var running = 0
        var starting = 0
        var crashed = 0
        var external = 0
        var total = 0
        var offline = false
    }

    var summary: Summary {
        var s = Summary(total: servers.count)
        if case .offline = connection { s.offline = true }
        for v in servers {
            switch v.displayStatus {
            case "running": s.running += 1
            case "starting", "stopping": s.starting += 1
            case "crashed", "blocked": s.crashed += 1
            case "external": s.external += 1
            default: break
            }
        }
        return s
    }

    var updateAvailable: Bool {
        guard let d = daemonVersion, let b = bundledVersion else { return false }
        return d != b
    }

    var api: APIClient? { baseURL.map { APIClient(base: $0, token: token) } }

    func start() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    private func run() async {
        if bundledVersion == nil { bundledVersion = await DaemonCLI.bundledVersion() }
        var delay: UInt64 = 500_000_000
        while !Task.isCancelled {
            if daemonStopped { return }
            do {
                if baseURL == nil || connection != .connected {
                    let info = try await DaemonCLI.info()
                    baseURL = URL(string: info.url)
                    token = info.token
                }
                guard let base = baseURL else { throw DaemonError.failed("No daemon address") }
                try await listen(base)
                delay = 500_000_000
            } catch {
                if Task.isCancelled { return }
                connection = .offline(error.localizedDescription)
                notifySummary()
                baseURL = nil
            }
            try? await Task.sleep(nanoseconds: delay)
            delay = min(delay * 2, 8_000_000_000)
        }
    }

    private func listen(_ base: URL) async throws {
        var comps = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        comps.scheme = "ws"
        comps.path = "/api/events"
        comps.queryItems = [URLQueryItem(name: "notifier", value: "1")]
        var req = URLRequest(url: comps.url!)
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let ws = URLSession.shared.webSocketTask(with: req)
        ws.maximumMessageSize = 32 << 20
        task = ws
        ws.resume()
        defer { ws.cancel(with: .goingAway, reason: nil) }
        while !Task.isCancelled {
            let msg = try await ws.receive()
            let data: Data
            switch msg {
            case .string(let s): data = Data(s.utf8)
            case .data(let d): data = d
            @unknown default: continue
            }
            guard let state = try? JSONDecoder().decode(DaemonState.self, from: data) else { continue }
            apply(state)
        }
    }

    private func apply(_ state: DaemonState) {
        connection = .connected
        daemonVersion = state.daemon.version
        configError = state.config.error
        groups = state.groups ?? []
        servers = state.servers
        for v in state.servers {
            let before = previous[v.name]
            if v.status == "crashed", let before, before != "crashed", before != "stopped" {
                onCrash?(v)
            }
            previous[v.name] = v.status
        }
        notifySummary()
    }

    private var lastSummary: Summary?
    private func notifySummary() {
        let s = summary
        if s != lastSummary {
            lastSummary = s
            onSummaryChange?(s)
        }
    }

    /// Servers grouped as the dashboard shows them: groups first, ungrouped last.
    var sections: [(title: String?, servers: [Server])] {
        var out: [(String?, [Server])] = []
        for g in groups {
            let members = servers.filter { $0.group == g }
            if !members.isEmpty { out.append((g, members)) }
        }
        let rest = servers.filter { ($0.group ?? "").isEmpty }
        if !rest.isEmpty { out.append((groups.isEmpty ? nil : "Other", rest)) }
        return out
    }

    // MARK: actions

    func perform(_ action: String, _ name: String, takeover: Bool = false) {
        Task {
            do {
                try await api?.post("api/servers/\(name)/\(action)", body: takeover ? ["takeover": true] : nil)
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func bulk(_ action: String, names: [String]? = nil) {
        Task {
            do {
                var body: [String: Any] = ["action": action]
                if let names { body["names"] = names } else { body["all"] = true }
                try await api?.post("api/bulk", body: body)
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func open(_ target: String, name: String? = nil) {
        Task {
            var body: [String: Any] = ["target": target]
            if let name { body["name"] = name }
            do { try await api?.post("api/open", body: body) } catch { lastError = error.localizedDescription }
        }
    }

    func restartDaemon() {
        Task {
            connection = .connecting
            do {
                _ = try await DaemonCLI.run(["daemon", "restart"])
            } catch {
                lastError = error.localizedDescription
            }
            baseURL = nil
            start()
        }
    }

    func stopDaemon() {
        daemonStopped = true
        task?.cancel(with: .goingAway, reason: nil)
        let running = loop
        loop?.cancel()
        Task {
            // Let the connection loop (and any `daemon info` that could start
            // a daemon) finish before stopping, so nothing restarts it after.
            await running?.value
            do { _ = try await DaemonCLI.run(["daemon", "stop"]) } catch { lastError = error.localizedDescription }
            connection = .offline("stopped")
            notifySummary()
        }
    }

    func startDaemon() {
        daemonStopped = false
        connection = .connecting
        baseURL = nil
        start()
    }
}
