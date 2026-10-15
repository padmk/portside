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

    /// Shows the dashboard, optionally focused on one server. The first load
    /// goes through a one-time sign-in link so the page gets its session.
    func show(api: APIClient?, server: String?) {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
        guard let api else {
            showPlaceholder()
            return
        }
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
                self.showPlaceholder()
            }
        }
    }

    private func showPlaceholder() {
        loadedBase = nil
        webView.loadHTMLString(offlinePage, baseURL: nil)
    }

    func reload() { webView.reload() }

    private func jsString(_ s: String) -> String {
        let data = try? JSONSerialization.data(withJSONObject: [s])
        let arr = data.flatMap { String(data: $0, encoding: .utf8) } ?? "[\"\"]"
        return String(arr.dropFirst().dropLast())
    }

    private var offlinePage: String {
        """
        <html><body style="font:14px -apple-system;display:grid;place-items:center;height:100vh;margin:0;color:#888;background:transparent">
        <p>\(L.t("Waiting for the daemon...", "Czekam na demona..."))</p></body></html>
        """
    }

    func windowWillClose(_ notification: Notification) { onClose?() }

    // Keep navigation inside the dashboard; everything else goes to the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { return decisionHandler(.allow) }
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

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { retryLater() }
    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { retryLater() }

    private func retryLater() {
        showPlaceholder()
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            self?.onRetry?()
        }
    }

    private func isDashboard(_ url: URL) -> Bool {
        guard let base, url.port == base.port else { return false }
        return url.host == "127.0.0.1" || url.host == "localhost"
    }
}
