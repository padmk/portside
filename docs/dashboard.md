# Dashboard and menu bar app

## Menu bar app (macOS)

The status icon shows whether everything runs, something is starting, or a server or one of its containers needs attention. Its panel lists every server with its project icon and start, stop, restart, open and logs, plus group actions; the dashboard opens in its own window.

A crash arrives as a notification with **Restart** and **Show Logs** once the restart policy gives up: one per crash, not one per attempt. A container that turns unhealthy or fails sends one too. More in [How it works](how-it-works.md#the-macos-app).

## Dashboard

The layout follows the number of servers: one server fills the screen with its output and charts, a few get large cards with a live log preview, and long lists switch to compact rows. `c` and `l` choose cards or the list yourself. Click a server for a full-size terminal, with every server one click away in a side rail. Group headers start, restart (in dependency order) and stop a whole group.

Everything else (ports, config, adding servers) is in the menu and the command palette: `⌘K` on macOS, Ctrl+Shift+Space elsewhere. `⌘,` (Ctrl+, elsewhere; a browser may keep it for its own settings) or `g` `s` opens Settings, and `?` lists the keyboard shortcuts.

While no server is configured, the dashboard shows its getting-started page: finding projects, adding a server by hand, [coding agents](coding-agents.md#setup) and the commands to scan from a terminal. In the macOS app's window it also has **Start Portside at login**, on until you have chosen once (here or in the menu) and applied when you find projects or add a server (or when you flip it); after that it shows the setting as it is; it is the app's **Launch at Login**, which brings your servers back after a restart. In a browser the page shows `portside service install` instead ([Login service](how-it-works.md#login-service)). **Settings** has the same switch under **Startup**, showing the setting as it is (in a browser, the same command).

Servers run in a PTY, so colors, progress bars and interactive prompts work. Type into a server's terminal in the dashboard, or attach from your own terminal with `portside attach web` and detach with `Ctrl-]`.

In the terminal view:

- `⌘F` (Ctrl+Shift+F elsewhere, so Ctrl-F and Ctrl-K stay with the shell) searches the terminal.
- **Filter lines** shows the matching lines from all the output the daemon keeps.
- **Clear output** clears only this view. **Clear daemon scrollback** in the server menu forgets the kept output; the log file stays, and **Open log file** shows it.

![The list layout with every server grouped by project](../design/screenshots/list.png)

CPU and memory are shown per server, summed over its whole process tree, with sparklines.

## Server states

- `starting` until the port accepts connections, the `health` path answers and the `ready_log` line appears, whichever are configured.
- `running`.
- **not responding** (an amber diamond; an orange light in the menu bar) when a running server has stopped answering on its port or `health` path for 4 seconds, with the reason. It is not restarted, and `portside check` reports it.
- `stopping` and `stopped`.
- `exited` when the process ended on its own with code 0.
- **done** for a [task](configuration.md#tasks) whose last run exited with code 0 (a hollow green light). A task is marked **task** and shows `starting` (**Running...**) while it runs. Group headers count servers only, not tasks.
- `crashed` with the exit code.
- `blocked` when another process holds the port.
- `external` for a stopped server whose port a program Portside did not start holds. The dashboard names that program, and you can take the port over in one click.

A server with a [schedule](configuration.md#schedules) carries a chip with its next run, such as **next 09:00** (**scheduled** while it is disabled); its tooltip shows the expression and the last scheduled run. In the detail pane, a server with [`watch`](configuration.md#restart-on-file-changes) is marked **watching**, with its patterns in the tooltip.

With a restart policy (`on-failure` or `always`, exponential backoff), a server waiting for its next automatic restart can be stopped like a running one.

A running server or container port that listens on the network, not only on loopback, is marked **May be reachable from the network**; click the mark for the fix ([Security](security.md#servers-reachable-from-the-network)).

## Ports overview

Every listening TCP port on the machine with its process and working directory, and the container a published port belongs to. Strays can be killed from here; [Security](security.md#killing-processes) says which processes may be.

## Settings and the server form

Everything in the [config file](configuration.md) can be set from the dashboard; **Edit config** in the menu stays for editing the file as text. Saves write only the keys you changed ([Editing and reloading](configuration.md#editing-and-reloading)) and leave a key out (or remove it) when you set it back to its default. Each field shows its default, or the value a server inherits, as its placeholder.

**Settings** has its sections on the left (at the top on a narrow window), each a few groups of rows with the label and a short explanation on the left and the control on the right:

- **General**: **Startup** (launch at login, see above, and bringing servers back after a restart, `restore`), **Projects** (the projects folder, `root`, and the editor and terminal the dashboard opens) and the language.
- **Appearance**: light, dark or system mode, the palette, the overview layout and, in the macOS app, the app's icon (also in the menu bar app's menu). Mode, palette, layout and language are kept in this browser.
- **Servers**: defaults for every server, which a server's own **Advanced** settings override: `stop_timeout`, `retry_after` and `memory_limit`, and the command **Start Docker** runs (`docker_start`).
- **Events**: desktop notifications (`notify`), `unready_after`, and the [hook commands](configuration.md#hook-commands) `on_crash`, `on_unready` and `on_recover`.
- **Environment**: `shell`, the login shell's environment (`login_env`), `umask`, the `PATH` folders (`path`) and environment variables for every server (`env`).
- **Coding agents**: [setup](coding-agents.md#setup) on this computer.
- **Advanced**: the dashboard's `port` (empty lets the daemon choose; a change applies after a daemon restart), **Computers** for sharing with your other devices, the config file (edit it here or open it in your editor) and the server logs folder.

The editor, terminal, theme, language and launch at login apply at once. The values from the config file are saved together with **Save** at the bottom, which says when there are unsaved changes; closing Settings or leaving for another window with unsaved changes asks first. An invalid value, such as `unready_after = "soon"`, is refused with the daemon's message and nothing is written. The footer shows the daemon's version.

A server's form (**Add** or **Edit** in its menu) has the common fields, **It's a task (runs once)** for a [task](configuration.md#tasks), which hides the port, `health` and readiness, and **Advanced** with the rest of the [server keys](configuration.md#server-keys): readiness (`ready_log`, `ready_after`), lifecycle and limits (`stop_timeout`, `retry_after`, `memory_limit`, `enabled`), `schedule` and `watch`, the hook commands with `unready_after` (empty inherits the top-level command, **None for this server** writes `""`), the tool environment (`wrap`), `umask` and `lan`, and `open`, `icon` and Compose `projects`. **Advanced** opens by itself when any of these is set.

## Themes

Rosé Pine (default), Everforest, Tokyo Night, Catppuccin, Nord and a neutral palette, each in light and dark, following the system setting or your choice. The terminal follows the theme.

![Settings: the General section with startup, the projects folder, editor, terminal and language](../design/screenshots/settings.png)
