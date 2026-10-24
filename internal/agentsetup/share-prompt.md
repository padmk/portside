Set up two of my computers so the {{.Name}} dashboard on one of them manages the dev servers on the other. {{.Name}} is a local dev-server manager with a CLI (`{{.ID}}`); its "Other computers" feature works over Tailscale. You run on one of the two computers. Ask me which computer runs the projects (the shared computer) and which one I work on, unless I already said so.{{if .Path}} `{{.ID}}` is not on PATH here, so run it as `{{.Path}}`.{{end}}

At the end:
- both computers have Tailscale connected and signed in to the same Tailscale account, and neither device is tagged;
- the tailnet has HTTPS certificates enabled;
- both computers run {{.Name}}, and the shared computer has sharing on;
- the computer I work on is paired with the shared computer and connected to it.

Do the steps for this computer yourself. For the other computer, tell me exactly what to run there; if I give you SSH access to it, ask me before using it.

1. Tailscale. Run `tailscale status`. On macOS the command may be missing from PATH while the app is installed; then use `/Applications/Tailscale.app/Contents/MacOS/Tailscale status`.
   - Not installed: ask me first. On macOS run `brew install --cask tailscale-app` (or have me download it from https://tailscale.com/download) and `open -a Tailscale`. On Linux run `curl -fsSL https://tailscale.com/install.sh | sh`.
   - Not connected or signed out: on macOS ask me to open Tailscale in the menu bar and sign in. On Linux run `sudo tailscale up` and give me the sign-in URL it prints. I sign in in my browser; wait for me.
   - Check that `tailscale status` lists both computers under the same account.
2. On Linux only, let {{.Name}} use Tailscale Serve once: `sudo tailscale set --operator=$USER` (ask me first).
3. On the shared computer, {{.Name}} has to be installed{{if .Cask}} (on macOS: `brew install --cask {{.Cask}}`){{end}}; `{{.ID}} status` starts its daemon. Run `{{.ID}} share on`: it turns sharing on and prints a pairing code valid for 10 minutes. `{{.ID}} share` shows whether the computer is reachable and any error, and `{{.ID}} share code` prints a new code.
   - If it reports that HTTPS certificates are needed, I have to enable them once in the Tailscale admin console, DNS page (https://login.tailscale.com/admin/dns). Ask me to, then run `{{.ID}} share` again; it retries within a minute.
4. On the computer I work on, `{{.ID}} computers` lists the shared computer. Ask me for the pairing code, then run `{{.ID}} pair <computer> <code>`, which pairs and connects.
5. Check with `{{.ID}} status --on <computer>` on the computer I work on, then summarize what you did and anything left for me.

Do not change Tailscale ACLs, tags or Funnel, and do not turn sharing on for the computer I work on unless I ask. Use a pairing code only with `{{.ID}} pair`. Paired devices get full control of the shared computer's dev servers, so pair only my own devices. Do not stop or restart running dev servers.
