# Langfuse tracer smoke test

A wire-contract smoke test for the Langfuse tracer adapter
(`components/tracers/langfuse`).

## Why this exists

The adapter's unit tests point the client at an `httptest` server we control, so
they verify the DTOs *we* defined — they cannot prove that **real Langfuse**
accepts the batch envelope, event types, field names, timestamps, and Basic
auth the adapter produces. That wire contract is the one thing those tests
structurally can't catch.

This program closes that gap: it runs a real agent and ships the trace to a
live Langfuse instance. Sends are best-effort (the adapter logs delivery errors
rather than returning them), so the program inspects `FailedSends()` after the
final flush and exits non-zero if any batch was rejected — a contract or auth
mismatch can't hide behind a "flushed" message.

It uses a scripted mock LLM, so **no model provider key is required** — the only
credentials needed are Langfuse's.

## Run it

```bash
LANGFUSE_PUBLIC_KEY=pk-... LANGFUSE_SECRET_KEY=sk-... go run ./examples/langfuse
```

Environment variables:

| Variable              | Required | Default                        |
| --------------------- | -------- | ------------------------------ |
| `LANGFUSE_PUBLIC_KEY` | yes      | —                              |
| `LANGFUSE_SECRET_KEY` | yes      | —                              |
| `LANGFUSE_HOST`       | no       | `https://cloud.langfuse.com`   |

Set `LANGFUSE_HOST` if you run a self-hosted Langfuse.

## Reading the result

| Output                                                       | Meaning |
| ------------------------------------------------------------ | ------- |
| `ingestion failed (N batch send(s)) ...` (exit code 1), preceded by a `langfuse: ...` error log line (bad HTTP status, or a transport/request error) | **Delivery problem** — wrong credentials, a wire-contract mismatch (non-success status), or an unreachable/misconfigured host. Catching this is exactly what the smoke test is for. |
| `buffer dropped N events ...` (exit code 1)                  | Events overflowed the buffer before flush — not a contract issue; raise the batch/flush settings. |
| `flushed cleanly — open <host> ...` with `failed sends: 0`   | Batch accepted. |

On success, open your Langfuse project and find the most recent trace named
**`run`**. It should contain a nested span per agent phase (the gantry wraps
each run in a single run-level span, so one agent run = one Langfuse trace).

Because a custom tracer does not populate `state.Trace`, this program can't print
the exact trace id — locate it by recency in the UI.

## Hermetic test

`go test ./examples/langfuse/...` runs the wiring against the in-memory default
tracer — no credentials, no network. The live wire-contract check is the
`go run` above, not the test, so CI stays green without Langfuse access.
