import AppKit
import SwiftUI

/// Strings follow the system language: Polish when it comes first, else English.
enum L {
    static let polish = Locale.preferredLanguages.first?.hasPrefix("pl") ?? false
    static func t(_ en: String, _ pl: String) -> String { polish ? pl : en }

    /// Polish renderings of the daemon's status notes, which arrive in English
    /// (mirrors web/src/i18n/notes.ts).
    static func note(_ note: String) -> String {
        guard polish else { return note }
        var base = note
        var suffix = ""
        let suffixes: [(String, ([String]) -> String)] = [
            (#", restarting in (\S+)$"#, { ", restart za \($0[0])" }),
            (#"; gave up after (\d+) restarts$"#, { "; bez kolejnych prób (restarty: \($0[0]))" }),
        ]
        for (pattern, render) in suffixes {
            if let (range, groups) = match(pattern, in: base) {
                suffix = render(groups) + suffix
                base.removeSubrange(range)
            }
        }
        let rules: [(String, ([String]) -> String)] = [
            (#"^exited with code (-?\d+)$"#, { "zakończył się z kodem \($0[0])" }),
            (#"^exited on its own$"#, { _ in "zakończył się sam" }),
            (#"^waiting for (.+)$"#, { "czeka na \($0[0])" }),
            (#"^port (\d+) is used by (.+) \(pid (\d+)\)$"#, { "port \($0[0]) zajmuje \($0[1]) (pid \($0[2]))" }),
            (#"^port (\d+) is already in use$"#, { "port \($0[0]) jest już zajęty" }),
            (#"^directory not found: (.+)$"#, { "brak katalogu: \($0[0])" }),
            (#"^failed to start: (.+)$"#, { "nie udało się uruchomić: \($0[0])" }),
            (#"^dependency (\S+) failed: (.+)$"#, { "zależność \($0[0]) nie wystartowała: \($0[1])" }),
            (#"^dependency (\S+) did not become ready: (.+)$"#, { "zależność \($0[0]) nie była gotowa: \($0[1])" }),
        ]
        for (pattern, render) in rules {
            if let (_, groups) = match(pattern, in: base) { return render(groups) + suffix }
        }
        return note
    }

    /// The range of the first match and its capture groups.
    private static func match(_ pattern: String, in text: String) -> (Range<String.Index>, [String])? {
        guard let re = try? NSRegularExpression(pattern: pattern),
              let m = re.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
              let whole = Range(m.range, in: text) else { return nil }
        let groups = (1..<m.numberOfRanges).compactMap { Range(m.range(at: $0), in: text).map { String(text[$0]) } }
        return (whole, groups)
    }

    /// Polish plural category: one (1), few (2-4, not 12-14), many (the rest).
    private static func plural(_ n: Int, one: String, few: String, many: String) -> String {
        let mod10 = n % 10, mod100 = n % 100
        if n == 1 { return one }
        if (2...4).contains(mod10) && !(12...14).contains(mod100) { return few }
        return many
    }

    /// "1 awaria", "3 awarie", "5 awarii".
    static func crashedCount(_ n: Int) -> String {
        guard polish else { return "\(n) crashed" }
        return "\(n) " + plural(n, one: "awaria", few: "awarie", many: "awarii")
    }

    /// "1 z problemem w kontenerach", "3 z problemami w kontenerach".
    static func containerIssueCount(_ n: Int) -> String {
        guard polish else { return "\(n) with container problems" }
        return "\(n) " + plural(n, one: "z problemem w kontenerach", few: "z problemami w kontenerach", many: "z problemami w kontenerach")
    }

    /// "1 startuje", "3 startują", "5 startuje".
    static func startingCount(_ n: Int) -> String {
        guard polish else { return "\(n) starting" }
        return "\(n) " + plural(n, one: "startuje", few: "startują", many: "startuje")
    }

    static func status(_ s: String) -> String {
        switch s {
        case "running": return t("running", "działa")
        case "starting": return t("starting", "startuje")
        case "stopping": return t("stopping", "zatrzymuje się")
        case "crashed": return t("crashed", "awaria")
        case "blocked": return t("blocked", "zablokowany")
        case "exited": return t("exited", "zakończony")
        case "external": return t("external", "zewnętrzny")
        default: return t("stopped", "zatrzymany")
        }
    }
}

enum Brand {
    static var name: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String ?? "Portside" }
}

struct Lamp: View {
    let status: String
    @State private var pulse = false

    var body: some View {
        let size: CGFloat = 9
        Group {
            switch status {
            case "running":
                Circle().fill(Color.green)
            case "starting", "stopping":
                Circle().fill(Color.orange)
                    .opacity(pulse ? 0.45 : 1)
                    .onAppear {
                        withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) { pulse = true }
                    }
            case "crashed", "blocked":
                Circle().fill(Color.red)
            case "external":
                Circle().strokeBorder(Color.blue, lineWidth: 1.6)
            default:
                Circle().strokeBorder(Color.secondary.opacity(0.7), lineWidth: 1.4)
            }
        }
        .frame(width: size, height: size)
        .accessibilityLabel(L.status(status))
    }
}

struct MenuPanel: View {
    @Bindable var store: Store
    let actions: AppActions
    @State private var hovered: String?
    @State private var launchAtLogin = LoginItem.isEnabled
    @State private var iconStyle = AppIconStyle.current

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            banners
            content
            Divider()
            footer
        }
        .frame(width: 360)
        // An opaque background keeps text readable over any wallpaper or window.
        .background(Color(nsColor: .windowBackgroundColor))
        .onAppear { launchAtLogin = LoginItem.isEnabled }
    }

    // MARK: header

    private var header: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 30, height: 30)
                .id(iconStyle)
            VStack(alignment: .leading, spacing: 1) {
                Text(Brand.name).font(.system(size: 13, weight: .semibold))
                Text(summaryText).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            settingsMenu
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    private var summaryText: String {
        let s = store.summary
        switch store.connection {
        case .connecting: return L.t("Connecting...", "Łączenie...")
        case .offline:
            if store.daemonStopped { return L.t("Daemon stopped", "Demon zatrzymany") }
            if store.configError != nil { return L.t("Config error", "Błąd konfiguracji") }
            return L.t("Daemon not reachable", "Brak połączenia z demonem")
        case .connected: break
        }
        if s.total == 0 { return L.t("No servers yet", "Brak serwerów") }
        var parts = [L.t("\(s.running) of \(s.total) running", "\(s.running) z \(s.total) działa")]
        if s.crashed > 0 { parts.append(L.crashedCount(s.crashed)) }
        if s.containerIssues > 0 { parts.append(L.containerIssueCount(s.containerIssues)) }
        if s.starting > 0 { parts.append(L.startingCount(s.starting)) }
        return parts.joined(separator: " · ")
    }

    private var settingsMenu: some View {
        Menu {
            Button(L.t("Open Dashboard", "Otwórz panel")) { actions.openDashboard(nil) }
            Divider()
            Button(L.t("Start All", "Uruchom wszystkie")) { store.bulk("start") }
                .disabled(store.connection != .connected)
            Button(L.t("Stop All", "Zatrzymaj wszystkie")) { store.bulk("stop") }
                .disabled(store.connection != .connected || store.summary.running + store.summary.starting == 0)
            Divider()
            Button(L.t("Edit Configuration", "Edytuj konfigurację")) { store.open("config") }
            Button(L.t("Show Logs Folder", "Pokaż folder logów")) { store.open("logs") }
            Divider()
            Menu(L.t("Settings", "Ustawienia")) {
                Toggle(L.t("Launch at Login", "Uruchamiaj przy logowaniu"), isOn: Binding(
                    get: { launchAtLogin },
                    set: { on in
                        LoginItem.set(on) { err in store.lastError = err }
                        launchAtLogin = LoginItem.isEnabled
                    }))
                Picker(L.t("App Icon", "Ikona aplikacji"), selection: Binding(
                    get: { iconStyle },
                    set: { style in
                        AppIconStyle.current = style
                        iconStyle = style
                    })) {
                    ForEach(AppIconStyle.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                Button(L.t("Install Command Line Tool...", "Zainstaluj narzędzie wiersza poleceń...")) { actions.installCLI() }
            }
            Divider()
            if store.daemonStopped {
                Button(L.t("Start Daemon", "Uruchom demona")) { store.startDaemon() }
            } else {
                Button(L.t("Restart Daemon", "Uruchom ponownie demona")) { store.restartDaemon() }
                Button(L.t("Stop Daemon and Servers", "Zatrzymaj demona i serwery")) { store.stopDaemon() }
            }
            Divider()
            Button(L.t("About \(Brand.name)", "O aplikacji \(Brand.name)")) { actions.about() }
            Button(L.t("Quit \(Brand.name)", "Zakończ \(Brand.name)")) { NSApp.terminate(nil) }
        } label: {
            Image(systemName: "ellipsis.circle").font(.system(size: 16)).foregroundStyle(.secondary)
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .focusEffectDisabled()
        .fixedSize()
        .help(L.t("More", "Więcej"))
    }

    // MARK: banners

    @ViewBuilder private var banners: some View {
        if case .offline(let msg) = store.connection, !store.daemonStopped, store.configError == nil {
            banner(icon: "bolt.horizontal.circle", text: L.t("Starting the daemon... ", "Uruchamianie demona... ") + msg, tint: .orange)
        }
        if let remote = store.remote {
            banner(icon: "desktopcomputer", text: L.t("Dashboard shows \(remote.peer.name)", "Panel pokazuje \(remote.peer.name)"), tint: .purple,
                   button: (L.t("Disconnect", "Rozłącz"), { Task { _ = await store.disconnectRemote() } }))
        }
        if store.daemonStopped {
            banner(icon: "pause.circle", text: L.t("The daemon is stopped. Servers are not running.", "Demon jest zatrzymany. Serwery nie działają."), tint: .secondary,
                   button: (L.t("Start", "Uruchom"), { store.startDaemon() }))
        }
        if let err = store.configError {
            banner(icon: "exclamationmark.triangle", text: L.t("Config error: ", "Błąd konfiguracji: ") + err, tint: .red,
                   button: (L.t("Edit", "Edytuj"), { store.open("config") }))
        }
        if store.connection == .connected, store.versionDiffers, let running = store.daemonVersion {
            banner(icon: "arrow.triangle.2.circlepath",
                   text: L.t("The daemon runs version \(running), which differs from this app.", "Demon działa w wersji \(running), innej niż ta aplikacja."), tint: .blue,
                   button: (L.t("Restart", "Uruchom ponownie"), { store.restartDaemon() }))
        }
        if let err = store.lastError {
            banner(icon: "xmark.octagon", text: err, tint: .red, button: (L.t("Dismiss", "Zamknij"), { store.lastError = nil }))
        }
    }

    private func banner(icon: String, text: String, tint: Color, button: (String, () -> Void)? = nil) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: icon).foregroundStyle(tint)
            Text(text).font(.system(size: 11.5)).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 4)
            if let button {
                Button(button.0, action: button.1).controlSize(.small)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        .background(tint.opacity(0.08))
    }

    // MARK: server list

    @ViewBuilder private var content: some View {
        if store.connection == .connected && store.servers.isEmpty {
            VStack(spacing: 8) {
                Text(L.t("No servers yet", "Brak serwerów")).font(.system(size: 13, weight: .medium))
                Text(L.t("Add projects in the dashboard, or run `portside scan --add`.",
                         "Dodaj projekty w panelu albo uruchom `portside scan --add`."))
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).multilineTextAlignment(.center)
                Button(L.t("Open Dashboard", "Otwórz panel")) { actions.openDashboard(nil) }.controlSize(.small)
            }
            .frame(maxWidth: .infinity)
            .padding(24)
        } else if !store.servers.isEmpty {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(store.sections, id: \.id) { section in
                        // Ungrouped servers get a header only when groups exist above them.
                        if section.group != nil || store.sections.count > 1 {
                            sectionHeader(section.group, section.servers)
                        }
                        ForEach(section.servers) { server in
                            row(server)
                        }
                    }
                }
                .padding(.vertical, 6)
            }
            .frame(height: listHeight)
            // Without a daemon the last known rows stay for orientation, without actions.
            .disabled(store.connection != .connected)
            .opacity(store.connection == .connected ? 1 : 0.45)
        }
    }

    /// Fits the list to its rows up to a cap; a ScrollView in a popover has no
    /// natural height of its own.
    private var listHeight: CGFloat {
        let sections = store.sections
        let headers = sections.filter { $0.group != nil || sections.count > 1 }.count
        let rows = store.servers.count
        return min(CGFloat(rows) * 38 + CGFloat(headers) * 29 + 12, 520)
    }

    private func sectionHeader(_ group: String?, _ servers: [Server]) -> some View {
        let running = servers.filter { $0.status == "running" }.count
        let title = group ?? L.t("Other", "Pozostałe")
        return HStack(spacing: 6) {
            Text(title.uppercased()).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(.secondary)
            Text(verbatim: "\(running)/\(servers.count)").font(.system(size: 10.5).monospacedDigit()).foregroundStyle(.tertiary)
            Spacer()
            if let group {
                Button {
                    store.bulk(running > 0 ? "stop" : "start", names: [group])
                } label: {
                    Image(systemName: running > 0 ? "stop.fill" : "play.fill").font(.system(size: 9))
                }
                .buttonStyle(.borderless)
                .help(running > 0 ? L.t("Stop group", "Zatrzymaj grupę") : L.t("Start group", "Uruchom grupę"))
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 8)
        .padding(.bottom, 3)
    }

    private func row(_ v: Server) -> some View {
        let status = v.displayStatus
        let isHovered = hovered == v.name && store.connection == .connected
        let issues = store.containerIssues[v.name] ?? []
        return HStack(spacing: 9) {
            Lamp(status: status)
            VStack(alignment: .leading, spacing: 1) {
                Text(v.name).font(.system(size: 13, weight: .medium)).lineLimit(1)
                Text(issues.isEmpty ? detail(v) : issues.map { "\($0.shortName) \($0.problem)" }.joined(separator: ", "))
                    .font(.system(size: 10.5))
                    .foregroundStyle(status == "crashed" || !issues.isEmpty ? Color.red : Color.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 6)
            if isHovered {
                rowButtons(v)
            } else if v.port > 0 {
                Text(verbatim: ":\(v.port)").font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 5)
        .frame(height: 38)
        .background(isHovered ? Color.primary.opacity(0.07) : Color.clear)
        .contentShape(Rectangle())
        .onHover { hovered = $0 ? v.name : (hovered == v.name ? nil : hovered) }
        .onTapGesture { if store.connection == .connected { actions.openDashboard(v.name) } }
        .contextMenu { contextMenu(v) }
        .help(v.dirDisplay)
    }

    private func detail(_ v: Server) -> String {
        if let from = store.forwardedFrom(v) {
            return L.t("port forwarded from \(from)", "port przekierowany z \(from)")
        }
        switch v.displayStatus {
        case "external":
            if let cmd = v.external?.command { return L.t("port held by \(cmd)", "port zajmuje \(cmd)") }
            return L.status("external")
        case "crashed", "blocked":
            return v.note.map(L.note) ?? L.status(v.status)
        case "running":
            var parts = [L.status("running")]
            if v.mem > 0 { parts.append(String(format: "%.0f MB", Double(v.mem) / 1_048_576)) }
            if v.stale == true { parts.append(L.t("restart to apply config", "zmieniona konfiguracja")) }
            return parts.joined(separator: " · ")
        default:
            return L.status(v.displayStatus)
        }
    }

    @ViewBuilder private func rowButtons(_ v: Server) -> some View {
        HStack(spacing: 2) {
            if v.status == "running", let url = v.url {
                iconButton("arrow.up.right.square", L.t("Open in browser", "Otwórz w przeglądarce")) { actions.openURL(url) }
            }
            if v.isAlive {
                iconButton("arrow.clockwise", L.t("Restart", "Uruchom ponownie")) { store.perform("restart", v.name) }
                iconButton("stop.fill", L.t("Stop", "Zatrzymaj")) { store.perform("stop", v.name) }
            } else if v.external != nil, store.forwardedFrom(v) == nil {
                iconButton("arrow.right.circle", L.t("Stop the other process and start", "Zatrzymaj proces zajmujący port i uruchom")) {
                    store.perform("start", v.name, takeover: true)
                }
            } else {
                iconButton("play.fill", L.t("Start", "Uruchom")) { store.perform("start", v.name) }
            }
            iconButton("terminal", L.t("Show logs", "Pokaż logi")) { actions.openLogs(v.name) }
        }
    }

    private func iconButton(_ symbol: String, _ help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: 11.5)).frame(width: 24, height: 22)
        }
        .buttonStyle(.borderless)
        .help(help)
        .accessibilityLabel(help)
    }

    @ViewBuilder private func contextMenu(_ v: Server) -> some View {
        if let url = v.url {
            Button(L.t("Open \(url.replacingOccurrences(of: "http://", with: ""))", "Otwórz \(url.replacingOccurrences(of: "http://", with: ""))")) { actions.openURL(url) }
        }
        if let alias = v.alias {
            Button(L.t("Open \(alias.replacingOccurrences(of: "http://", with: ""))", "Otwórz \(alias.replacingOccurrences(of: "http://", with: ""))")) { actions.openURL(alias) }
        }
        if let url = v.url {
            Button(L.t("Copy URL", "Kopiuj adres")) { actions.copy(url) }
        }
        Divider()
        if v.isAlive {
            Button(L.t("Restart", "Uruchom ponownie")) { store.perform("restart", v.name) }
            Button(L.t("Stop", "Zatrzymaj")) { store.perform("stop", v.name) }
        } else {
            Button(L.t("Start", "Uruchom")) { store.perform("start", v.name) }
            // The daemon frees a forwarded port itself; only a foreign holder needs a takeover.
            if v.external != nil, store.forwardedFrom(v) == nil {
                Button(L.t("Stop the Other Process and Start", "Zatrzymaj proces zajmujący port i uruchom")) { store.perform("start", v.name, takeover: true) }
            }
        }
        Divider()
        Button(L.t("Show Logs", "Pokaż logi")) { actions.openLogs(v.name) }
        Button(L.t("Open Folder", "Otwórz folder")) { store.open("folder", name: v.name) }
        Button(L.t("Open in Editor", "Otwórz w edytorze")) { store.open("editor", name: v.name) }
        Button(L.t("Open in Terminal", "Otwórz w terminalu")) { store.open("terminal", name: v.name) }
    }

    // MARK: footer

    private var footer: some View {
        HStack {
            Button {
                actions.openDashboard(nil)
            } label: {
                Label(L.t("Open Dashboard", "Otwórz panel"), systemImage: "macwindow")
            }
            .buttonStyle(.borderless)
            .keyboardShortcut("d", modifiers: .command)
            Spacer()
            Text(totals).font(.system(size: 10.5).monospacedDigit()).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }

    private var totals: String {
        let cpu = store.servers.reduce(0) { $0 + $1.cpu }
        let mem = store.servers.reduce(Int64(0)) { $0 + $1.mem }
        guard store.connection == .connected, mem > 0 else { return "" }
        let memText = mem >= 1 << 30 ? String(format: "%.1f GB", Double(mem) / 1_073_741_824) : String(format: "%.0f MB", Double(mem) / 1_048_576)
        return String(format: "CPU %.0f%% · ", cpu) + memText
    }
}
