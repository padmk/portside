# Development

Requirements: Go 1.26+, Node 24 and pnpm 12.

```sh
make dev-daemon     # isolated daemon on :7980 (config and state in .dev/)
make dev-open       # sign the browser in; prints the Vite address on the dev daemon's dashboard host (new on each cold start)
make dev-ui         # dashboard with hot reload on http://<dashboard host>:5173 (the session cookie covers every port of that host)
make build          # dashboard + bin/portside
make check          # go vet, go test, dashboard build, license notices
make snapshot       # release archives in ./dist (goreleaser)
make app            # macOS app in dist/macos/Portside.app (scripts/build-macos-app.sh)
make app-install    # build the app, install it in /Applications and upgrade the running daemon in place
```

`AGENTS.md` maps the code and lists the constraints changes must keep.

## Next to an installed Portside

On a Mac that runs Portside from Homebrew, keep that install for daily work and develop beside it:

- `make dev-daemon`, `make dev-open` and `make dev-ui` run an isolated daemon on :7980 with its config and state in `.dev/`.
- `make app` builds `dist/macos/Portside.app`; `scripts/onboarding-preview.sh` runs it as a second instance with its own config, state, daemon and temporary home.
- Anything else runs isolated with `PORTSIDE_CONFIG` and `PORTSIDE_STATE_DIR` in a temporary directory and a free `port`.

Do not run `make app-install` or `make install` there: they replace the app in `/Applications` and the `portside` command that Homebrew installed. Update the installed copy with `brew upgrade --cask portside`; the reopened app upgrades the daemon in place.

## Idle benchmark

`scripts/bench-idle.sh` measures what an idle daemon costs, for the numbers in [How it works](how-it-works.md#periodic-checks). It builds the binary (unless given `--binary PATH`) and the events client `scripts/benchclient` into a fresh temporary directory, starts an isolated daemon there on a free port with 21 `python3 -m http.server` servers, 5 of them running (`--servers`, `--running`), and counts the daemon's `ps` and `lsof` calls through wrappers first on its `PATH`. It prints the daemon's CPU time and those calls and their CPU over one window each (`--window`, default 60 s): with no events client, with one connected with `usage=0` (the app with its panel closed) and with one showing usage (an open dashboard). It never talks to the account's own daemon, stops its daemon and servers on exit, and moves the temporary directory to the Trash with `trash` (or prints its path when `trash` is missing). Needs python3 and Go; macOS and Linux.

## Releases

A `v*` tag runs `.github/workflows/release.yml`. Two build jobs, which can read the repository and attest their files but not publish, make the CLI archives for macOS (arm64) and Linux (amd64, arm64) with GoReleaser and the app as `Portside-<version>.zip`; a `publish` job, which runs none of the built code, creates the release from those files, and a `tap` job in the `release` environment (open to `v*` tags only, which only repository admins may create) updates the tap. Before anything is published, the macOS archive's binary reports the tag's version and starts and stops a daemon, the app zip, unpacked, passes `codesign --verify` and its bundled command reports the version too, and `publish` checks that the tag points at a commit of `main` whose `ci` and `codeql` runs passed (waiting for runs still in progress). Releases are immutable: once published, their files and tag cannot change. Running the workflow from the Actions tab is a dry run: it builds everything and publishes nothing. The release notes are the version's section of `CHANGELOG.md`. CI and releases build with the Go version in the `toolchain` line of `go.mod` (the `go` line is the oldest Go that builds from source); CI runs `govulncheck` and `pnpm audit --prod --audit-level high`, so raise the `toolchain` line when a Go security release fixes something it reports. When the `release` environment has a `HOMEBREW_TAP_TOKEN` secret (a fine-grained token with contents write on the tap only), the cask `Casks/portside.rb` and the formula `Formula/portside.rb` are published to the `homebrew-tap` repository of the same account; without it that step is skipped. Each archive has an SPDX SBOM (`<archive>.sbom.json`, made by syft), and every published file (archives, SBOMs, `checksums.txt` and the app zip) has a GitHub build provenance attestation; check a download with `gh attestation verify <file> -R <owner>/<repo>` (for this repository `-R padmk/portside`). CI builds on Linux for every push and pull request, runs the tests and then the systemd user service end to end on the runner (`scripts/linux-service-e2e.sh`, which uses the default config and state locations, so run it yourself only on a disposable machine), and CodeQL (`codeql.yml`) scans the Go and TypeScript code on the same events and weekly; the macOS build, with the Go tests, runs for pushes to main and pull requests, from the Actions tab (`ci` workflow, "Run workflow") and on release tags. The app is signed ad hoc unless `SIGN_IDENTITY` is set; notarization needs an Apple Developer account.

## Third-party licenses

`THIRD_PARTY_NOTICES.txt` lists the Go modules, dashboard packages and fonts, and the color themes the palettes come from. It ships in the release archives and in `Portside.app/Contents/Resources` and is served by the dashboard at `/licenses.txt` (Settings, "Open source licenses"). `make notices` regenerates it after a dependency change; `make check` and CI fail while it is stale.
