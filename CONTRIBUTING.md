# Contributing to Gantry

Contributions are welcome! Bug fixes, new components, LLM adapters, examples, and
documentation improvements are all appreciated.

## Ways to contribute

- **Found a bug or have an idea?** Open an issue to discuss it first.
- **Building a component?** Implement the relevant interface, validate it against
  the matching [conformance suite](docs/reference.md#conformance), and add tests.
- **Improving docs?** Fixes and clarifications to the README, the
  [reference](docs/reference.md), or these guidelines are all welcome.
- **Picking up a roadmap item?** See the [roadmap](docs/roadmap.md) — it's a great
  place to start.

## Development setup

Gantry needs **Go 1.22+**. Clone the repository and work from its root. The
project is organized as three Go modules:

- the root module `github.com/farazhassan/gantry` (the core loop and components),
- `mcp/` (the MCP client),
- `examples/assistant/` (the personal-assistant example).

Common commands (run from the repo root):

```sh
go test ./...           # full suite
go test -race ./...     # with the race detector (what CI runs)
go vet ./...
gofmt -l .              # lists files needing formatting (empty = clean)
```

## Commit & PR conventions

- Write commit messages in the [Conventional Commits](https://www.conventionalcommits.org/)
  style (e.g. `feat:`, `fix:`, `docs:`).
- Before opening a PR, run `go vet ./...`, `go test -race ./...`, and
  `gofmt -l .` and make sure everything passes — that's what CI checks.
- Reference the related issue in your PR description.

## License

By contributing, you agree that your contributions will be licensed under the
project's [MIT License](LICENSE).
