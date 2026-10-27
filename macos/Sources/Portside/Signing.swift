import CryptoKit
import Foundation

/// Signs requests to the daemon and checks its proofs as internal/signing
/// does. A signature is an HMAC-SHA256, keyed by the API token (hex), over
/// the method, the request URI as sent, the Unix time, a random nonce, the
/// body hash and the daemon's epoch, joined by newlines. A reply's proof is
/// HMAC(token, "server", challenge, epoch, status, body hash); a WebSocket
/// upgrade's HMAC(token, "server", challenge, epoch). The token itself is
/// never sent.
enum RequestSigner {
    static func header(_ name: String) -> String { "X-\(Brand.name)-\(name)" }

    /// Signs a request whose method, URL and body are final, for epoch, and
    /// adds a fresh challenge for the reply's proof, which it returns.
    static func sign(_ req: inout URLRequest, token: String?, epoch: String) -> String? {
        guard let key = key(token), let url = req.url,
              let comps = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let nonce = randomHex(16), let challenge = randomHex(16) else { return nil }
        var uri = comps.percentEncodedPath.isEmpty ? "/" : comps.percentEncodedPath
        if let q = comps.percentEncodedQuery { uri += "?" + q }
        let date = String(Int(Date().timeIntervalSince1970))
        let bodyHash = hex(Data(SHA256.hash(data: req.httpBody ?? Data())))
        let mac = HMAC<SHA256>.authenticationCode(
            for: Data([req.httpMethod ?? "GET", uri, date, nonce, bodyHash, epoch].joined(separator: "\n").utf8),
            using: key)
        req.setValue(date, forHTTPHeaderField: header("Date"))
        req.setValue(nonce, forHTTPHeaderField: header("Nonce"))
        req.setValue(epoch, forHTTPHeaderField: header("Epoch"))
        req.setValue(hex(Data(mac)), forHTTPHeaderField: header("Signature"))
        req.setValue(challenge, forHTTPHeaderField: header("Challenge"))
        return challenge
    }

    /// Adds a fresh challenge to an unsigned request and returns it.
    static func challenge(_ req: inout URLRequest) -> String? {
        guard let ch = randomHex(16) else { return nil }
        req.setValue(ch, forHTTPHeaderField: header("Challenge"))
        return ch
    }

    /// The epoch a WebSocket upgrade proves for challenge, or nil when it
    /// does not prove that the daemon holds the token.
    static func proven(_ resp: HTTPURLResponse, token: String?, challenge: String) -> String? {
        check(resp, token: token, challenge: challenge) { epoch in ["server", challenge, epoch] }
    }

    /// The epoch a whole reply proves for challenge, or nil when it does not
    /// prove that the daemon holds the token and sent this status and body.
    /// The proof is HMAC(token, "server", challenge, epoch, status, body hash).
    static func provenReply(_ resp: HTTPURLResponse, body: Data, token: String?, challenge: String) -> String? {
        let bodyHash = hex(Data(SHA256.hash(data: body)))
        return check(resp, token: token, challenge: challenge) { epoch in
            ["server", challenge, epoch, String(resp.statusCode), bodyHash]
        }
    }

    private static func check(_ resp: HTTPURLResponse, token: String?, challenge: String, parts: (String) -> [String]) -> String? {
        guard let key = key(token), let epoch = resp.value(forHTTPHeaderField: header("Epoch")), isToken(epoch),
              let proof = resp.value(forHTTPHeaderField: header("Proof")).flatMap(hexData), isToken(challenge) else { return nil }
        let message = Data(parts(epoch).joined(separator: "\n").utf8)
        return HMAC<SHA256>.isValidAuthenticationCode(proof, authenticating: message, using: key) ? epoch : nil
    }

    private static func key(_ token: String?) -> SymmetricKey? {
        guard let token, let k = hexData(token), k.count == 32 else { return nil }
        return SymmetricKey(data: k)
    }

    /// 16 to 32 bytes of hex: the form of challenges and epochs.
    private static func isToken(_ s: String) -> Bool {
        (32...64).contains(s.utf8.count) && hexData(s) != nil
    }

    private static func randomHex(_ n: Int) -> String? {
        var bytes = [UInt8](repeating: 0, count: n)
        guard SecRandomCopyBytes(kSecRandomDefault, n, &bytes) == errSecSuccess else { return nil }
        return hex(Data(bytes))
    }

    private static func hex(_ d: Data) -> String { d.map { String(format: "%02x", $0) }.joined() }

    /// Decodes hex digits only, as Go's hex.DecodeString does: no signs,
    /// spaces or prefixes.
    private static func hexData(_ s: String) -> Data? {
        let chars = Array(s.utf8)
        guard chars.count % 2 == 0 else { return nil }
        func nibble(_ c: UInt8) -> UInt8? {
            switch c {
            case UInt8(ascii: "0")...UInt8(ascii: "9"): return c - UInt8(ascii: "0")
            case UInt8(ascii: "a")...UInt8(ascii: "f"): return c - UInt8(ascii: "a") + 10
            case UInt8(ascii: "A")...UInt8(ascii: "F"): return c - UInt8(ascii: "A") + 10
            default: return nil
            }
        }
        var out = Data(capacity: chars.count / 2)
        var i = 0
        while i < chars.count {
            guard let hi = nibble(chars[i]), let lo = nibble(chars[i + 1]) else { return nil }
            out.append(hi << 4 | lo)
            i += 2
        }
        return out
    }
}

/// The epoch each daemon last proved. Before the first signed request to a
/// daemon, and after it refused one (it restarted), the app asks
/// GET /api/health with a fresh challenge and checks the proof; nothing
/// signed goes to a daemon that cannot prove it holds the token.
actor DaemonHandshake {
    static let shared = DaemonHandshake()
    private var epochs: [URL: String] = [:]

    /// The epoch to sign for, after a handshake when none is known.
    func epoch(base: URL, token: String?) async throws -> String {
        if let e = epochs[base] { return e }
        return try await hello(base: base, token: token)
    }

    /// Repeats the handshake after a refusal and reports whether the daemon
    /// proved a new epoch, so the request is worth sending again.
    func renewed(base: URL, token: String?, after old: String) async -> Bool {
        epochs[base] = nil
        guard let e = try? await hello(base: base, token: token) else { return false }
        return e != old
    }

    func forget(_ base: URL) { epochs[base] = nil }

    private func hello(base: URL, token: String?) async throws -> String {
        var req = URLRequest(url: base.appendingPathComponent("api/health"))
        req.timeoutInterval = 5
        req.cachePolicy = .reloadIgnoringLocalCacheData
        guard let challenge = RequestSigner.challenge(&req) else { throw DaemonError.unverified }
        let (data, resp) = try await DaemonSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse, http.statusCode == 200,
              let epoch = RequestSigner.provenReply(http, body: data, token: token, challenge: challenge) else {
            epochs[base] = nil
            throw DaemonError.unverified
        }
        epochs[base] = epoch
        return epoch
    }
}

/// The session for requests to the daemon. It never follows a redirect: the
/// redirect is the reply to check, and following it would send the request,
/// signed and with its body, to wherever an unproven listener points.
enum DaemonSession {
    static let shared = URLSession(configuration: .default, delegate: NoRedirects(), delegateQueue: nil)

    private final class NoRedirects: NSObject, URLSessionTaskDelegate, Sendable {
        func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                        newRequest request: URLRequest) async -> URLRequest? {
            nil
        }
    }
}
