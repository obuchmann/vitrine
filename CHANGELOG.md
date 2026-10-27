# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `vitrine serve`: serves prototypes on `127.0.0.1:8720` and `[::1]:8720` with `hub.js` injected, and provides the HTTP API for agents (publish, list, extend, delete, events, long-poll `next`, SSE stream).
- Storage: prototypes, inbox and acknowledgements are kept as files in the per-user state directory and survive restarts.
- Agent token: created on first start in the state directory and usable as a curl header file (`curl -H @<state dir>/auth`). Every page gets an event token for its own inbox. Requests must name an allowed Host and carry `X-Vitrine`, and vitrine sends no CORS headers.
- vitrine refuses to listen beyond loopback without `VITRINE_TOKEN` and `-public-url`.
- CI on Linux, Windows and macOS.
