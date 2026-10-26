# Dashboard and menu bar app

## Menu bar app (macOS)

The status icon shows at a glance whether everything runs, something is starting, or a server or one of its containers needs attention. Its panel lists every server with its project icon and start, stop, restart, open and logs, plus group actions; the dashboard opens in its own window. A crash arrives as a notification with **Restart** and **Show Logs** buttons once the restart policy gives up (one per crash, not one per attempt), and so does a container that starts needing attention. More in [How it works](how-it-works.md#the-macos-app).

## Dashboard

The dashboard scales with your setup. One server fills the screen with its output and charts, a few get large cards with a live log preview, and long lists switch to compact rows (`c` and `l` toggle them). Click a card for a full-size terminal, with every server one click away in a side rail. Group headers start, restart (in dependency order) and stop a whole group. Everything else (ports, config, add server) stays in the menu and the `⌘K` (Ctrl+Shift+Space on Linux and Windows) palette; `?` lists the keyboard shortcuts.

Servers run in a PTY, so colors, progress bars and interactive prompts work. Watch output live in the dashboard or attach from your terminal (`portside attach web`), type into it, detach with `Ctrl-]`. `⌘F` (Ctrl+Shift+F on Linux, so Ctrl-F and Ctrl-K stay with the shell) searches the terminal, and **Filter lines** shows the matching lines from all the output the daemon keeps; **Clear output** clears only that view, while **Clear daemon scrollback** in the server menu forgets the kept output (the log file stays, and **Open log file** shows it).

![The list layout with every server grouped by project](../design/screenshots/list.png)

CPU and memory are shown per server, summed over the whole process tree, with sparklines.

## Server states

- `starting` until the port accepts connections (optionally an HTTP health path);
- `running`, with a delayed warning if a running server loses its listener;
- `crashed` with the exit code;
- `blocked` when another process holds the port.

A running server or container port that listens on the network, not only on loopback, is marked **Reachable from network**; click the mark for the fix ([Security](security.md#servers-reachable-from-the-network)).

Ports held by programs Portside did not start are shown with their owner, and you can take the port over in one click. With a restart policy (`on-failure` or `always`, exponential backoff), a server waiting for its next automatic restart can be stopped like a running one.

## Ports overview

Every listening TCP port on the machine with its process and working directory, the container a published port belongs to, and a safe way to kill strays.

## Themes

Rosé Pine (default), Everforest, Tokyo Night, Catppuccin, Nord and a neutral palette, each in light and dark, following the system setting or your choice. The terminal follows the theme. **Settings** also choose the layout, the language and the editor and terminal the dashboard opens.

![Settings: appearance, palette, layout, language, and the editor and terminal the dashboard opens](../design/screenshots/settings.png)
