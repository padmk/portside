# Development

Requirements: Go 1.26+ (`go.mod`'s `toolchain` line names the release that CI uses), Node 24 and pnpm 12.6.0.

```sh
make dev-daemon     # isolated daemon on :7980 (config and state in .dev/)
make dev-open       # sign the browser in; prints the Vite address on the dev daemon's dashboard host (new on each cold start)
make dev-ui         # dashboard with hot reload on http://<dashboard host>:5173 (the session cookie covers every port of that host)
make build          # dashboard + bin/portside
make check          # go vet, go test, dashboard build, license notices
make notices        # regenerate THIRD_PARTY_NOTICES.txt
make snapshot       # release archives in ./dist (goreleaser)
make app            # macOS app in dist/macos/Portside.app (scripts/build-macos-app.sh)
make app-install    # build the app, install it in /Applications and upgrade the running daemon in place
make install        # copy bin/portside to ~/.local/bin
```

`AGENTS.md` maps the code and lists the constraints changes must keep.

## Next to an installed Portside

On a Mac that runs Portside from Homebrew, keep that install for daily work and develop beside it:

- `make dev-daemon`, `make dev-open` and `make dev-ui` run an isolated daemon on :7980 with its config and state in `.dev/`.
- `make app` builds `dist/macos/Portside.app`; `scripts/onboarding-preview.sh` runs it as a second instance with its own config, state, daemon and temporary home.
- `scripts/upgrade-from-release.sh <binary>` upgrades the latest published release in place to that binary on an isolated instance and checks that the servers stay ready.
- Anything else runs isolated with `PORTSIDE_CONFIG` and `PORTSIDE_STATE_DIR` in a temporary directory and a free `port`.

Do not run `make app-install` or `make install` there: the first replaces the app in `/Applications` and upgrades the running daemon to your build, the second puts another `portside` in `~/.local/bin` that can shadow the one Homebrew installed. Update the installed copy with `brew upgrade --cask portside`; the reopened app upgrades the daemon in place.

## Idle benchmark

`scripts/bench-idle.sh` measures what an idle daemon costs, for the numbers in [How it works](how-it-works.md#periodic-checks).

- It builds the binary (unless given `--binary PATH`) and the events client `scripts/benchclient` into a fresh temporary directory.
- It starts an isolated daemon there on a free port with 21 `python3 -m http.server` servers, 5 of them running (`--servers`, `--running`), and counts the daemon's `ps` and `lsof` calls through wrappers first on its `PATH`.
- It prints the daemon's CPU time and those calls and their CPU over one window each (`--window`, default 60 s): with no events client, with one connected with `usage=0` (the app with its panel closed) and with one showing usage (an open dashboard).
- It never talks to the account's own daemon, stops its daemon and servers on exit, and moves the temporary directory to the Trash with `trash` (or prints its path when `trash` is missing).

It needs python3 and Go and runs on macOS and Linux.

## Continuous integration

- **Linux** (`ci.yml`, pushes to main and pull requests): `make build`, the license notices check, `go vet`, `go test -race`, `pnpm audit --prod --audit-level high`, `govulncheck`, the theme contrast check, then the systemd user service end to end on the runner (`scripts/linux-service-e2e.sh`) and an in-place upgrade from the latest release (`scripts/upgrade-from-release.sh`). The end-to-end script uses the default config and state locations, so run it yourself only on a disposable machine.
- **macOS** (`ci.yml`, pushes to main, pull requests and on demand from the Actions tab with "Run workflow"): builds the app, runs the Go tests and the in-place upgrade from the latest release.
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

`THIRD_PARTY_NOTICES.txt` lists the Go modules, dashboard packages and fonts, and the color themes the palettes come from. It ships in the release archives and in `Portside.app/Contents/Resources` and is served by the dashboard at `/licenses.txt` (Settings, "Open source licenses"). `make notices` regenerates it after a dependency change; `make check`, CI and the release build fail while it is stale.
