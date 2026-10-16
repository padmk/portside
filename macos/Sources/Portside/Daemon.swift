import Foundation

// JSON shapes from the daemon (see internal/server/routes.go and
// internal/supervisor/view.go). Only the fields the app uses are decoded.

struct DaemonState: Decodable {
    let daemon: Health
    let config: ConfigInfo
    let servers: [Server]
    let groups: [String]?
    let docker: DockerState?
}

struct DockerState: Decodable {
    let status: String
    let containers: [Container]?
}

struct Container: Decodable, Hashable {
    let name: String
    let service: String?
    let state: String
    let health: String?
    let failed: Bool?
    /// Servers this container needs attention for; the daemon decides.
    let issueFor: [String]?

    /// The compose service it runs, else its name.
    var shortName: String { service ?? name }

    /// What is wrong with it, in a word.
    var problem: String {
        if state == "restarting" { return L.t("restarting", "restartuje się") }
        if state == "running" && health == "unhealthy" { return L.t("unhealthy", "niesprawny") }
        return L.t("crashed", "padł")
    }
}

struct Health: Decodable {
    let version: String
    let pid: Int
    let port: Int
}

struct ConfigInfo: Decodable {
    let path: String
    let error: String?
    let notify: Bool?
}

struct PortHolder: Decodable, Hashable {
    let pid: Int?
    let command: String?
}

struct Server: Decodable, Identifiable, Hashable {
    let name: String
    let group: String?
    let dir: String
    let dirDisplay: String
    let port: Int
    let url: String?
    let alias: String?
    let status: String
    let note: String?
    let pid: Int?
    let external: PortHolder?
    let stale: Bool?
    let enabled: Bool
    let cpu: Double
    let mem: Int64

    var id: String { name }

    /// What to show: a stopped server whose port is held elsewhere is "external".
    var displayStatus: String {
        if status == "stopped", external != nil { return "external" }
        return status
    }

    var isAlive: Bool { status == "running" || status == "starting" || status == "stopping" }
}

struct DaemonInfo: Decodable {
    let url: String
    let port: Int
    let pid: Int
    let version: String
    let cli: String
    let tokenFile: String?

    /// The API token, read from its private file (never passed through argv or logs).
    var token: String? {
        guard let tokenFile, let s = try? String(contentsOfFile: tokenFile, encoding: .utf8) else { return nil }
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }
}

enum DaemonError: LocalizedError {
    case helperMissing
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .helperMissing: return "The bundled command-line tool is missing from the app."
        case .failed(let msg): return msg
        }
    }
}

/// Runs the bundled `portside` binary and talks to the daemon over HTTP.
enum DaemonCLI {
    static var helperURL: URL {
        Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/portside")
    }

    /// Runs the helper with arguments and returns stdout.
    static func run(_ args: [String]) async throws -> Data {
        let url = helperURL
        guard FileManager.default.isExecutableFile(atPath: url.path) else { throw DaemonError.helperMissing }
        let p = Process()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { cont in
            p.executableURL = url
            p.arguments = args
            let out = Pipe(), err = Pipe()
            p.standardOutput = out
            p.standardError = err
            p.standardInput = FileHandle.nullDevice
            p.terminationHandler = { proc in
                let data = out.fileHandleForReading.readDataToEndOfFile()
                let errData = err.fileHandleForReading.readDataToEndOfFile()
                if proc.terminationStatus == 0 {
                    cont.resume(returning: data)
                } else {
                    let msg = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    cont.resume(throwing: DaemonError.failed(msg.isEmpty ? "portside \(args.joined(separator: " ")) failed" : msg))
                }
            }
            do { try p.run() } catch { cont.resume(throwing: error) }
            }
        } onCancel: {
            // A cancelled call must not keep going (e.g. start a daemon the user just stopped).
            if p.isRunning { p.terminate() }
        }
    }

    /// Starts the daemon if needed and returns where it listens.
    static func info() async throws -> DaemonInfo {
        let data = try await run(["daemon", "info"])
        return try JSONDecoder().decode(DaemonInfo.self, from: data)
    }

    static func bundledVersion() async -> String? {
        guard let data = try? await run(["version"]), let s = String(data: data, encoding: .utf8) else { return nil }
        // "portside 0.1.0 (darwin/arm64)"
        let parts = s.split(separator: " ")
        return parts.count >= 2 ? String(parts[1]) : nil
    }
}

struct APIClient {
    let base: URL
    let token: String?

    func authorize(_ req: inout URLRequest) {
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
    }

    /// A one-time link that signs a browser (or the dashboard window) in.
    func signInURL(next: String) async throws -> URL {
        var req = URLRequest(url: base.appendingPathComponent("api/auth/code"))
        req.httpMethod = "POST"
        authorize(&req)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["next": next])
        let (data, _) = try await URLSession.shared.data(for: req)
        guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let s = obj["url"] as? String, let url = URL(string: s) else {
            throw DaemonError.failed("Could not create a sign-in link")
        }
        return url
    }

    @discardableResult
    func post(_ path: String, body: [String: Any]? = nil) async throws -> Data {
        var req = URLRequest(url: base.appendingPathComponent(path))
        req.httpMethod = "POST"
        req.timeoutInterval = 120
        authorize(&req)
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, resp) = try await URLSession.shared.data(for: req)
        if let http = resp as? HTTPURLResponse, http.statusCode >= 400 {
            let msg = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw DaemonError.failed(msg ?? "Request failed (\(http.statusCode))")
        }
        return data
    }
}
