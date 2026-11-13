VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: build ui go install dev-daemon dev-open dev-ui check lint hooks notices clean snapshot app app-install

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
	PORTSIDE_DEV_ORIGIN=5173 ./bin/portside daemon

## dev-open: sign the browser in to the dev daemon; the session also works for Vite at the same host
dev-open:
	PORTSIDE_CONFIG=$(DEV_DIR)/portside.toml PORTSIDE_STATE_DIR=$(DEV_DIR)/state ./bin/portside open
	@echo "Vite: http://$$(cat $(DEV_DIR)/state/dashboard-host):5173"

## dev-ui: Vite with hot reload, proxying /api to the daemon
dev-ui:
	pnpm --dir web dev

## check: vet, the linters, the tests with the race detector, the dashboard build and the license notices
check: lint
	go vet ./...
	go test -race ./...
	pnpm --dir web build
	go run ./scripts/notices -check
	go run ./scripts/tuithemes -check

# The linters come from tools/go.mod, built with this module's toolchain.
LINT_BIN := $(CURDIR)/bin/golangci-lint
CHECK_BIN := $(CURDIR)/bin/lintcheck
# noctx and contextcheck report only code changed since LINT_BASE.
LINT_BASE ?= $(shell git merge-base HEAD origin/main 2>/dev/null || echo HEAD)
# LINT_PKGS narrows the run to some packages and LINT_WARN=0 leaves out the
# warnings (the pre-commit hook sets both).
LINT_PKGS ?= ./...
LINT_WARN ?= 1

$(LINT_BIN): tools/go.mod tools/go.sum
	cd tools && GOOS= GOARCH= go build -o $(LINT_BIN) github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(CHECK_BIN): tools/go.mod tools/go.sum $(wildcard tools/lintcheck/*.go)
	cd tools && GOOS= GOARCH= go build -o $(CHECK_BIN) ./lintcheck

## lint: go vet with the project's analyzers, then golangci-lint (.golangci.yml); funlen and gocognit only warn
lint: $(LINT_BIN) $(CHECK_BIN)
	go vet -vettool=$(CHECK_BIN) $(LINT_PKGS)
	$(LINT_BIN) run -D noctx,contextcheck $(LINT_PKGS)
	$(LINT_BIN) run --enable-only noctx,contextcheck --new-from-rev=$(LINT_BASE) $(LINT_PKGS)
	@if [ "$(LINT_WARN)" != 0 ]; then $(LINT_BIN) run --enable-only funlen,gocognit --issues-exit-code=0 $(LINT_PKGS); fi

## hooks: run .githooks/pre-commit before every commit in this clone
hooks:
	git config core.hooksPath .githooks

## notices: regenerate THIRD_PARTY_NOTICES.txt from the Go modules, web/node_modules and theme credits
notices:
	go run ./scripts/notices

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
