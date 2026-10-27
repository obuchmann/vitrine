# AGENTS.md

Guidance for AI coding agents working on this repository. Human contributors should read [CONTRIBUTING.md](CONTRIBUTING.md) first; everything there applies to agents too.

## Ground rules

- Read the ADRs in [`docs/adr/`](docs/adr/) before changing behaviour. If a change contradicts an ADR, propose a new ADR instead of silently diverging.
- **The inbox never loses an event.** Events are only marked as read by an explicit `ack`. Tests in the store and end-to-end tests guard this. Never weaken them.
- **Security model:** [ADR 0002](docs/adr/0002-authentication.md). Never add CORS headers, cookie auth, or unauthenticated agent endpoints.
- **Every platform is first class:** [ADR 0004](docs/adr/0004-platforms-and-distribution.md). Use `filepath` instead of hard-coded separators. Platform-specific code goes in `_windows.go`, `_unix.go` or `_darwin.go` files with tests.
- MCP tool descriptions and `instructions` are the agents' user manual. Keep them in sync with the README.

## Workflow

- Conventional Commits, small focused PRs.
- Run `go test -race ./...` before proposing changes.
