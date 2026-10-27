# Roadmap

This roadmap shows direction, not promises. Once issues exist, each item will link to its GitHub milestone. Design decisions are in [`docs/adr/`](docs/adr/).

## v0.1: your own machine

- [x] Zero-setup local mode: `vitrine mcp` over stdio, a shared daemon that exits when idle, browser opens automatically ([ADR 0001](docs/adr/0001-runtime-modes.md))
- [x] Local service: `vitrine service install` for systemd `--user`, launchd and Windows Task Scheduler
- [x] `vitrine doctor`
- [x] Invisible agent token, event token per prototype, Host and header checks ([ADR 0002](docs/adr/0002-authentication.md))
- [x] File storage in the state directory ([ADR 0003](docs/adr/0003-storage.md))
- [ ] Linux, Windows (incl. WSL2) and macOS with a CI matrix ([ADR 0004](docs/adr/0004-platforms-and-distribution.md))
- [x] Release archives, `install.sh` and `install.ps1`, `go install`
- [ ] Documented and tested with Claude Code, opencode, GitHub Copilot (VS Code) and oh my pi

## Later

- Shared and team servers: token distribution, namespace-scoped tokens, a separate prototype origin (`--proto-host`)
- More install channels: Homebrew tap, winget, Scoop, deb/rpm, npm wrapper, container image
- Share links for people outside your machine

## Not planned

- Long-term archive of prototypes or events. The repository is the source.
- Hosting vitrine as a public service.

Ideas and use cases are welcome as [feature requests](https://github.com/obuchmann/vitrine/issues/new/choose).
