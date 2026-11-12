# Terminal UI

`portside tui` shows your servers, their logs, the Docker containers and the listening ports full screen in the terminal: what runs, what broke and on which port, with every action a key away. It is a client of the same daemon as the dashboard and the menu bar app, so all three show the same state. Use it on Linux, over SSH, or whenever you would rather stay in the terminal than open a browser.

## Start

```sh
portside tui                      # this computer
portside --on mbp tui             # the connected computer
portside tui --theme nord-light   # a theme and variant for this run, not saved
portside tui --icons nerd         # Nerd Font icons
```

- `--theme <theme>` picks a theme for this run: `terminal`, `rose-pine`, `everforest`, `tokyo-night`, `catppuccin`, `nord` or `neutral`, each also with `-light` or `-dark` to force the variant ([Themes and icons](#themes-and-icons)).
- `--icons <set>` picks `unicode`, `nerd` or `ascii`.
- `--on <computer>` (before `tui`) opens the session on the connected computer ([Other computers](#other-computers)).

It needs an interactive terminal on standard input and output; piped, it says so and points to `portside status` and `portside logs`. At 80x24 or more it shows the list and the main panel side by side; in a smaller terminal it shows one pane at a time and `esc` goes back. `NO_COLOR` turns colors off; states keep their glyphs. `q` quits; Bubble Tea then leaves the alternate screen and turns the mouse and the cursor back to how they were.

## The screen

- **Top bar:** the daemon's port (click it for the daemon's details), the servers' count per state, after a muted `servers` when it fits (click one to show only the servers in that state, again to show all), the computer the session is on and the daemon version. While the daemon cannot be reached it keeps the last state on screen, greyed, says when the next attempt is, and `R` retries at once; during an upgrade or restart of the daemon it says `restarting, reconnecting`. A config error or warning shows as a banner under it.
- **Left column:** Servers always; Containers while Docker reports containers (or while the engine is not running and a server runs containers); Ports after `:ports` or `3`. The focused list takes the column's height and the others shrink to one header row with their key, title and count (`─ 2 Containers ──── 3 ─`); a click on a header or its key opens it.
- **Main panel** (right): the selected item's tabs. A server has **Logs**, **Details**, **Env** and **Metrics**; a container has **Logs**, **Details**, **Shell** and, while it has one, **Job** with a compose job's output. The top border holds the state glyph and name, the tabs and, when there is room, the state and uptime; narrow frames first narrow the space between the tabs, then abbreviate them (`Log Det Env Met`) or show only the open one between arrows. The bottom border shows the reading state of the logs, or the position (`3/12`) on a Details or Env tab that does not fit.
- **Footer:** the keys for the focused pane that act on the row under the cursor, with `x menu` and `? keys` always there: a crashed server is not offered `a attach` or `o open`, and `e edit` and `↵ logs` take their place. On a port that belongs to a server or container, `↵` says which one it selects. While you type a filter, a search or a command, select lines or edit a form field, the mode shows first (`FILTER`, `SEARCH`, `COMMAND`, `SELECT`, `INSERT`). Messages replace the hints for a few seconds; `:messages` keeps them.
- **Toasts** at the top right announce crashes, containers that became unhealthy, crashed or were killed for memory, and a lost connection; click one to select its server or container.

It opens on the first server that crashed, stopped answering or exited with an error, else the first running one.

Actions take the row the focused list's cursor is on, from any pane: on a group or project header they act on the group or project, and with a filter that matches nothing they say `nothing selected`. The main panel then keeps showing the last server, marked `(preview)` after its name, as it does while the Ports pane's cursor is on a port.

### Servers

Servers are grouped as in the dashboard; `other` holds the ones outside any group. Each row shows the state, the port, a CPU sparkline and memory while it runs, otherwise how it ended (`exit 1 · 2 min ago`, `done 5 min ago`), and badges for an unanswering server, a changed config, disabled servers, network exposure, tasks, schedules, watched files and temporary copies. A narrow list keeps the state, the mark and the port, and drops the sparkline, memory and badges first. A group header counts its crashed servers first (`◆ 1`), then its running services and finished tasks apart (`◆ 1 · 2/3 up · ✓ 1/2`; `2/3` without `up` when narrow). The counts in the top bar are the servers' (`servers ● 2 ◆ 1`). The sparkline is scaled to at least 5 percent of a core, so an idle server draws a low line. A server's logs with no output yet say when it is stopped and how to start it. When the list does not fit, its bottom border shows the cursor's position (`4/25`).

State is shown by shape as well as color: `●` running, `◐` starting or stopping, `◍` not answering, `◆` crashed, `○` stopped, `✓` done, `◎` port held by another process; next to it, `⊘` disabled and `↻` a config changed since the server started (restart to apply it). The selected row is marked with `▎`.

### Containers

Rows are grouped by Compose project: the header has the owner server as a badge, how many containers run and, while a compose job runs, a spinner and what it does; the containers outside any project come last under `other containers`. Each row shows the state (`●` running, `◆` unhealthy or crashed, `◐` starting, `○` stopped), the service, what is wrong (`unhealthy`, `crashed (137)`, `OOM` for a container killed for memory), the published ports and, outside its project's header, the server that owns it. While the engine is not running the pane says so, with the engine's error and `:start-docker`. A list that does not fit shows the cursor's position in its bottom border.

### Ports

One row per process listening on a TCP port, sorted by port: the port, the process and its PID, what it is (`server web`, `container db (shop)`, `blocks web` for a configured server's port that another process holds, `unmanaged`) and where it can be reached from (`local`, `all interfaces` or the addresses). The list is read again every 3 seconds while the pane is open, keeping the cursor on its row, and not at all once it is closed. The bottom border shows the filter, `refresh failed` and, when the list does not fit, the cursor's position.

## Keys

The keys are the ones lazygit and lazydocker use for the same things. `?` lists every key: the essentials first (moving, panes, tabs, open, back, the menu, the palette, the command line, quit), then the focused pane's, with which lines show (`lines 1-31 of 85`) when they do not all fit; `x` shows the focused pane's actions as a menu; `ctrl+p` finds any action or server by typing. Every action is also a command: the Command column is what the [command line](#command-line) takes, with its argument in brackets when it is optional (the cursor's item is used without it) and in angle brackets when it is required.

### Everywhere

| Keys | Action | Command |
| --- | --- | --- |
| `h` `←`, `l` `→` | Pane on the left or right | `:focus-left`, `:focus-right` |
| `tab`, `shift+tab` | Next or previous pane | `:focus-next`, `:focus-prev` |
| `1`, `2`, `3`, `0` | Servers; Containers (when they show); Ports (opening the pane); the main panel | `:focus-servers`, `:focus-containers`, `:focus-ports`, `:focus-main` |
| `]`, `[` | Next or previous tab of the main panel | `:tab-next`, `:tab-prev` |
| `+`, `_` | Next or previous screen mode: normal, half, full | `:screen-next`, `:screen-prev` |
| `J`, `K` | Scroll the main panel down or up, from any pane | `:scroll-down`, `:scroll-up` |
| `ctrl+d`, `ctrl+u` | Half a page down or up in the main panel | `:half-down`, `:half-up` |
| `PgDn`, `PgUp` | A page down or up in the main panel | `:page-down`, `:page-up` |
| `x` | Menu of the focused pane's actions | `:menu` |
| `?` | All keys | `:help` |
| `:` | Command line | `:command` |
| `ctrl+p` | Palette: find an action, a server, a theme | `:palette` |
| `T` | Next theme, or the theme named | `:theme [theme]` |
| | Next icon set, or the set named | `:icons [icons]` |
| `R` | Reconnect to the daemon now | `:reconnect` |
| | The notices shown so far, with connection errors | `:messages` |
| | Show or hide the Ports pane | `:ports` |
| | Start the container engine, when the daemon knows how | `:start-docker` |
| | Show only the servers in a state; `all` shows every server | `:filter-state <state>` |
| | Edit the config file in your editor | `:config` |
| | Settings | `:settings` |
| | Scan a folder for projects (default: the projects folder) | `:scan [folder]` |
| `C` | Other computers | `:computers` |
| | Switch the session to another computer; without a name or with `local`, back to this one | `:on [computer]` |
| | Pair with another computer (asks for its code) | `:pair [computer]` |
| | Daemon details | `:daemon` |
| | Upgrade the daemon in place (asks first) | `:upgrade` |
| | Restart the daemon in place (asks first) | `:restart-daemon` |
| | Set up coding agents | `:setup` |
| | Check the setup with `portside doctor` | `:doctor` |
| `q`, `ctrl+c` | Quit | `:quit`, `:q` |

In the half screen mode the server list takes half the width, or the main panel all of it when it is focused; the full mode shows only the focused pane.

### Moving in lists and panels

These work in every list and on every tab of the main panel.

| Keys | Action | Command |
| --- | --- | --- |
| `j` `↓`, `k` `↑` | Down, up | `:down`, `:up` |
| `.`, `,` | Next or previous page | `:next-page`, `:prev-page` |
| `<` `Home`, `>` `End` | Top or bottom (in logs, the bottom follows new output again) | `:top`, `:bottom` |

### Servers

| Keys | Action | Command |
| --- | --- | --- |
| `space` | Start or stop the server; on a group header, the group | `:toggle [server or group]` |
| `S`, `s`, `r` | Start, stop or restart it; on a group header, the group | `:start [server or group]`, `:stop [server or group]`, `:restart [server or group]` |
| `a` | Attach the terminal to the server's process; `ctrl+]` detaches | `:attach [server]` |
| `o`, `y` | Open its URL in the browser, or copy it | `:open [server]`, `:copy-url [server]` |
| `e` | Edit its config entry in a form | `:edit [server]` |
| `E` | Your shell (`$SHELL`) in its folder, in this terminal; `exit` comes back | `:shell [server]` |
| | Open its folder in your editor app | `:editor [server]` |
| | Open a terminal app in its folder | `:terminal [server]` |
| | Start, stop or restart its group, or the group named | `:start-group [group]`, `:stop-group [group]`, `:restart-group [group]` |
| | Enable or disable it for `--all` and `restore` | `:toggle-enabled [server]` |
| | Start it, taking over the port another process holds | `:takeover [server]` |
| `c` | Clear the daemon's scrollback (asks first; the log file stays) | `:clear-logs [server]` |
| `d` | Remove it from the config (asks first) | `:delete [server]` |
| `enter` | Show its logs, or fold or unfold a group | `:open-detail` |
| `-`, `=` | Fold or unfold all groups | `:collapse-all`, `:expand-all` |
| `/` | Filter by name, group, folder, port or state; `enter` keeps it | `:filter` |
| `esc` | Clear the filter and a state filter | `:clear-filter` |
| `n` | Add a server: the scan's proposals or an empty form | `:new` |

A group's start takes its enabled servers that are not running, and its stop and restart the servers that run or wait for an automatic restart, as in the dashboard; the menu and the footer say how many (`Restart group (2)`). A server with a start, stop or restart still in progress refuses another until it returns. The keyless actions are in the `x` menu, the command line and the palette.

### Containers

| Keys | Action | Command |
| --- | --- | --- |
| `space` | Start or stop the container; on a project header, the project | `:toggle [container]` |
| `S`, `s`, `r` | Start, stop or restart it (`S` and `s` on a project header start or stop all its containers) | `:start [container]`, `:stop [container]`, `:restart [container]` |
| `a` | A shell in the running container (`bash` when the image has it, else `sh`); `ctrl+]` comes back | `:shell [container]` |
| `o`, `y` | Open its first published port in the browser, or copy the address | `:open [container]`, `:copy-url [container]` |
| | Recreate the service (`compose up --force-recreate`) | `:recreate [container]` |
| | Rebuild its image, then recreate it (for a service with a known build) | `:rebuild [container]` |
| | Pull its image, then recreate it | `:pull [container]` |
| | Start or stop all of the project's containers | `:start-project [project]`, `:stop-project [project]` |
| `d` | Remove the project's containers (`compose down`, asks first; volumes are kept) | `:down-project [project]` |
| | Dismiss the finished compose job | `:dismiss-job [container]` |
| `enter` | Show its logs, or fold or unfold the project | `:open-detail` |

The commands that servers and containers share take either name: `:restart shop-db-1` restarts the container, `:restart web` the server, and without a name they act on the focused pane's row. The project commands take a project's name (`:stop-project shop`).

### Ports

| Keys | Action | Command |
| --- | --- | --- |
| `d` | Kill the process on the port (asks first, naming the process, its PID and the port); on a managed server's port it asks to stop the server instead | `:kill [port or PID]` |
| `t` | Start the server the port blocks, taking the port over from the process that holds it | `:takeover` |
| `o`, `y` | Open `http://127.0.0.1:<port>` in the browser, or copy it (on another computer only a forwarded port, or a server's own port through the proxy, has an address here) | `:open`, `:copy-url` |
| `enter` | Select the server or container the port belongs to | `:open-detail` |
| `/` | Filter by port, process, PID or what it is | `:filter` |
| `esc` | Clear the filter, then close the pane | `:back` |

### Logs

| Keys | Action | Command |
| --- | --- | --- |
| `/` | Search (`re:` for a regular expression) | `:search` |
| `n`, `N` | Next or previous match | `:next-match`, `:prev-match` |
| `F` | Show only matching lines | `:keep-matching` |
| `f` | Follow new output on or off | `:follow` |
| `w` | Wrap long lines | `:wrap` |
| `t` | Show or hide the time column | `:timestamps` |
| `v` | Select lines: `j` `k` extend, `y` copies, `esc` ends | `:visual` |
| `shift+↓`, `shift+↑` | Select down or up from the bottom line | `:select-down`, `:select-up` |
| `y` | Copy the line at the bottom | `:yank` |
| `H`, `L` | Scroll sideways while lines are not wrapped | `:scroll-left`, `:scroll-right` |
| `a` | Attach, as in the server list | `:attach` |
| `c` | Clear the daemon's scrollback (asks first) | `:clear-logs` |
| `esc` | Clear the search, then back to the list | `:back` |

### Details, Env, Metrics

| Keys | Action | Command |
| --- | --- | --- |
| `y` | Copy the value under the cursor (a secret too, without showing it) | `:copy` |
| | Copy the server's folder, or a container's Compose folder | `:copy-dir` |
| `esc` | Back to the list | `:back` |

On the Logs tab, `:copy` copies the line at the bottom (as `y` does there), and `:copy-dir` copies the shown server's folder from any tab and from the servers list.

## Menus and the palette

`x`, or a right click on a row, opens the menu of the focused pane's actions, titled with what they act on (`web`, `group shop (4)`): a server's actions and, in a group, its group's; a container's and its project's; a port's. Its last item, **All actions** (`ctrl+p`), opens the palette, which with the command line and the help holds everything else (the config, settings, computers, the daemon and the agents). `j` `k` move, `enter` or `space` run the highlighted item, an item's own key runs it, and `esc`, `q` or `x` close it. Items that do nothing for this target stay listed, greyed; with one highlighted, the reason shows under the list (`Stop: not running`). Toggles such as follow and wrap show `✓` when on. A menu taller than the screen scrolls with the cursor and says how many items are above or below it (`↓ 8 more`). `q` closes the help, the messages, a menu or a confirmation, never the program; where you type text (the palette, the command line, a filter, a form field, a pairing code) `q` is a letter. `ctrl+c` always quits, even with a dialog open.

`ctrl+p` opens the palette: type a few letters of what you want (`rw` for restart web, `theme nord`, `shell api`) and press `enter`. It lists the global actions, every theme, icon set and state filter, and each target action once for each server, group, container, Compose project and (while the Ports pane is open) port: `Restart web`, `Restart container shop-db-1`, `Stop project shop`, `Kill node 5141 on :3016`. Results whose action word starts with what you typed come first (`res` lists the restarts, the cursor's own first, before a recreate whose container name happens to hold the letters), and the matched letters are highlighted. Each row shows the key that does the same from where you are, or else its command, so the palette also teaches the keys. Actions that remove something are marked, and actions that do nothing now are greyed. Under the list, the highlighted row's command, its reason when it does nothing now, and its full name when the row cuts it.

## Command line

`:` opens the command line. The completions for the word under the cursor (commands, then server, group and container names, themes, icon sets, states and computers) show above it as you type; the one `tab` puts in place stands out, `tab` cycles forward and `shift+tab` back, and the panes make room for the row. `↑` and `↓` walk the history of the last 100 commands. Quote names with spaces: `:restart "my app"`. A command may be shortened while it stays unambiguous (`:rest` is `:restart`).

A command without a name acts on what the cursor is on, as its key does; a group name acts on the group. A name is looked up among the kinds the command takes, the focused pane's first, so `:restart shop-db-1` restarts the container and `:restart web` the server. `:filter-state` takes the states the list's glyphs show (`unready` is a running server that stopped answering). In `:messages`, `enter` on a notice about a server or container that still exists selects it; the overlay names the keys that work on the highlighted notice. The [key tables](#keys) list every command with its argument. Before it runs anything, the command line checks the argument: how many a command takes and that a name names something of the right kind. A wrong name gets the closest one (`no server named "blgo"; did you mean blog?`, `:start-group web` says web is a server in group shop), and a command that cannot act on its target says why (`stop blog: not running`, `restart mailer: disabled; S starts it anyway`).

## Confirmations

Removing a server from the config (`d`), clearing its scrollback (`c`), killing a process or stopping the server on its port (`d` or `:kill` in Ports), `compose down` (`d` in Containers), turning sharing on, upgrading or restarting the daemon, closing a form with changes and overwriting a config that changed on disk ask first. The dialog names the target and the consequence and starts on Cancel: `tab`, the arrows or `h` `l` choose, `enter` runs the choice, `y` confirms and `n`, `q` or `esc` cancel; both choices can be clicked.

## Logs

The logs open with the server's scrollback from the same connection that then carries new output, so nothing is shown twice or missed at the join. When the terminal UI falls behind a very fast program, it drops the oldest waiting output from a line start and marks the place with a `some output was not shown` rule. The daemon also drops live output for a connection that falls far behind rather than slow the program down, and that loss has no mark; the log file keeps everything. The daemon's start, stop and crash markers are drawn as rules.

- **Reading state:** the bottom border shows independent pieces, for example `PAUSED · 240 new · search error 3/17 · wrap`: following or paused with the lines that arrived since, the search with the current match and how many lines match (or `filter` and the count while only matching lines show), and `wrap` or the first column shown. Moving up pauses following; `f` or `>` follows again. Each of the last 8 servers you looked at keeps its position, search, filter and time column choice.
- **Time column:** when the daemon received each line. It appears on its own once the latest lines carry those times and most of them do not begin with a time of their own; the choice is made once per server, so it does not come and go while output arrives, and `t` overrides it. Lines restored after a daemon restart have no time and leave it blank. Container logs carry no times from the engine, so the column shows when each line arrived and is off until `t`.
- **Search:** `/` highlights the matches as you type: plain text ignoring case, or `re:` and a Go regular expression. `enter` goes to the newest match on screen or above it, `n` `N` to the next or previous one, and `esc` clears it. The current match is drawn in reverse, the others highlighted. `F` shows only the matching lines.
- **Scrolling:** positions count screen rows, so a wrapped line taller than the pane can be read from its first row. With wrapping off, `H` `L` scroll sideways and a search scrolls to its match.
- **Copying:** `y` copies the bottom line; `v` or `shift+↑` `shift+↓` start a selection that `j` `k` extend and `y` copies. Copying uses the terminal's clipboard support (OSC 52), which also works over SSH. After copying a selection, the pane keeps showing it instead of following new output unless the selection reached the newest line.
- Links and `file:line` references are highlighted; links open with your terminal's link click.

Attach (`a`) suspends the terminal UI and connects the terminal to the server's process, with its input and size. It ends with `ctrl+]`, when the process exits or is restarted, or when the connection to the daemon fails; the terminal UI comes back where it was, and the footer says which of these ended it. When the terminal UI is told to end (a SIGTERM or SIGHUP, or its program ending) while it is suspended for attach, a container's shell, `E`'s shell or `:doctor`, the session ends first (a shell gets SIGHUP, as when its terminal closes, and SIGKILL 2 seconds later), then the terminal is restored and the terminal UI quits.

## Details, Env and Metrics

- **Details:** state, restarts, schedule and next run, addresses, the ports it listens on and who can reach them, command and folder. A container's Details show its state, image, project and service, owner server, ports and what the daemon flags, then from an inspection the command, creation time, health check, volumes, networks, compose files and its environment.
- **Env:** the config's variables, with which config file and revision they come from, marked `stale` when a refresh failed.
- **Metrics:** CPU and memory now, their peak and history, or whether the server is stopped or the first sample is due.

Secret-looking values are hidden, as in the dashboard; `y` copies one without showing it. Long values wrap over several rows, text from the config that would control the terminal shows as `�`, and addresses are links your terminal can open.

## Containers

The container actions run in the daemon, which runs `docker compose` for the recreate, rebuild, pull and project actions. A compose job can take minutes: its project header shows a spinner and what it does, the Job tab streams its output and says how it ended (its output stays until the next job), and the project's start, stop, restart, recreate, rebuild, pull and down wait until it does (its shell and open do not); `:dismiss-job` hides the finished job. A container's Logs follow it across restarts and recreation; `a` on its Logs tab opens its shell too (its menu calls it Shell), its Shell tab takes the container's keys, and its Details tab's `x` menu has the container's actions. When the engine is not running, `:start-docker` starts it when the daemon knows how (`docker_start`, or a detected engine) ([Containers](containers.md)).

## Ports

`d` kills the process on the port under the cursor, after a dialog that names the process, its PID and the port; on the port of a server the daemon manages it asks to stop the server instead (`Stop server api on port 3102?`). `:kill 3016` or `:kill 5141` names the port or the PID instead of the row and opens the pane for it; when several processes listen on the port, it asks for the PID. Processes the daemon protects (itself, and what looks like a system or container service, such as the runtime that publishes container ports) cannot be killed here, and the menu says why.

A server whose port another process holds (`◎`) starts with `t` on that port's row, or `:takeover` on the server, which stops the other process first.

## Editing the config

Every edit goes through the daemon, as the dashboard's do: it checks the values and keeps your comments, and with `--on` the other computer's config is edited ([Configuration](configuration.md#editing-and-reloading)).

- `:config` (also in the palette) opens the config text in your editor (`$VISUAL`, else `$EDITOR`, else `vi`, run through `sh` so it may hold arguments) on a private copy (`config.toml` in a directory only you can read, named `portside-config-<pid>-…` in the temporary folder), with the terminal UI suspended. Leaving the editor saves the text, unless it is unchanged, with the revision it was opened at. When the daemon refuses the text, the dialog shows why and **Edit again** reopens the editor with your text; when the file changed since you opened it, **Overwrite** replaces that change with your text and Cancel, the default, drops your edits. The copy exists only while the editor runs: it is read back and removed when the editor exits, whether or not the result is still wanted (a newer `:config` or `:on` another computer drops it). Copies left by a terminal UI that was killed while its editor ran are removed by the next `:config`. `portside config edit` edits the file itself, on this computer, without these checks.
- `e` or `:edit web` opens a server's entry as a form, `:settings` the top-level settings, with the defaults shown where a key is not set. `n` or `:new` lists the projects a scan of the projects folder finds (`:scan <folder>` scans another), with their kind, port and folder and the command under the highlighted one; `enter` opens the form filled in from it, and the last row or `m` an empty form; `space` marks proposals and `enter` then adds the marked ones as they are, keeping any the daemon refuses in the list with its reason. A new server is selected, not started. Its form fills in the lowest free port when the proposal has none; clear the field to leave the port automatic (assigned when the config loads), or write `0` for no port.
- In a form, `j` `k` and `tab` move, `enter` or `i` edit a value (insert mode: `esc` or `enter` keep it, `tab` keeps it and moves on), `space` and `enter` switch an on/off value or pick the next choice (`space` does nothing on a text value), `ctrl+s` saves and `esc` closes, asking first when something changed; the footer shows these keys. The line under the highlighted field explains it; a value too long for its column shows whole above that line. An error from the daemon is shown under the field it names. A server's form starts with the keys most edits need; the **More** row (with how many of the others are set) shows readiness, limits, schedule, tools, dashboard and event settings, and opens by itself when an error names one of them. Secret-looking environment values are masked until you edit the field. Lists take one item per line (environment, watch patterns, `PATH` folders) or commas (depends on, Compose projects). Pasting into a one-line value turns each line break into one space and keeps every other character.
- A save writes only the values you changed; the others stay exactly as the file has them, comments included. Environment values keep every character after the first `=`, spaces included; only the name is trimmed. Read-only values say why next to them: a server's name (rename with `:config`), an environment that holds a value spanning lines (shown with `\n`, edited with `:config`) and the commands a server runs on events (`on_crash`, `on_unready`, `on_recover`, at the end of its form).
- After a save, servers that keep running with their old settings are named and one dialog offers to restart them: the edited server after its form, every such server after `:config` and the settings. A form whose config changed elsewhere in the meantime is refused and can be reloaded.

The forms leave out some keys, which `:config` edits: a server's event commands (`on_crash`, `on_unready`, `on_recover`, shown read-only at the end of its form), and in the settings the dashboard's appearance (theme, palette, layout, language, app icon) and the `editor`, `terminal` and `share` keys. The terminal UI's own theme and icons are `T` and `:icons`.

## Other computers

`C` or `:computers` opens **Computers**: this computer with its sharing and paired devices, the computers on your tailnet that share their daemon (paired or not, connected, version, how many servers run), and the forwarded ports of the connected one. The marked row is the computer this session is on.

- `enter` on a computer switches the session to it, connecting first when needed: the panes, the selection and the operations in progress start over, the top bar names the computer, and replies still on their way from the previous one are dropped. `enter` on this computer, `:on local` or `:on` alone come back. `:on <computer>` does the same from the command line.
- `c` connects without switching, `d` disconnects (a session on that computer comes back here), `p` pairs: it asks for the code the other computer shows (`portside share code` there, or its Computers) in a field that hides what you type; the code never goes through the command line or its history. A computer that needs pairing asks for the code on its own, and `:pair <computer>` opens the same field.
- `s` turns sharing of this computer on, after a confirmation (it makes this daemon reachable on your tailnet and gives paired devices full control of its servers), or off; `n` shows a new pairing code for another device.
- `f` on a forwarded port turns its forwarding on or off; `R` scans the tailnet again.

On another computer, addresses show and open as they do from this computer (through a forwarded port or the `*.localhost` proxy), following forwarding as it is turned on or off here or in another client; a container's port or a server's other port opens only through its forwarded port (`no local address for :5432 on mbp` otherwise), and the editor app, the terminal app and the shell (`E`) are not offered, since they would open on the wrong computer. Errors from the daemon show in the footer as it words them and stay in `:messages`. Setting up Tailscale, revoking a paired device and forwarding a port to another local port are done in the dashboard or the CLI ([Other computers](other-computers.md)).

## The daemon

`:daemon`, or a click on the port in the top bar, shows the daemon: version, process, port, uptime, the config file, the state directory, the dashboard's address and where the API token is (requests are signed with it; it is never sent). There, or from the command line:

- `:upgrade` (`u` there) replaces the daemon in place with this command's binary when that is a newer release, as `portside daemon restart --in-place` does; `:restart-daemon` (`r` there) restarts it in place with this binary, as `portside daemon restart` does. Both ask first, keep the servers running and act on this computer's daemon. The top bar says `restarting, reconnecting` until the new daemon answers, then a notice names its version.
- A daemon too old for an upgrade in place is stopped and started by `portside daemon restart` in this terminal, which restarts its servers; the restart's confirmation says so.

Installing a new release stays with Homebrew or a download ([Install](../README.md#install)): after `brew upgrade`, `:upgrade` in a newly started `portside tui` moves the running daemon to it.

## Coding agents

`:setup` lists the coding agents' setup items (the MCP server for Claude Code and Codex, the agent skill) with their state; `space` checks one (the ones that need it start checked), `enter` sets up the checked ones and shows each result, with the command to run yourself when one fails. `:doctor` runs `portside doctor` in this terminal and comes back after `enter`. Both act on this computer ([Coding agents](coding-agents.md)).

## Themes and icons

The themes are Terminal (`terminal`), which uses your terminal's 16 colors and is the default, and the dashboard's Rosé Pine, Everforest, Tokyo Night, Catppuccin, Nord and Neutral. Since Terminal cannot know how your colors contrast, it shows the active tab, the mode and the highlighted entry of a menu or dialog in reverse video of your terminal's text and background, and the selected row's text in your terminal's text color, with the state glyphs still colored. The light or dark variant follows the terminal's background unless `-light` or `-dark` sets it. `T` cycles the themes, `:theme nord-light` picks one.

Icons are Unicode by default, Nerd Font icons with `--icons nerd` or when `$NERD_FONT` is set, and ASCII in a locale that is not UTF-8 (`LC_ALL`, `LC_CTYPE`, `LANG`). `:icons` cycles them.

The theme, its variant and the icons you pick are kept in `tui.json` in the state directory ([Configuration](configuration.md#where-things-live)) and used on the next start; `--theme` and `--icons` apply to one run.

## Mouse

Click a row to select it or a group header to fold it, double-click a server to open its logs, click a tab, a state count in the top bar, a footer key (or an arrow of the narrow tab strip), a collapsed pane's header, a value in Details or Env, a dialog's choice or a toast, scroll with the wheel, and right-click a row for the menu `x` opens, which acts on that row. A double click on a container opens its logs too. In Computers, Daemon and Coding agents, click a row to select it (twice on a computer to switch to it), the sharing line to turn sharing on or off, a forwarded port's state to turn its forwarding on or off, an agent's check box to check it, and the actions at the bottom to run them.

## Troubleshooting

- **No colors:** `NO_COLOR` is set, or the terminal reports no color support. States keep their glyphs and the selected row its `▎`; `--theme` does not bring colors back while `NO_COLOR` is set.
- **Wrong light or dark variant:** the themes ask the terminal for its background (OSC 11) and assume a dark one when it does not answer, as some terminals and multiplexers do not. `:theme terminal-light` sets the variant and keeps it; `--theme terminal-light` sets it for one run.
- **No mouse in tmux:** tmux passes mouse events on only with `set -g mouse on` in `~/.tmux.conf`.
- **Copy does nothing:** the terminal must allow OSC 52 clipboard writes, which some turn off by default; tmux passes them on with `set -g set-clipboard on`. The terminal does not report whether it accepted the text, so the notice cannot tell.
- **Attach does not detach:** the detach key is `ctrl+]` and cannot be changed; make sure tmux, screen or your terminal does not bind it. `ctrl+c` goes to the attached process.
- **`tui needs an interactive terminal`:** standard input or output is not a terminal, as under a pipe or `ssh host portside tui` without `-t`; use `ssh -t`.
- **Boxes or question marks instead of icons:** pick `--icons ascii`, or `--icons nerd` with a Nerd Font.
