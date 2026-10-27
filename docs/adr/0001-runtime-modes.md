# 0001: Runtime modes and single instance

- Status: accepted
- Date: 2026-10-27

## Context

Agents start MCP servers as stdio child processes, one per session. Developers often run several sessions at once (parallel agents, subagents, different tools on the same repository). They expect a prototype and its inbox to outlive the session that published it. At the same time, nothing should need to be set up or left running.

## Decision

One binary with these commands:

| Command | Purpose |
|---|---|
| `vitrine mcp` | stdio MCP server. This is what agents are configured with. |
| `vitrine serve` | Runs the HTTP server in the foreground. |
| `vitrine service install \| uninstall \| status` | Registers `serve` as a per-user background service: systemd `--user`, launchd, or a Windows Task Scheduler task at logon. |
| `vitrine doctor` | Prints platform, state directory, token file and permissions, port, daemon status, browser opener and WSL detection. |

**Single instance per user.** `vitrine mcp` probes `GET http://127.0.0.1:8720/api/health`.

- If a vitrine answers, `vitrine mcp` attaches to it as a thin proxy.
- If not, it starts a detached daemon (`serve`), which exits after an idle period of about 30 minutes.
- Whoever binds the port wins a race. There are no lock files.
- All sessions share one inbox.

**Agent configuration is the same in both modes:** always `vitrine mcp`. MCP over HTTP with a token header is documented as an alternative.

**Publishing reads files locally.** `publish_prototype` accepts a `path`. The stdio process reads the file relative to the agent's working directory, so HTML never passes through the agent's context, and the daemon needs no access to the repository.

**Naming:** prototypes are called `<repo>/<name>`. `repo` defaults to the name of the git root that contains the published file, and otherwise to that of the agent's working directory. Agents often work in one directory and publish a file from another repository.

**Browser:** opens automatically on the first publish. `--no-open` disables this. Without a desktop session, vitrine only prints the URL.

**No separate client CLI.** MCP covers publishing. Background waiting uses `curl` against the HTTP API (see [ADR 0002](0002-authentication.md)).

## Consequences

- Zero setup for the default path. The service is optional.
- The idle-exit daemon needs durable storage so that no events are lost. See [ADR 0003](0003-storage.md).
- Port 8720 is the rendezvous point. If it is taken by something else, `vitrine doctor` must say so clearly.

## Alternatives considered

- **One server per stdio session on a random port.** Rejected. Inboxes would be split per session, and prototypes would die with the session.
- **A `vitrine push` / `vitrine wait` client CLI.** Rejected for now. `publish {path}` and curl cover the need. It can be added later if curl with a token proves too cumbersome.
