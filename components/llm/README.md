# LLM Adapters

This directory holds adapters that connect external LLM providers to Gantry's
`gantry.StreamingLLMClient` interface. Each provider lives in its own package
(`ollama`, `openai`, `anthropic`, ...).

These are the conventions every adapter follows. Read this before adding a new
one — the goal is that all adapters look and behave the same way, so callers can
swap providers without surprises.

## Package layout

- **One package per provider:** `components/llm/<provider>`, with the package
  named after the provider (`package openai`). Because the packages are
  separate, importing one provider does **not** pull in the others' code.
- **File responsibilities:**
  - `<provider>.go` — the public `Client`, `New`, options, and transport
    (`Generate`, `GenerateStream`, HTTP plumbing).
  - `wire.go` — **private** request/response DTOs that mirror the provider's
    wire format, plus the mapping to and from `gantry` types. Callers only ever
    see `gantry` types; all provider-shaped structs stay unexported here so the
    transport code stays focused.
  - `doc.go` — the package doc comment.
  - `*_test.go` — see [Testing](#testing).

## Construction

- **Constructor signature:** `New(model string, opts ...Option) *Client` using
  the functional-options pattern.
- **Panic on programmer error, not runtime error.** `New` panics on an empty
  model and on a missing required credential. A missing model or API key is a
  wiring mistake, not a recoverable condition — fail loudly at construction
  rather than returning an error from every call. (This matches the constructor
  style elsewhere in the repo.)

## Options

- `WithBaseURL(url)` — point at a non-default endpoint (proxy, local server,
  compatible API). Trim a trailing slash so path joins stay clean.
- `WithHTTPClient(h)` — supply the `*http.Client` (for timeouts/transport, or to
  point tests at an `httptest` server). A nil client is ignored.
- `WithAPIKey(key)` — for providers that require auth. See below.
- Expose a `BaseURL()` accessor so tests and callers can confirm the resolved
  endpoint.

### API keys

For providers that need a key:

- Resolve from `WithAPIKey(key)` first, falling back to the provider's
  conventional environment variable (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, ...).
- `WithAPIKey("")` (empty) is ignored, so the env fallback still applies.
- Validate at `New`: panic if neither source supplied a key.

Providers that need no key (e.g. a local Ollama server) simply omit
`WithAPIKey` and the validation.

## Request mapping

- **Provider defaults are signalled by zero.** `LLMRequest.Temperature == 0` and
  `MaxTokens == 0` mean "use the provider default" — do not send an explicit `0`
  that would override the provider. If a provider *requires* a positive value
  (e.g. Anthropic's `max_tokens`), the adapter substitutes a sensible default
  only when the request leaves it at 0.
- **System prompt** goes wherever the provider expects it: a leading
  system-role message (Ollama, OpenAI) or a dedicated top-level field
  (Anthropic). Either way the caller just sets `LLMRequest.System`.
- **Keep mapping in one place.** A single `assembleResponse` helper is the only
  place stop-reason and tool-call mapping live, shared by the streaming and
  non-streaming paths.

## Tool calls

- The gantry links a tool result back to its call by `ToolCall.ID`, so every
  returned call **must** have a stable ID.
  - If the provider supplies per-call IDs (OpenAI `call_...`, Anthropic
    `toolu_...`), **preserve** them.
  - If the provider omits IDs (Ollama), **synthesize** stable index-based IDs:
    `call-0`, `call-1`, ...
- `ToolCall.Input` is raw JSON (`json.RawMessage`). Providers that carry
  arguments as a JSON-encoded *string* (OpenAI) forward that string as-is — it
  is already a serialized JSON object.
- When forwarding a prior assistant turn back to the provider, map
  `Message.ToolCalls` and tool results (`RoleTool` + `ToolCallID`) into whatever
  shape the provider expects (e.g. OpenAI `tool` messages keyed by
  `tool_call_id`; Anthropic `tool_result` blocks inside a user message, merging
  consecutive results).

## Streaming

Implement `GenerateStream` so that it:

- Invokes `yield` once per **non-empty text delta** as chunks arrive.
- Returns the **fully aggregated** `LLMResponse` on success, so callers that
  also want the whole response (transcript append, usage accounting) get it
  without re-assembling deltas.
- Emits a **terminal empty-delta chunk** carrying the final `StopReason` and
  `Usage`, for parity with the in-repo mock. (The default LLM handler ignores
  empty-delta chunks, so this is harmless.)
- If `yield` returns an error, **stops reading and returns that error as-is**,
  so callers can match it with `errors.Is`.
- Reassembles tool calls that stream in fragments (OpenAI argument deltas keyed
  by index; Anthropic `input_json_delta` fragments keyed by block index).
- Respects context cancellation while reading the stream.

## Stop-reason mapping

Normalize the provider's finish reason to a `gantry.StopReason`:

| Condition                                  | `gantry.StopReason`     |
| ------------------------------------------ | ------------------------ |
| Response contains tool calls               | `StopReasonToolUse`      |
| Truncated by token limit                   | `StopReasonMaxTokens`    |
| Normal completion (anything else)          | `StopReasonEnd`          |

Tool calls take precedence: if the response carries tool calls, report
`StopReasonToolUse` regardless of the provider's finish-reason string.

## Testing

- Drive the client with an `httptest.Server` whose handler returns canned
  provider responses; point the client at it with
  `WithHTTPClient(srv.Client())` + `WithBaseURL(srv.URL)`.
- Cover request-shape mapping, response mapping, tool calls, error status,
  streaming aggregation, yield-error propagation, and context cancellation.
- **Wire both conformance suites** from the `conformance` package, backed by an
  httptest server returning a valid reply:

  ```go
  func TestProviderConformsToLLMClient(t *testing.T) {
      conformance.LLMClientSuite(t, func() gantry.LLMClient {
          return newServerClient(t, conformanceHandler)
      })
  }

  func TestProviderConformsToStreamingLLMClient(t *testing.T) {
      conformance.StreamingLLMClientSuite(t, func() gantry.StreamingLLMClient {
          return newServerClient(t, conformanceHandler)
      })
  }
  ```

## Dependencies

Adapters use the **Go standard library only** — no provider SDKs, no third-party
HTTP or JSON libraries. This keeps Gantry's zero-dependency promise intact.
