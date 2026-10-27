# 0002: Authentication on localhost

- Status: accepted
- Date: 2026-10-27

## Context

Events end up in an agent's context, so whoever can write into an inbox can inject prompts. On a developer machine, the threats are:

- **Websites in the user's browser.** Any open page can send requests to `127.0.0.1:8720`. A multipart form POST is a "simple request" and needs no CORS preflight.
- **DNS rebinding.** A malicious domain can resolve to `127.0.0.1`.
- **Other OS users on the same machine.** Shared dev servers and Windows terminal servers are examples. All users share loopback.

## Decision

**Variant A: an invisible agent token, with header and Host checks as a second line.**

- **Agent token.** vitrine generates a random token on first start and stores it in the state directory:
  - Linux: `$XDG_STATE_HOME/vitrine/auth`, mode `0600`
  - Windows: `%LOCALAPPDATA%\vitrine\auth`, protected by the profile ACL
  - macOS: `~/Library/Application Support/vitrine/auth`
- **What the token covers.** It is required for every agent operation: publish, read, acknowledge, extend and delete, as well as MCP over HTTP. It is accepted only as `Authorization: Bearer`, never from cookies.
- **Agents never see it.** `vitrine mcp` reads it itself. For background waiting, agents use `curl -H @<auth file> …` (`curl.exe` in PowerShell), and the file holds the full header line. The MCP instructions give the exact path for the platform.
- **Event token per prototype.** It is embedded in the served page. It permits only `POST /api/events/<this prototype>` with a JSON body. It stays stable across versions so that open tabs keep working.
- **Second line of defence.** Every API request must carry `X-Vitrine: 1`, and the `Host` header must be `localhost`, `127.0.0.1` or `[::1]` with the configured port. A cross-origin request carrying `X-Vitrine` triggers a CORS preflight, and vitrine never approves one.
- **No CORS.** vitrine never sends `Access-Control-Allow-*` headers.
- **Non-loopback binds.** vitrine refuses to start on a non-loopback address without an explicitly configured token.

## Consequences

- The developer never handles a token, and local and remote use follow one model.
- **Accepted gap:** anyone who can load a prototype page can read its event token and write into that prototype's inbox. On a single-user machine, that means other prototypes on the same instance. An optional separate prototype origin (`--proto-host`) is planned for teams.
- No sandboxing of prototypes. `localStorage` and the rest of the platform remain available to them.
- No per-session tokens. The inbox belongs to the prototype, and on a single machine the OS user is the real boundary.

## Alternatives considered

- **B: no token, only the `X-Vitrine` header and the Host check.** Rejected. Every other OS user on a multi-user machine could read, acknowledge and inject.
- **C: the agent API only on a Unix domain socket.** Rejected. `curl.exe --unix-socket` on Windows is unverified, some agent sandboxes block sockets, and remote use needs a token anyway.
- **D: no agent HTTP API, MCP only.** Rejected. It allows no background waiting, and the shared daemon would still need a protected internal channel.
- **Sandboxed iframe with an opaque origin for prototypes.** Rejected. It costs `localStorage` and adds complexity, while the token already protects the API.
