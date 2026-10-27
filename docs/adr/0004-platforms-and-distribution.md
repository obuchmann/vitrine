# 0004: Platform support and distribution

- Status: accepted
- Date: 2026-10-27

## Context

Developers run agents on Linux, on Windows (natively with PowerShell or Git Bash, and inside WSL2) and on macOS. Each platform differs in the following ways:

- paths
- file permissions
- how background processes are detached
- how a browser is opened
- which `curl` is available

## Decision

**Linux, Windows (including WSL2) and macOS are first-class platforms.**

| Concern | Linux | Windows | macOS |
|---|---|---|---|
| State directory | `$XDG_STATE_HOME/vitrine` | `%LOCALAPPDATA%\vitrine` | `~/Library/Application Support/vitrine` |
| Protecting the token | mode `0600` | profile ACL (`0600` only sets read-only) | mode `0600` |
| Detaching the daemon | `setsid` | `DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP` | `setsid` |
| Background service | systemd `--user` | Task Scheduler, at logon | launchd agent |
| Opening the browser | `xdg-open` | `rundll32 url.dll,FileProtocolHandler` | `open` |

- **WSL2.** vitrine is installed inside WSL and opens the Windows browser via `wslview` or `explorer.exe`. The browser reaches `localhost:8720` through WSL localhost forwarding, which must be tested in both NAT and mirrored mode.
- **Bind address.** vitrine binds to `127.0.0.1` and `::1`. Windows Firewall does not prompt for loopback.
- **Platform-specific instructions.** MCP instructions and error messages show snippets for the current platform. PowerShell gets `curl.exe`, because `curl` is an alias there.
- **CI.** A GitHub Actions matrix runs on `ubuntu-latest`, `windows-latest` and `macos-latest`. End-to-end tests cover:
  - the attack cases from [ADR 0002](0002-authentication.md)
  - daemon start and attach
  - storage across restarts

**Distribution:**

- GitHub Release archives built with GoReleaser for linux, windows and darwin on amd64 and arm64, with checksums and an SBOM
- `install.sh` (Linux, macOS, WSL) and `install.ps1` (Windows), which download the release
- `go install github.com/obuchmann/vitrine/cmd/vitrine@latest`

Further channels (package managers, container image) are tracked in the [roadmap](../../ROADMAP.md).

## Consequences

- Every change to paths, processes or permissions needs a check on all three platforms. CONTRIBUTING asks for this, and `vitrine doctor` output is part of the bug report template.
- A manual test matrix covers the agents Claude Code, oh my pi, opencode and GitHub Copilot (VS Code), on Linux, Windows and WSL2.

## Alternatives considered

- **Linux first, Windows later.** Rejected. Paths, permissions and process handling shape the core design and are expensive to retrofit.
