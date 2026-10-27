# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-27

First release: vitrine on your own machine, on Linux, Windows (including WSL2) and macOS.

### Added

- `vitrine mcp`: the MCP server agents are configured with, on stdio.
  - It starts vitrine in the background when nothing answers on `127.0.0.1:8720`. That daemon is shared by all sessions and exits after 30 minutes without requests.
  - The agent token stays in this process; agents never see it.
- MCP tools:
  - `publish_prototype` reads the HTML file by `path`, so it does not pass through the agent's context. The name defaults to `<git repo of the file>/<folder>`, and the first publish opens the page in the browser (`--no-open` turns this off).
  - `wait_for_events`, `list_prototypes`, `get_prototype`, `list_events`, `send_to_prototype`, `extend_prototype` and `delete_prototype`.
- Inbox: events stay unread until an agent acknowledges them with the cursor, which covers only events it received; events a type filter left out stay unread. Prototypes, events and acknowledgements are stored as files in the per-user state directory and survive restarts.
- `hub.js` is injected into every prototype: `hub.emit`, automatic `view`, `click` and `input` events with `data-track`, messages from the agent, and reloading on a new version. At most four visible tabs keep a live connection, so the browser's limit of six connections per host never blocks pages or events.
- `vitrine serve` runs the server in the foreground. It provides an HTTP API for agents, including a long poll for background waiting with `curl -H @<state dir>/auth`.
- `vitrine service install | uninstall | status`: runs vitrine at every login as a systemd `--user` unit, a launchd agent or a Task Scheduler task, without administrator rights.
- `vitrine doctor` checks the platform and WSL, the `vitrine` binaries on PATH, the state directory and token file permissions, what answers on the port and its version, the service and the browser opener.
- Security ([ADR 0002](docs/adr/0002-authentication.md)):
  - an agent token for every agent request
  - an event token per page that only writes into its own inbox
  - Host and `X-Vitrine` header checks
  - no CORS headers
  - a refusal to listen beyond loopback without `VITRINE_TOKEN` and `-public-url`
- Releases for Linux, macOS and Windows on amd64 and arm64, with SBOMs and checksums signed by cosign.
- `install.sh` (Linux, macOS, WSL) and `install.ps1` (Windows) install without root or administrator rights and verify the checksum. `go install github.com/obuchmann/vitrine/cmd/vitrine@latest` works too.

[Unreleased]: https://github.com/obuchmann/vitrine/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/obuchmann/vitrine/releases/tag/v0.1.0
