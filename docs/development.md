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

## Releases

A `v*` tag runs `.github/workflows/release.yml`: GoReleaser publishes the CLI archives for macOS (arm64) and Linux (amd64, arm64), then the app is built and attached to the release as `Portside-<version>.zip`. The release notes are the version's section of `CHANGELOG.md`. When a `HOMEBREW_TAP_TOKEN` secret exists, the cask `Casks/portside.rb` is also published to the `homebrew-tap` repository of the same account; without it that step is skipped. CI builds on Linux for every push; the macOS build runs from the Actions tab (`ci` workflow, "Run workflow") and on release tags. The app is signed ad hoc unless `SIGN_IDENTITY` is set; notarization needs an Apple Developer account.

## Third-party licenses

`THIRD_PARTY_NOTICES.txt` lists the Go modules, dashboard packages and fonts, and the color themes the palettes come from. It ships in the release archives and in `Portside.app/Contents/Resources` and is served by the dashboard at `/licenses.txt` (Settings, "Open source licenses"). `make notices` regenerates it after a dependency change; `make check` and CI fail while it is stale.
