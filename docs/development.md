# Development

Requirements: Go 1.26+ (`go.mod`'s `toolchain` line names the release that CI uses), Node 24 and pnpm 12.6.0.

```sh
make dev-daemon     # isolated daemon on :7980 (config and state in .dev/)
make dev-open       # sign the browser in; prints the Vite address on the dev daemon's dashboard host (new on each cold start)
make dev-ui         # dashboard with hot reload on http://<dashboard host>:5173 (the session cookie covers every port of that host)
make build          # dashboard + bin/portside
make check          # go vet, make lint, go test -race, dashboard build, license notices, terminal UI palettes
make lint           # the linters (see Linters and hooks)
make hooks          # run .githooks/pre-commit before every commit in this clone
make notices        # regenerate THIRD_PARTY_NOTICES.txt
make snapshot       # release archives in ./dist (goreleaser)
make app            # macOS app in dist/macos/Portside.app (scripts/build-macos-app.sh)
make app-install    # build the app, install it in /Applications and upgrade the running daemon in place
make install        # copy bin/portside to ~/.local/bin
```

[Code map](code-map.md) maps the code and lists what the tests cover; `AGENTS.md` lists the commands and the constraints changes must keep.

## Next to an installed Portside

On a Mac that runs Portside from Homebrew, keep that install for daily work and develop beside it:

- `make dev-daemon`, `make dev-open` and `make dev-ui` run an isolated daemon on :7980 with its config and state in `.dev/`.
- `make app` builds `dist/macos/Portside.app`; `scripts/onboarding-preview.sh` runs it as a second instance with its own config, state, daemon and temporary home.
- `scripts/upgrade-from-release.sh <binary>` upgrades the latest published release in place to that binary on an isolated instance and checks that the servers stay ready.
- Anything else runs isolated with `PORTSIDE_CONFIG` and `PORTSIDE_STATE_DIR` in a temporary directory and a free `port`.

Do not run `make app-install` or `make install` there: the first replaces the app in `/Applications` and upgrades the running daemon to your build, the second puts another `portside` in `~/.local/bin` that can shadow the one Homebrew installed. Update the installed copy with `brew upgrade --cask portside`; the reopened app upgrades the daemon in place.

## Linters and hooks

`make lint` builds golangci-lint and `tools/lintcheck` into `bin/` from the separate module `tools/go.mod`, with this module's toolchain, and runs:

1. `go vet -vettool=bin/lintcheck`: the project's own analyzers. `httpclienttimeout` reports an `http.Client` literal without `Timeout`; a client whose requests carry their own deadline, or one for a WebSocket or another long-lived stream, sets `Timeout: 0` with a comment saying why. `phaseassign` reports a change of a server's phase (`proc.ph` in `internal/supervisor`) outside `transition.go`; tests may set one by hand.
2. golangci-lint with `.golangci.yml`, without `noctx` and `contextcheck`.
3. `noctx` and `contextcheck` over code changed since `LINT_BASE` only (default: the merge base with `origin/main`), since older code calls `exec.Command`, `net.Dial` and the like without a context.
4. `funlen` and `gocognit`, which only print warnings (`LINT_WARN=0` leaves them out).

`LINT_PKGS` narrows a run to some packages. An exclusion is either a narrow rule in `.golangci.yml` with its reason or a `//nolint:<linter> // <reason>` on the line; never disable a linter for the whole tree. To upgrade golangci-lint, run `go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version>` in `tools/`.

`make hooks` sets `core.hooksPath` to `.githooks` for this clone. The pre-commit hook checks the staged files: `gofmt`, `make lint` on the packages of the staged Go files (with `LINT_BASE=HEAD`, no warnings), and `pnpm --dir web typecheck` when TypeScript or the web project's config changes (it needs `pnpm --dir web install` first). `git commit --no-verify` skips it once.

## Model tests

`TestModelLifecycle` (`internal/supervisor/model_test.go`) drives one server or task through random sequences of starts (also ones held in their preflight), stops, exits, reaps, the passing of time, upgrades in place, renames and config reloads with [rapid](https://pkg.go.dev/pgregory.net/rapid), and compares the supervisor after each step with a reference model of what the documentation promises. It runs on a fake process table and clock (`fakesys_test.go`) that replaces the supervisor's `system` (`internal/supervisor/system.go`): processes exit, are reaped and let go of their terminal only when a step says so, restart timers fire only when the clock advances, and a start can be held at its port release. The default run makes 20 sequences per model; deeper runs:

```sh
go test ./internal/supervisor -run TestModelLifecycle -rapid.checks=2000 -rapid.steps=60
```

`TestModelDependencies` (`model_deps_test.go`) does the same for a server that depends on a task (a migration): the task runs once for every start that needs it, also across upgrades in place, and the server starts only after the task succeeded. A failure prints the steps (`ran <step>` lines), shrunk to a short sequence, and saves them under `internal/supervisor/testdata/rapid` (ignored by git); `-rapid.failfile=<file>` replays one.

## Idle benchmark

`scripts/bench-idle.sh` measures what an idle daemon costs, for the numbers in [How it works](how-it-works.md#periodic-checks).

- It builds the binary (unless given `--binary PATH`) and the events client `scripts/benchclient` into a fresh temporary directory.
- It starts an isolated daemon there on a free port with 21 `python3 -m http.server` servers, 5 of them running (`--servers`, `--running`), the first of them watching that many Python files with `--watch FILES`, and counts the daemon's `ps` and `lsof` calls through wrappers first on its `PATH`.
- It prints the daemon's CPU time and those calls and their CPU over one window each (`--window`, default 60 s): with no events client, with one connected with `usage=0` (the app with its panel closed) and with one showing usage (an open dashboard).
- It never talks to the account's own daemon, stops its daemon and servers on exit, and moves the temporary directory to the Trash with `trash` (or prints its path when `trash` is missing).

It needs python3 and Go and runs on macOS and Linux.

## Continuous integration

- **Linux** (`ci.yml`, pushes to main and pull requests): `make build`, the license notices check, `go vet`, `make lint`, `go test -race`, `pnpm audit --prod --audit-level high`, `govulncheck`, the theme contrast check, then the systemd user service end to end on the runner (`scripts/linux-service-e2e.sh`) and an in-place upgrade from the latest release (`scripts/upgrade-from-release.sh`). The end-to-end script uses the default config and state locations, so run it yourself only on a disposable machine.
- **macOS** (`ci.yml`, pushes to main, pull requests and on demand from the Actions tab with "Run workflow"): builds the app, runs `make lint`, `go test -race` and the in-place upgrade from the latest release.
- Both jobs check out the whole history: `make lint` gets `LINT_BASE` from the pull request's base or the commit before the push.
- **CodeQL** (`codeql.yml`): scans the Go and TypeScript code on the same events and weekly. The Swift app is not scanned.

CI and releases build with the Go version in the `toolchain` line of `go.mod`; the `go` line is the oldest Go that builds from source. Raise the `toolchain` line when a Go security release fixes something `govulncheck` reports.

## Releases

A `v*` tag runs `.github/workflows/release.yml`:

1. Two build jobs, which can read the repository and attest their files but not publish, make the CLI archives for macOS (arm64) and Linux (amd64, arm64) with GoReleaser and the app as `Portside-<version>.zip`. Each archive gets an SPDX SBOM (`<archive>.sbom.json`, made by syft).
2. The release notes are the version's section of `CHANGELOG.md` plus an install footer; a tag without a section fails here.
3. Smoke tests: the macOS archive's binary reports the tag's version and starts and stops a daemon; the app zip, unpacked, passes `codesign --verify` and its bundled command reports the version too.
4. Every published file (archives, SBOMs, `checksums.txt` and the app zip) gets a GitHub build provenance attestation.
5. The `publish` job, which runs none of the built code, checks that the tag points at a commit of `main` whose `ci` and `codeql` runs passed (waiting for runs still in progress), then creates the release from those files.
6. The `tap` job, in the `release` environment (open to `v*` tags only, which only repository admins may create), publishes the cask `Casks/portside.rb` and the formula `Formula/portside.rb` to the `homebrew-tap` repository of the same account. It needs a `HOMEBREW_TAP_TOKEN` secret in that environment (a fine-grained token with contents write on the tap only) and is skipped without it.

Releases are immutable: once published, their files and tag cannot change. Running the workflow from the Actions tab is a dry run: it builds and smoke-tests everything and publishes nothing.

Check a download with `gh attestation verify <file> -R padmk/portside`.

The app is signed ad hoc unless `SIGN_IDENTITY` is set for `scripts/build-macos-app.sh`; notarization needs an Apple Developer account.

## Third-party licenses

`THIRD_PARTY_NOTICES.txt` lists the Go modules, dashboard packages and fonts, and the color themes the palettes come from. It ships in the release archives and in `Portside.app/Contents/Resources` and is served by the dashboard at `/licenses.txt` (through a Vite plugin in `web/vite.config.ts`; Settings, "Open source licenses"). `make notices` regenerates it after a dependency change; `make check`, CI and the release build fail while it is stale.
