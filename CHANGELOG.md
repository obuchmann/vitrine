# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `vitrine service install | uninstall | status`: runs `vitrine serve` at every login as a systemd `--user` unit, a launchd agent or a Task Scheduler task, without administrator rights.
- `vitrine doctor`: reports platform and WSL, state directory, token file permissions, what answers on the port (and whether it is another version), the service and the browser opener.
- `publish_prototype` warns once per session if the running vitrine is another version than `vitrine mcp`.
- On WSL, prototypes open with `explorer.exe` if `wslview` is missing.
- `vitrine mcp`: MCP server on stdio. It starts vitrine in the background when nothing answers on `127.0.0.1:8720`, and that daemon exits after 30 minutes without requests (`vitrine serve -idle-exit`). The agent token stays in this process.
- MCP tools `publish_prototype` (reads the file by `path`; the name defaults to `<git repo>/<folder>`), `wait_for_events`, `list_prototypes`, `get_prototype`, `list_events`, `send_to_prototype`, `extend_prototype` and `delete_prototype`.
- The first publish of a prototype opens it in the browser (`xdg-open`, `open`, `rundll32`, or `wslview` on WSL). `--no-open` turns this off.
- `POST /api/p/<repo>/<name>/message` sends a message to the open tabs of a prototype.
- `vitrine serve`: serves prototypes on `127.0.0.1:8720` and `[::1]:8720` with `hub.js` injected, and provides the HTTP API for agents (publish, list, extend, delete, events, long-poll `next`, SSE stream).
- Storage: prototypes, inbox and acknowledgements are kept as files in the per-user state directory and survive restarts.
- Agent token: created on first start in the state directory and usable as a curl header file (`curl -H @<state dir>/auth`). Every page gets an event token for its own inbox. Requests must name an allowed Host and carry `X-Vitrine`, and vitrine sends no CORS headers.
- vitrine refuses to listen beyond loopback without `VITRINE_TOKEN` and `-public-url`.
- CI on Linux, Windows and macOS.
