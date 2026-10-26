# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub: open the [Security tab](https://github.com/padmk/portside/security) of `padmk/portside` and choose **Report a vulnerability** ([GitHub's guide](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)). Please do not open a public issue, pull request or discussion for a vulnerability.

Include the version (`portside version`), the operating system, what an attacker can do and the steps to reproduce it. Fixes are released as a new version with a GitHub security advisory, which credits the reporter unless they ask otherwise.

## Supported versions

Only the latest release receives security fixes. With the app, `brew upgrade --cask portside` also upgrades the running daemon. After `brew upgrade portside` (the formula) or a new release download, run `portside daemon restart`.

## Scope

Portside runs the commands in your config as your user, so a program that already runs as your user, or can edit the config, is outside the threat model. In scope, for example:

- reaching the daemon's API, dashboard, terminals or logs without the token or session cookie, including from web pages (DNS rebinding, cross-origin requests) or from other accounts on the same computer;
- reaching a shared daemon through Tailscale without a paired device's key, or a paired connection being impersonated or replayed;
- the `*.localhost` proxy, project icons or log replies leading to script running on the dashboard's origin;
- config, state or log files readable by other accounts;
- the release artifacts, the Homebrew cask and formula, the macOS app's install path and its in-place daemon upgrade.

[docs/security.md](docs/security.md) describes the protections in detail.
