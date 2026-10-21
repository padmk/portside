import AppKit
import WebKit

/// The dashboard in a native window. Links that leave the dashboard open in
/// the default browser.
@MainActor
final class DashboardWindowController: NSWindowController, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    private let webView: WKWebView
    private var base: URL?
    /// The dashboard's own host (<label>.localhost), named with its sign-in
    /// link; its session cookie is valid there only.
    private var dashboardHost: String?
    /// The sign-in link being opened, on 127.0.0.1; the daemon forwards it
    /// to the dashboard host.
    private var signInLink: URL?
    /// The daemon whose dashboard is loaded; nil while the waiting page shows.
    private var loadedBase: URL?
    /// The client of that daemon, for signing in again.
    private var api: APIClient?
    private var lastSignIn = Date.distantPast
    /// Sign-ins in a row that ended on the daemon's sign-in page again.
    private var signInFailures = 0
    private var signInRetry: DispatchWorkItem?
    var onClose: (() -> Void)?
    /// Asked to load the dashboard again after a failed load.
    var onRetry: (() -> Void)?
    /// A button on the status page: "start" or "edit-config".
    var onAction: ((String) -> Void)?

    init() {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .default()
        webView = WKWebView(frame: .zero, configuration: config)
        webView.isInspectable = true
        webView.allowsBackForwardNavigationGestures = false
        webView.setValue(false, forKey: "drawsBackground")

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1320, height: 860),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false)
        window.title = Brand.name
        window.titlebarAppearsTransparent = false
        window.minSize = NSSize(width: 900, height: 560)
        window.contentView = webView
        window.setFrameAutosaveName("Dashboard")
        if !window.setFrameUsingName("Dashboard") { window.center() }
        window.tabbingMode = .disallowed
        super.init(window: window)
        window.delegate = self
        webView.navigationDelegate = self
        webView.uiDelegate = self
    }

    required init?(coder: NSCoder) { fatalError() }

    /// Brings the window to the front.
    func present() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    /// Whether the window shows the status page rather than the dashboard.
    var showsStatus: Bool { loadedBase == nil }

    /// Loads the dashboard, optionally focused on one server. The first load
    /// goes through a one-time sign-in link so the page gets its session.
    func load(api: APIClient, server: String?) {
        if loadedBase == api.base, webView.url.map(isDashboard) == true {
            if let server {
                webView.evaluateJavaScript("location.hash = " + jsString("#/" + server))
            }
            return
        }
        Task { @MainActor in
            do {
                self.open(try await api.signInLink(next: server.map { "/#/" + $0 } ?? "/"), api: api)
            } catch {
                self.retryLater()
            }
        }
    }

    private func open(_ link: APIClient.SignInLink, api: APIClient) {
        cancelSignInRetry()
        self.api = api
        base = api.base
        dashboardHost = link.dashboardHost
        signInLink = link.url
        loadedBase = api.base
        webView.load(URLRequest(url: link.url))
    }

    /// After the daemon restarted: a cold start gives the dashboard a new
    /// host, so the window signs in there again, on the route it shows. A
    /// hidden window signs in when it opens next.
    func daemonRestarted(api: APIClient) {
        guard loadedBase == api.base, let dashboardHost else { return }
        guard window?.isVisible == true else {
            loadedBase = nil
            return
        }
        Task { @MainActor in
            guard let link = try? await api.signInLink(next: self.shownRoute()) else { return }
            if link.dashboardHost != dashboardHost { self.open(link, api: api) }
        }
    }

    /// The route the dashboard shows, as a sign-in link's `next`.
    private func shownRoute() -> String {
        guard let f = webView.url?.fragment, f.hasPrefix("/") else { return "/" }
        return "/#" + f
    }

    /// The daemon answered with its page on how to open the dashboard, as
    /// for a dashboard host from before a cold start: get a new link. When
    /// sign-ins keep ending there, the window says so and tries again after
    /// a wait that doubles from 20 seconds up to 5 minutes.
    private func signInAgain() {
        guard api != nil, signInRetry == nil else { return }
        let wait = signInFailures == 0 ? 0 : min(20 * pow(2, Double(signInFailures - 1)), 300)
        let left = wait - Date().timeIntervalSince(lastSignIn)
        guard left > 0 else { return signInNow() }
        webView.loadHTMLString(page(signInFailedBody(retryIn: left)), baseURL: nil)
        let retry = DispatchWorkItem { [weak self] in
            self?.signInRetry = nil
            self?.signInNow()
        }
        signInRetry = retry
        DispatchQueue.main.asyncAfter(deadline: .now() + left, execute: retry)
    }

    private func signInNow() {
        cancelSignInRetry()
        guard let api, loadedBase == api.base else { return }
        lastSignIn = Date()
        signInFailures += 1
        Task { @MainActor in
            guard let link = try? await api.signInLink(next: "/") else {
                self.signInAgain()
                return
            }
            self.open(link, api: api)
        }
    }

    private func cancelSignInRetry() {
        signInRetry?.cancel()
        signInRetry = nil
    }

    private func signInFailedBody(retryIn seconds: TimeInterval) -> String {
        let s = Int(seconds.rounded(.up))
        let when = s >= 60 ? L.t("\((s + 59) / 60) min", "\((s + 59) / 60) min") : L.t("\(s) s", "\(s) s")
        return "<p>\(L.t("The dashboard could not sign in.", "Nie udało się zalogować do panelu."))</p>"
            + "<p class=note>\(L.t("Trying again in \(when).", "Ponowna próba za \(when)."))</p>"
            + "<a class=button href=\"\(Self.actionScheme):\(Self.signInAction)\">\(L.t("Try Again", "Spróbuj ponownie"))</a>"
    }

    /// Shows what keeps the dashboard from loading, with the action that helps.
    func showStatus(_ status: Store.DaemonStatus) {
        cancelSignInRetry()
        loadedBase = nil
        webView.loadHTMLString(statusPage(status), baseURL: nil)
    }

    private func jsString(_ s: String) -> String {
        let data = try? JSONSerialization.data(withJSONObject: [s])
        let arr = data.flatMap { String(data: $0, encoding: .utf8) } ?? "[\"\"]"
        return String(arr.dropFirst().dropLast())
    }

    private func html(_ s: String) -> String {
        s.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;").replacingOccurrences(of: ">", with: "&gt;")
    }

    /// Buttons are links with the `app-action:` scheme, handled in the navigation policy.
    private func statusPage(_ status: Store.DaemonStatus) -> String {
        var body: String
        switch status {
        case .stopped:
            body = "<p>\(L.t("The daemon is stopped. Servers are not running.", "Demon jest zatrzymany. Serwery nie działają."))</p>"
                + "<a class=button href=\"\(Self.actionScheme):start\">\(L.t("Start", "Uruchom"))</a>"
        case .configError(let err):
            body = "<p>\(L.t("The config file has an error, so the daemon cannot start:", "Plik konfiguracji zawiera błąd, więc demon nie może wystartować:"))</p>"
                + "<pre>\(html(err))</pre>"
                + "<a class=button href=\"\(Self.actionScheme):edit-config\">\(L.t("Edit Configuration", "Edytuj konfigurację"))</a>"
        case .unreachable(let msg):
            body = "<p>\(L.t("Waiting for the daemon...", "Czekam na demona..."))</p><p class=note>\(html(msg))</p>"
        case .connecting, .connected:
            body = "<p>\(L.t("Waiting for the daemon...", "Czekam na demona..."))</p>"
        }
        return page(body)
    }

    private func page(_ body: String) -> String {
        """
        <html><head><meta name="color-scheme" content="light dark"><style>
        body{font:14px -apple-system;display:grid;place-items:center;height:100vh;margin:0;color:#888;background:transparent;text-align:center}
        main{max-width:560px;padding:24px}
        pre{white-space:pre-wrap;text-align:left;font:12px ui-monospace,monospace;color:CanvasText}
        .note{font-size:12px}
        .button{display:inline-block;margin-top:8px;padding:5px 16px;border-radius:6px;background:#0a84ff;color:#fff;text-decoration:none}
        </style></head><body><main>\(body)</main></body></html>
        """
    }

    static let actionScheme = "app-action"
    /// The window's own action: sign in again now.
    private static let signInAction = "sign-in"

    func windowWillClose(_ notification: Notification) { onClose?() }

    // Keep navigation inside the dashboard; everything else goes to the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { return decisionHandler(.allow) }
        if url.scheme == Self.actionScheme {
            let name = url.absoluteString.dropFirst(Self.actionScheme.count + 1).description
            if name == Self.signInAction { signInNow() } else { onAction?(name) }
            return decisionHandler(.cancel)
        }
        // The sign-in link itself is on 127.0.0.1; the daemon forwards it.
        if url.scheme == "about" || isDashboard(url) || (signInLink != nil && url == signInLink) {
            return decisionHandler(.allow)
        }
        NSWorkspace.shared.open(url)
        decisionHandler(.cancel)
    }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse, decisionHandler: @escaping @MainActor (WKNavigationResponsePolicy) -> Void) {
        if response.isForMainFrame, loadedBase != nil, let http = response.response as? HTTPURLResponse {
            if http.value(forHTTPHeaderField: RequestSigner.header("Sign-In")) != nil {
                signInAgain()
            } else if let url = http.url, isDashboard(url), http.statusCode < 400 {
                signInFailures = 0
            }
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url { NSWorkspace.shared.open(url) }
        return nil
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { loadFailed(error) }
    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { loadFailed(error) }

    private func loadFailed(_ error: Error) {
        // A navigation the policy cancelled (a link opened elsewhere, an app action) is no failure.
        let e = error as NSError
        if e.code == NSURLErrorCancelled || (e.domain == "WebKitErrorDomain" && e.code == 102) { return }
        retryLater()
    }

    private func retryLater() {
        showStatus(.connecting)
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            self?.onRetry?()
        }
    }

    private func isDashboard(_ url: URL) -> Bool {
        guard let base, let dashboardHost, url.port == base.port else { return false }
        return url.host == dashboardHost
    }
}
