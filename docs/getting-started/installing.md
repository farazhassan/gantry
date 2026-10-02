# Installing

Gantry is a Go module. Assuming you have [Go 1.24 or newer](https://go.dev/dl/)
installed, create a directory for your application and make it your working
directory:

```sh
mkdir myagent
cd myagent
```

Initialize a Go module, then add Gantry as a dependency:

```sh
go mod init myagent
go get github.com/farazhassan/gantry
```

Gantry's core depends only on the Go standard library. The built-in LLM
adapters and components live in the same module, so that one `go get` is all
you need to follow these guides.

## Choose an LLM provider

!!! warning "An LLM provider is required"
    Every Gantry agent needs an LLM client. Gantry ships adapters for
    **OpenRouter**, **Ollama**, **OpenAI**, and **Anthropic**, plus scripted
    mock clients (`eval.NewMockLLMClient`) for offline tests. Mocks are great for
    unit tests, but we recommend a **live LLM** while you learn — you'll see the
    agent loop behave the way it will in production.

These guides use [OpenRouter](https://openrouter.ai), which gives one API key
access to many models. Create a key in your OpenRouter account and export it:

```sh
export OPENROUTER_API_KEY=your-key-here
```

The OpenRouter adapter reads `OPENROUTER_API_KEY` from the environment
automatically.

**Next:** [Hello agent](hello-agent.md)
