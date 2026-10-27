# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report them privately through [GitHub Security Advisories](https://github.com/obuchmann/vitrine/security/advisories/new). We aim to acknowledge reports within 7 days and to agree on a disclosure timeline with you.

## Supported versions

vitrine is pre-1.0. Only the latest release receives security fixes.

## Scope

The events vitrine collects go straight into an AI agent's context. The following are therefore in scope, among others:

- Any way for a website, another local user, or a prototype to write events into an inbox it does not own (prompt injection).
- Reading, acknowledging or deleting events or prototypes without the agent token.
- Bypassing the localhost-only default or the Host and Origin checks (DNS rebinding, CSRF).
- Leaking the agent token, for example through file permissions or ACLs on the state directory.

Known and accepted limitations are documented in [ADR 0002](docs/adr/0002-authentication.md).
