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

## Idle benchmark

`scripts/bench-idle.sh` measures what an idle daemon costs, for the numbers in [How it works](how-it-works.md#periodic-checks). It builds the binary (unless given `--binary PATH`) and the events client `scripts/benchclient` into a fresh temporary directory, starts an isolated daemon there on a free port with 21 `python3 -m http.server` servers, 5 of them running (`--servers`, `--running`), and counts the daemon's `ps` calls through a wrapper first on its `PATH`. It prints the daemon's CPU time and the `ps` calls and their CPU over one window each (`--window`, default 60 s): with no events client, with one connected with `usage=0` (the app with its panel closed) and with one showing usage (an open dashboard). It never talks to the account's own daemon, stops its daemon and servers on exit, and moves the temporary directory to the Trash with `trash` (or prints its path when `trash` is missing). Needs python3 and Go; macOS and Linux.

## Releases

A `v*` tag runs `.github/workflows/release.yml`: GoReleaser publishes the CLI archives for macOS (arm64) and Linux (amd64, arm64), then the app is built and attached to the release as `Portside-<version>.zip`. The release notes are the version's section of `CHANGELOG.md`. CI and releases build with the Go version in the `toolchain` line of `go.mod` (the `go` line is the oldest Go that builds from source); CI runs `govulncheck`, so raise the `toolchain` line when a Go security release fixes something it reports. When a `HOMEBREW_TAP_TOKEN` secret exists, the cask `Casks/portside.rb` is also published to the `homebrew-tap` repository of the same account; without it that step is skipped. Each archive has an SPDX SBOM (`<archive>.sbom.json`, made by syft), and every published file (archives, SBOMs, `checksums.txt` and the app zip) has a GitHub build provenance attestation; check a download with `gh attestation verify <file> -R <owner>/<repo>` (for this repository `-R padmk/portside`). CI builds on Linux for every push and pull request, and CodeQL (`codeql.yml`) scans the Go and TypeScript code on the same events and weekly; the macOS build, with the Go tests, runs for pushes to main and pull requests, from the Actions tab (`ci` workflow, "Run workflow") and on release tags. The app is signed ad hoc unless `SIGN_IDENTITY` is set; notarization needs an Apple Developer account.

## Third-party licenses

`THIRD_PARTY_NOTICES.txt` lists the Go modules, dashboard packages and fonts, and the color themes the palettes come from. It ships in the release archives and in `Portside.app/Contents/Resources` and is served by the dashboard at `/licenses.txt` (Settings, "Open source licenses"). `make notices` regenerates it after a dependency change; `make check` and CI fail while it is stale.
