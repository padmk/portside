# Homebrew tap

Homebrew casks and formulae for padmk's tools.

## Portside

```sh
brew install --cask padmk/tap/portside
```

[Portside](https://github.com/padmk/portside) keeps your local dev servers running, in one place: a menu bar app, a dashboard, a CLI and an MCP server for coding agents. It needs macOS 26 or later on Apple Silicon. Report problems in the [Portside repository](https://github.com/padmk/portside/issues).

To build the command-line tool and dashboard from source instead, without the menu bar app, use `brew install --formula padmk/tap/portside`. Install either the cask or the formula, not both.

Each project's release workflow updates its own files here (`Casks/portside.rb` and `Formula/portside.rb` for Portside); do not edit them by hand.
