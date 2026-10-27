# Contributing to vitrine

Thanks for your interest! vitrine is in early development, so the best contributions right now are **issues**: use cases, environments we haven't thought of, and feedback on the [architecture decisions](docs/adr/).

## Before you start

- For anything larger than a typo, **open an issue first** so we can agree on the approach.
- Significant design changes need an ADR in [`docs/adr/`](docs/adr/). Copy the [template](docs/adr/template.md).
- By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development

vitrine is written in Go and is a single binary with no runtime dependencies.

```sh
go test -race ./...
go run ./cmd/vitrine serve
```

### Platforms

Linux, Windows and macOS are all first class. CI runs on all three. If you touch any of the following, please say in the PR which platforms you tested on:

- file paths
- processes and daemons
- opening the browser
- permissions

`vitrine doctor` prints the details we need.

### Agents

Tool descriptions and MCP `instructions` are the user manual for agents. When behaviour changes, update them together with the README. Changes to waiting or listening (timeouts, background jobs) should be checked against at least Claude Code and one other MCP client.

## Pull requests

- Keep PRs focused. One logical change per PR.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and PR titles, for example `feat(store): add file backend` or `fix(web): reject cross-origin event posts`.
- Add tests. Changes to the inbox must keep the guarantee that no event is lost before an explicit `ack`.
- Update docs and the [CHANGELOG](CHANGELOG.md) (`Unreleased` section) for user-visible changes.

## Releases

Maintainers release from `main`:

1. Move the `Unreleased` entries in the CHANGELOG under the new version and merge that.
2. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`.
3. The release workflow tests, builds the archives with GoReleaser, adds SBOMs and the install scripts, and signs the checksums with cosign. It creates a **draft** release.
4. Check the draft, then publish it. `releases/latest`, which the install scripts use, only points at published releases.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
