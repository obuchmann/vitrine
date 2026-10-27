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

The scripts need no root or administrator rights. They install to `~/.local/bin` or `%LOCALAPPDATA%\Programs\vitrine` and check the download against `checksums.txt`. With Go installed, `go install github.com/obuchmann/vitrine/cmd/vitrine@latest` works too.

<details><summary>Verify a release with cosign</summary>

The checksums are signed in the release workflow, without a long-lived key:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/obuchmann/vitrine/\.github/workflows/release\.yml@refs/tags/v'
sha256sum --ignore-missing -c checksums.txt
```
</details>

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

`vitrine mcp` starts vitrine in the background on first use. All sessions share it, and it exits after 30 minutes without requests. Prototypes and unread events stay on disk. Add `--no-open` if published prototypes should not open in your browser.

To keep vitrine running instead, for example so a prototype stays reachable while no agent runs, install it as a service for your user. No administrator rights are needed:

```sh
vitrine service install     # systemd --user, a launchd agent, or a Task Scheduler task at logon
vitrine service status
vitrine service uninstall
```

On WSL, the service needs systemd (`[boot] systemd=true` in `/etc/wsl.conf`). Without it, `vitrine mcp` works as before.

## What your agent gets

| Tool | Purpose |
|---|---|
| `publish_prototype` | Publish an HTML file by `path`. vitrine reads the file, so the HTML does not pass through the agent's context. Names are `<repo>/<name>`; the repo defaults to the git repository that contains the file. |
| `wait_for_events` | Return unacknowledged events, or wait for the next one. Events stay unread until the agent passes the `cursor` back as `ack`, which acknowledges only events it received. |
| `list_prototypes`, `get_prototype` | Find where a previous session left off. |
| `list_events` | Every stored event, read or not. |
| `send_to_prototype` | Show a message in every tab that shows the prototype right now. |
| `extend_prototype`, `delete_prototype` | Keep a prototype longer than 3 days without activity, or remove it. |

To keep working while people click, an agent can wait in the background with curl. The token file holds the headers, so the token is never on the command line. vitrine tells the agent the exact command for its platform:

```sh
curl -s -H "@$HOME/.local/state/vitrine/auth" 'http://127.0.0.1:8720/api/events/next?name=my-app/login&types=choice&ack=0'
```

## In a prototype

vitrine injects a small script, `hub.js`. The prototype decides what is worth reporting:

```html
<html data-track="view,click">  <!-- optional automatic events -->
<button onclick="hub.emit('choice', { variant: 'B' })">I'll take B</button>
```

## Troubleshooting

`vitrine doctor` checks the platform, the state directory and the token file permissions, whether vitrine or another program uses the port, the service and how the browser is opened. Problems are marked with `!`. Please include its output in bug reports.

## Principles

- **The repository is the source.** vitrine is a display case, not an archive. Prototypes live in git, and decisions are written back there.
- **The inbox never loses an event.** Events stay unread until an agent acknowledges them explicitly and are stored on disk, so they survive restarts and a new session picks up exactly where the last one stopped.
- **Safe by default.** vitrine binds to localhost and every agent call needs a token. A prototype can only write to its own inbox. You never handle the token yourself.
- **Every platform is first class.** Linux, Windows, WSL2 and macOS are tested in CI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues as described in [SECURITY.md](SECURITY.md), not in public issues.

## License

[Apache-2.0](LICENSE)
