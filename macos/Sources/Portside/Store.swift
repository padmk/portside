import AppKit
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

    /// The daemon as the dashboard window presents it.
    enum DaemonStatus: Equatable {
        case connected
        case connecting
        case stopped
        case configError(String)
        case unreachable(String)

        /// No daemon comes back until the user starts one or fixes the config.
        var needsUser: Bool {
            switch self {
            case .stopped, .configError: return true
            default: return false
            }
        }
    }

    private(set) var connection: Connection = .connecting
    private(set) var servers: [Server] = []
    private(set) var groups: [String] = []
    /// Containers that need attention, by server.
    private(set) var containerIssues: [String: [Container]] = [:]
    /// From the daemon's state, or from `daemon info` when the config keeps a daemon from starting.
    private(set) var configError: String?
    private(set) var configPath: String?
    private(set) var stateDir: String?
    /// Mirrors the config's notify setting.
    private(set) var notifyOnCrash = true
    private(set) var daemonVersion: String?
    private(set) var bundledVersion: String?
    /// The other computer the dashboard shows, if any.
    private(set) var remote: RemoteInfo?
    private var baseURL: URL?
    private var token: String?
    var lastError: String?
    /// True while the daemon is stopped on purpose; the app then waits instead of starting it.
    private(set) var daemonStopped = false
    /// True while a daemon answers but cannot prove itself to this app or
    /// refuses its requests, such as one older than the app; a restart replaces it.
    private(set) var daemonRefused = false

    /// Called once per crash the daemon gave up on.
    var onCrash: ((Server) -> Void)?
    /// Called when containers of a server start needing attention.
    var onContainerIssue: ((String, [Container]) -> Void)?
    /// Called whenever the summary used by the status item changes.
    var onSummaryChange: ((Summary) -> Void)?
    /// Called whenever `daemonStatus` changes.
    var onStatusChange: ((DaemonStatus) -> Void)?
    /// Called when the events connection reaches another daemon process than
    /// before (a restart or an upgrade): after a cold start the dashboard has
    /// a new host, so the dashboard window signs in again.
    var onDaemonRestart: (() -> Void)?
    /// The epoch of the daemon the events connection last reached.
    private var connectedEpoch: String?

    private var task: URLSessionWebSocketTask?
    private var loop: Task<Void, Never>?
    /// The last crash reported per server (exit time and note).
    private var notifiedCrash: [String: String] = [:]
    /// Containers with an issue per server, as last seen.
    private var knownIssues: [String: Set<String>] = [:]
    /// False until the first state arrives; problems that already exist then are not announced.
    private var seeded = false
    /// Set by Start: the next check starts a daemon even after a deliberate stop.
    private var startRequested = false
    /// The config file's modification date when a start failed on it; the
    /// app starts no daemon again until the file changes.
    private var failedConfigDate: Date?

    struct Summary: Equatable {
        var running = 0
        var starting = 0
        var crashed = 0
        var external = 0
        /// Servers with a container that needs attention.
        var containerIssues = 0
        var total = 0
        var offline = false
    }

    var summary: Summary {
        var s = Summary(containerIssues: containerIssues.count, total: servers.count)
        if connection != .connected { s.offline = true }
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

    var daemonStatus: DaemonStatus {
        switch connection {
        case .connected: return .connected
        case .connecting: return .connecting
        case .offline(let msg):
            if daemonStopped { return .stopped }
            if let configError { return .configError(configError) }
            return .unreachable(msg)
        }
    }

    /// The daemon runs a different build than the one bundled with the app.
    var versionDiffers: Bool {
        guard let d = daemonVersion, let b = bundledVersion else { return false }
        return Self.plainVersion(d) != Self.plainVersion(b)
    }

    private static func plainVersion(_ v: String) -> String {
        v.hasPrefix("v") ? String(v.dropFirst()) : v
    }

    /// The API of the connected daemon; nil while none is connected.
    var api: APIClient? {
        guard connection == .connected, let baseURL else { return nil }
        return APIClient(base: baseURL, token: token)
    }

    /// The server's port carries the other computer's server, forwarded here.
    func forwardedFrom(_ v: Server) -> String? {
        if let from = v.forwardedFrom { return from }
        guard !v.isAlive, let remote, remote.mirrors(v.port) else { return nil }
        return remote.peer.name
    }

    func start() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    private func run() async {
        if bundledVersion == nil { bundledVersion = await DaemonCLI.bundledVersion() }
        var delay: UInt64 = 500_000_000
        while !Task.isCancelled {
            var wait = delay
            do {
                var info = try await DaemonCLI.info(start: false)
                if info.running != true, shouldStart(info) {
                    startRequested = false
                    info = try await DaemonCLI.info(start: true)
                    failedConfigDate = info.configError != nil ? Self.modificationDate(info.config) : nil
                }
                if Task.isCancelled { return }
                adopt(info)
                if info.running == true, let url = info.url.flatMap(URL.init(string:)) {
                    baseURL = url
                    token = info.token
                    // Known even when the events connection is refused, so the restart banner can show it.
                    if let v = info.version { daemonVersion = v }
                    if info.verified == false { throw DaemonError.unverified }
                    do {
                        try await listen(url)
                    } catch {
                        // A connection that worked starts the next attempt from the shortest delay.
                        if connection == .connected { delay = 500_000_000 }
                        throw error
                    }
                } else {
                    // Wait for an explicit start, an external one or a config edit.
                    wait = 2_000_000_000
                }
            } catch {
                if Task.isCancelled { return }
                daemonRefused = (error as? DaemonError)?.needsRestart ?? false
                setOffline(error.localizedDescription, clear: false)
                wait = delay
                delay = min(delay * 2, 8_000_000_000)
            }
            try? await Task.sleep(nanoseconds: wait)
        }
    }

    /// Whether to start a daemon that is not running: not after a deliberate
    /// stop, and not again for a config that already failed, unless the user
    /// asked for it.
    private func shouldStart(_ info: DaemonInfo) -> Bool {
        if startRequested { return true }
        if info.stopped == true { return false }
        if info.configError != nil, let failed = failedConfigDate, failed == Self.modificationDate(info.config) { return false }
        return true
    }

    private static func modificationDate(_ path: String?) -> Date? {
        guard let path else { return nil }
        return (try? FileManager.default.attributesOfItem(atPath: path))?[.modificationDate] as? Date
    }

    /// Takes what `daemon info` says about a daemon that is not running.
    private func adopt(_ info: DaemonInfo) {
        if let c = info.config { configPath = c }
        if let s = info.stateDir { stateDir = s }
        guard info.running != true else { return }
        daemonRefused = false
        daemonStopped = info.stopped == true
        configError = info.configError
        setOffline(daemonStopped ? "stopped" : (info.configError ?? L.t("The daemon is not running.", "Demon nie działa.")), clear: true)
    }

    /// Marks the daemon unreachable. Servers stay (dimmed) while it may come
    /// back; `clear` drops them once it is known not to run.
    private func setOffline(_ msg: String, clear: Bool) {
        connection = .offline(msg)
        baseURL = nil
        remote = nil
        if clear {
            servers = []
            containerIssues = [:]
        }
        notifyChange()
    }

    /// Follows the daemon's events. The daemon proves itself before the
    /// signed upgrade and again in its reply; an upgrade refused for an old
    /// epoch (the daemon restarted) is tried once more after a new handshake.
    private func listen(_ base: URL) async throws {
        var comps = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        comps.scheme = "ws"
        comps.path = "/api/events"
        comps.queryItems = [URLQueryItem(name: "notifier", value: "1")]
        var retried = false
        while true {
            let epoch = try await DaemonHandshake.shared.epoch(base: base, token: token)
            var req = URLRequest(url: comps.url!)
            guard let challenge = RequestSigner.sign(&req, token: token, epoch: epoch) else { throw DaemonError.unverified }
            let ws = URLSession.shared.webSocketTask(with: req)
            ws.maximumMessageSize = 32 << 20
            task = ws
            ws.resume()
            defer { ws.cancel(with: .goingAway, reason: nil) }
            var msg: URLSessionWebSocketTask.Message
            do {
                msg = try await ws.receive()
            } catch {
                guard (ws.response as? HTTPURLResponse)?.statusCode == 401 else { throw error }
                if !retried, await DaemonHandshake.shared.renewed(base: base, token: token, after: epoch) {
                    retried = true
                    continue
                }
                throw DaemonError.refused
            }
            guard let http = ws.response as? HTTPURLResponse,
                  RequestSigner.proven(http, token: token, challenge: challenge) != nil else {
                await DaemonHandshake.shared.forget(base)
                throw DaemonError.unverified
            }
            // Told once the state is in, when the dashboard can be reached.
            var restarted = connectedEpoch != nil && connectedEpoch != epoch
            connectedEpoch = epoch
            while true {
                let data: Data
                switch msg {
                case .string(let s): data = Data(s.utf8)
                case .data(let d): data = d
                @unknown default: data = Data()
                }
                if let state = try? JSONDecoder().decode(DaemonState.self, from: data) {
                    apply(state)
                    if restarted {
                        restarted = false
                        onDaemonRestart?()
                    }
                }
                if Task.isCancelled { return }
                msg = try await ws.receive()
            }
        }
    }

    private func apply(_ state: DaemonState) {
        connection = .connected
        daemonStopped = false
        daemonRefused = false
        daemonVersion = state.daemon.version
        configError = state.config.error
        configPath = state.config.path
        notifyOnCrash = state.config.notify ?? true
        groups = state.groups ?? []
        servers = state.servers
        remote = state.remote
        var issues: [String: [Container]] = [:]
        for c in state.docker?.containers ?? [] {
            for name in c.issueFor ?? [] { issues[name, default: []].append(c) }
        }
        containerIssues = issues
        announce(state.servers, issues)
        notifyChange()
    }

    /// Notifies once per crash the daemon gave up on (not while it restarts
    /// the server on its own) and when containers start needing attention.
    /// Exit time and note identify a crash, so batched updates miss none.
    private func announce(_ list: [Server], _ issues: [String: [Container]]) {
        let notify = seeded && notifyOnCrash
        for v in list where v.status == "crashed" && !v.waitsForRestart {
            let key = (v.exitedAt ?? "") + "|" + (v.note ?? "")
            if notifiedCrash[v.name] != key {
                notifiedCrash[v.name] = key
                if notify { onCrash?(v) }
            }
        }
        for (name, containers) in issues {
            let fresh = containers.filter { !(knownIssues[name]?.contains($0.name) ?? false) }
            if notify, !fresh.isEmpty { onContainerIssue?(name, fresh) }
        }
        knownIssues = issues.mapValues { Set($0.map(\.name)) }
        seeded = true
    }

    private var lastSummary: Summary?
    private var lastStatus: DaemonStatus?
    private func notifyChange() {
        let s = summary
        if s != lastSummary {
            lastSummary = s
            onSummaryChange?(s)
        }
        let st = daemonStatus
        if st != lastStatus {
            lastStatus = st
            onStatusChange?(st)
        }
    }

    struct Section {
        let id: String
        /// nil for the ungrouped servers.
        let group: String?
        let servers: [Server]
    }

    /// Servers grouped as the dashboard shows them: groups first, ungrouped last.
    var sections: [Section] {
        var out: [Section] = []
        for g in groups {
            let members = servers.filter { $0.group == g }
            if !members.isEmpty { out.append(Section(id: "group:" + g, group: g, servers: members)) }
        }
        let rest = servers.filter { ($0.group ?? "").isEmpty }
        if !rest.isEmpty { out.append(Section(id: "ungrouped", group: nil, servers: rest)) }
        return out
    }

    // MARK: actions

    /// The connected daemon's API, or nil after saying that none is running.
    private func requireAPI() -> APIClient? {
        if let api { return api }
        lastError = daemonStopped
            ? L.t("The daemon is stopped. Start it first.", "Demon jest zatrzymany. Najpierw go uruchom.")
            : L.t("The daemon is not running.", "Demon nie działa.")
        return nil
    }

    func perform(_ action: String, _ name: String, takeover: Bool = false) {
        guard let api = requireAPI() else { return }
        Task {
            do {
                try await api.post("api/servers/\(name)/\(action)", body: takeover ? ["takeover": true] : nil)
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func bulk(_ action: String, names: [String]? = nil) {
        guard let api = requireAPI() else { return }
        Task {
            do {
                var body: [String: Any] = ["action": action]
                if let names { body["names"] = names } else { body["all"] = true }
                let data = try await api.post("api/bulk", body: body)
                // The request succeeds as a whole; individual servers can still fail.
                let results = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["results"] as? [[String: Any]] ?? []
                let failures = results.compactMap { r -> String? in
                    guard let err = r["error"] as? String, !err.isEmpty, let name = r["name"] as? String else { return nil }
                    return "\(name): \(L.note(err))"
                }
                if !failures.isEmpty { lastError = failures.joined(separator: "\n") }
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    /// Ends the connection to another computer, so the dashboard shows this one.
    /// Returns once this app's state no longer shows the remote.
    func disconnectRemote() async -> Bool {
        guard let api = requireAPI() else { return false }
        do {
            try await api.post("api/remote/disconnect", body: [:])
        } catch {
            lastError = error.localizedDescription
            return false
        }
        for _ in 0..<20 where remote != nil {
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        return true
    }

    func open(_ target: String, name: String? = nil) {
        guard let api else {
            // Without a daemon (stopped, or a broken config) open the files
            // directly, which is exactly when they are needed.
            openLocally(target)
            return
        }
        Task {
            var body: [String: Any] = ["target": target]
            if let name { body["name"] = name }
            do { try await api.post("api/open", body: body) } catch { lastError = error.localizedDescription }
        }
    }

    private func openLocally(_ target: String) {
        let env = ProcessInfo.processInfo.environment
        let home = FileManager.default.homeDirectoryForCurrentUser
        let config = configPath.map(URL.init(fileURLWithPath:))
            ?? env["PORTSIDE_CONFIG"].map(URL.init(fileURLWithPath:))
            ?? home.appendingPathComponent(".config/portside/portside.toml")
        let state = stateDir.map(URL.init(fileURLWithPath:))
            ?? env["PORTSIDE_STATE_DIR"].map(URL.init(fileURLWithPath:))
            ?? home.appendingPathComponent(".local/state/portside")
        switch target {
        case "config":
            if FileManager.default.fileExists(atPath: config.path) {
                NSWorkspace.shared.open(config)
            } else {
                lastError = L.t("No config file yet at \(config.path).", "Brak pliku konfiguracji: \(config.path).")
            }
        case "logs":
            NSWorkspace.shared.activateFileViewerSelecting([state.appendingPathComponent("logs")])
        default:
            _ = requireAPI()
        }
    }

    /// Stops the connection loop, runs a daemon command, then resumes the loop.
    private func pauseLoop(while command: [String], before: () -> Void) {
        task?.cancel(with: .goingAway, reason: nil)
        let running = loop
        loop?.cancel()
        before()
        Task {
            // Let the connection loop (and any `daemon info` that could start
            // a daemon) finish first, so it does not race the command.
            await running?.value
            do { _ = try await DaemonCLI.run(command) } catch { lastError = error.localizedDescription }
            start()
        }
    }

    func restartDaemon() {
        pauseLoop(while: ["daemon", "restart"]) {
            connection = .connecting
            baseURL = nil
            notifyChange()
        }
    }

    func stopDaemon() {
        pauseLoop(while: ["daemon", "stop"]) {
            daemonStopped = true
            setOffline("stopped", clear: true)
        }
    }

    func startDaemon() {
        daemonStopped = false
        startRequested = true
        failedConfigDate = nil
        connection = .connecting
        notifyChange()
        start()
    }
}
