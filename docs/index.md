# Gantry

**A tiny, testable, Go-native agent runtime for teams that want control, conformance, and no framework lock-ins.**

Gantry gives you a phase-based agent loop with onion-style (`net/http`-style)
middleware at every phase — a small, dependency-free foundation for prototyping
and shipping LLM agents in Go.

[![CI](https://github.com/farazhassan/gantry/actions/workflows/ci.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/ci.yml)
[![CodeQL](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/farazhassan/gantry.svg)](https://pkg.go.dev/github.com/farazhassan/gantry)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-00ADD8)](https://go.dev/dl/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](https://github.com/farazhassan/gantry/blob/main/LICENSE)
[![Release](https://img.shields.io/github/v/release/farazhassan/gantry?sort=semver)](https://github.com/farazhassan/gantry/releases/latest)

!!! info "Status: Beta"
    The core loop and component contracts are in place, but the public API may
    still change ahead of a v1.0 release.

## Why Gantry

- **Control.** Every stage of the loop is an onion of middleware you compose
  with ordinary Go — retries, caching, timing, short-circuiting, and state
  mutation are all just middleware. No DSL, no hidden control flow; you decide
  what runs and when.
- **Conformance.** Every component is defined by a contract, and Gantry ships a
  reusable **conformance** test suite so your own implementations can prove they
  honor that contract.
- **Testability.** Units are plain `func(ctx, *State) error` handlers; a mock
  LLM client and a black-box **eval** harness (configs × cases × scorers) let
  you test agents like normal Go code, with no API keys.
- **No lock-in.** The agent core depends only on the Go standard library. You
  supply one interface — `LLMClient` — and wire in Anthropic, OpenAI, a local
  model, or a mock. Adapters and components are opt-in, never load-bearing.

## A taste

```sh
go get github.com/farazhassan/gantry
```

```go
// excerpt — see Getting started for the full program
llm := openrouter.New("deepseek/deepseek-v4-flash")

agent, _ := gantry.NewAgent(gantry.WithLLM(llm))

agent.Use(gantry.PhaseLLMCall, timing) // middleware on any phase

state, _ := agent.Run(ctx, "Say hello in one sentence.")
fmt.Println(state.FinalOutput)
```

[Get started](getting-started/installing.md){ .md-button .md-button--primary }
[Browse examples](getting-started/examples.md){ .md-button }

## Learn more

- **[Guide](guide/sessions.md)** — sessions and long-running tasks.
- **[API reference](https://pkg.go.dev/github.com/farazhassan/gantry)** — every exported type and function.
- **[Reference](resources/reference.md)** — components, conformance suites, and the eval harness.
- **[Roadmap](resources/roadmap.md)** — what's planned, and where contributions help most.

## Contributing

Bug fixes, new components, LLM adapters, examples, and docs are all welcome.
See [CONTRIBUTING.md](https://github.com/farazhassan/gantry/blob/main/CONTRIBUTING.md).

## License

Gantry is released under the
[MIT License](https://github.com/farazhassan/gantry/blob/main/LICENSE).
