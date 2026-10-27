# vitrine

**Show a prototype. Let a human click it. Get the result back into your agent.**

vitrine is a small MCP server that hosts HTML prototypes. It streams what people do in them (choices, comments, clicks) back to Claude Code, Codex, opencode, GitHub Copilot or any other MCP client.

> [!IMPORTANT]
> **Status: early development.** Nothing on this page is released yet. This README describes the planned v0.1.
> See the [roadmap](ROADMAP.md) and the design decisions in [`docs/adr/`](docs/adr/). Feedback is welcome in issues.

## Why

Coding agents are good at generating three variants of a UI. Asking a person which one they prefer is harder for them. vitrine closes that loop:

1. Your agent writes an HTML prototype.
2. vitrine serves it and opens your browser.
3. You click, choose, comment.
4. Your agent receives the events and carries on.

## Works where you work

v0.1 focuses on your own machine, on **Linux, Windows (incl. WSL2) and macOS**.

| Mode | What it is | You run |
|---|---|---|
| **Zero-setup local** | Your agent starts `vitrine mcp` over stdio. vitrine serves prototypes on `127.0.0.1` and opens the browser. | nothing |
| **Local service** | vitrine runs in the background. All agent sessions share one inbox. | `vitrine service install` |

Both modes use the same agent configuration. `vitrine mcp` attaches to a running service or starts its own. Shared and team servers are on the [roadmap](ROADMAP.md).

## Quick start (planned)

```sh
# Linux, macOS, WSL
curl -fsSL https://github.com/obuchmann/vitrine/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/obuchmann/vitrine/releases/latest/download/install.ps1 | iex
```

Register vitrine with your agent:

<details open><summary>Claude Code</summary>

```sh
claude mcp add --scope user vitrine -- vitrine mcp
```
</details>

<details><summary>opencode (<code>~/.config/opencode/opencode.jsonc</code>)</summary>

```json
{ "mcp": { "vitrine": { "type": "local", "command": ["vitrine", "mcp"], "enabled": true } } }
```
</details>

<details><summary>GitHub Copilot in VS Code (<code>.vscode/mcp.json</code> or user config)</summary>

```json
{ "servers": { "vitrine": { "type": "stdio", "command": "vitrine", "args": ["mcp"] } } }
```
</details>

<details><summary>oh my pi (<code>~/.omp/agent/mcp.json</code>)</summary>

```json
{ "mcpServers": { "vitrine": { "type": "stdio", "command": "vitrine", "args": ["mcp"], "timeout": 0 } } }
```
</details>

Then ask your agent:

> Build two variants of a login form as a vitrine prototype and ask me which one I prefer.

## In a prototype

vitrine injects a small script, `hub.js`. The prototype decides what is worth reporting:

```html
<html data-track="view,click">  <!-- optional automatic events -->
<button onclick="hub.emit('choice', { variant: 'B' })">I'll take B</button>
```

## Principles

- **The repository is the source.** vitrine is a display case, not an archive. Prototypes live in git, and decisions are written back there.
- **The inbox never loses an event.** Events stay unread until an agent acknowledges them explicitly and are stored on disk, so they survive restarts and a new session picks up exactly where the last one stopped.
- **Safe by default.** vitrine binds to localhost and every agent call needs a token. A prototype can only write to its own inbox. You never handle the token yourself.
- **Every platform is first class.** Linux, Windows, WSL2 and macOS are tested in CI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues as described in [SECURITY.md](SECURITY.md), not in public issues.

## License

[Apache-2.0](LICENSE)
