import AppKit
import WebKit

/// The dashboard in a native window. Links that leave the dashboard open in
/// the default browser.
@MainActor
final class DashboardWindowController: NSWindowController, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    private let webView: WKWebView
    private var base: URL?
    /// The daemon whose dashboard is loaded; nil while the waiting page shows.
    private var loadedBase: URL?
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
                let url = try await api.signInURL(next: server.map { "/#/" + $0 } ?? "/")
                self.base = api.base
                self.loadedBase = api.base
                self.webView.load(URLRequest(url: url))
            } catch {
                self.retryLater()
            }
        }
    }

    /// Shows what keeps the dashboard from loading, with the action that helps.
    func showStatus(_ status: Store.DaemonStatus) {
        loadedBase = nil
        webView.loadHTMLString(statusPage(status), baseURL: nil)
    }

    func reload() { webView.reload() }

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
        return """
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

    func windowWillClose(_ notification: Notification) { onClose?() }

    // Keep navigation inside the dashboard; everything else goes to the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { return decisionHandler(.allow) }
        if url.scheme == Self.actionScheme {
            onAction?(url.absoluteString.dropFirst(Self.actionScheme.count + 1).description)
            return decisionHandler(.cancel)
        }
        if url.scheme == "about" || isDashboard(url) {
            return decisionHandler(.allow)
        }
        NSWorkspace.shared.open(url)
        decisionHandler(.cancel)
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
        guard let base, url.port == base.port else { return false }
        return url.host == "127.0.0.1" || url.host == "localhost"
    }
}
