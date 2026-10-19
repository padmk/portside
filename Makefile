VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: build ui go install dev-daemon dev-open dev-ui check clean snapshot app app-install

## build: dashboard + binary in ./bin/portside (the dashboard must exist before Go embeds it)
build: ui
	$(MAKE) go

ui:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build

go:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/portside ./cmd/portside

## install: copy the binary to $(PREFIX)/bin
install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/portside $(PREFIX)/bin/portside

DEV_DIR := $(CURDIR)/.dev

## dev-daemon: isolated daemon on :7980 (config and state in .dev/) for the Vite dev server
dev-daemon: go
	@mkdir -p $(DEV_DIR)
	@[ -f $(DEV_DIR)/portside.toml ] || printf 'root = "~/dev"\nport = 7980\nrestore = false\n' > $(DEV_DIR)/portside.toml
	PORTSIDE_CONFIG=$(DEV_DIR)/portside.toml PORTSIDE_STATE_DIR=$(DEV_DIR)/state \
	PORTSIDE_DEV_ORIGIN=http://localhost:5173,http://127.0.0.1:5173 ./bin/portside daemon

## dev-open: sign the browser in to the dev daemon through Vite
dev-open:
	PORTSIDE_CONFIG=$(DEV_DIR)/portside.toml PORTSIDE_STATE_DIR=$(DEV_DIR)/state ./bin/portside open

## dev-ui: Vite with hot reload, proxying /api to the daemon
dev-ui:
	pnpm --dir web dev

check:
	go vet ./...
	go test ./...
	pnpm --dir web build

## snapshot: local release archives in ./dist (needs goreleaser)
snapshot:
	goreleaser release --snapshot --clean

## app: macOS menu bar app in dist/macos/Portside.app
app:
	scripts/build-macos-app.sh

## app-install: build and install /Applications/Portside.app (old copy goes to the Trash); a running copy is quit and reopened, a running daemon upgraded in place
app-install: app
	@running=0; if pgrep -qf /Applications/Portside.app/Contents/MacOS/; then running=1; osascript -e 'quit app "Portside"' >/dev/null; sleep 1; fi; \
	if [ -e /Applications/Portside.app ]; then osascript -e 'tell application "Finder" to delete POSIX file "/Applications/Portside.app"' >/dev/null; fi; \
	ditto dist/macos/Portside.app /Applications/Portside.app; \
	echo "Installed /Applications/Portside.app"; \
	cli=/Applications/Portside.app/Contents/Helpers/portside; \
	if "$$cli" daemon status >/dev/null 2>&1; then "$$cli" daemon restart; fi; \
	if [ $$running = 1 ]; then open /Applications/Portside.app; fi
