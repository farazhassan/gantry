# Qdrant vector retriever example

Semantic retrieval backed by a [Qdrant](https://qdrant.tech) vector store.
Unlike the in-process examples, this one needs two external services.

## Prerequisites

1. **Qdrant** — run locally with Docker:

   ```bash
   docker run -p 6333:6333 qdrant/qdrant
   ```

2. **Embeddings + chat** — an OpenAI-compatible endpoint:

   ```bash
   export OPENAI_API_KEY=sk-...
   # optional overrides:
   # export QDRANT_URL=http://localhost:6333
   # export QDRANT_API_KEY=...
   ```

## Run

```bash
# 1. Create the collection and ingest seed documents:
go run ./examples/qdrant -ingest

# 2. Ask a question; context is retrieved from Qdrant:
go run ./examples/qdrant
```

### With Ollama (no API key required)

[Ollama](https://ollama.com) exposes an OpenAI-compatible API on
`http://localhost:11434`, so the same example works against it — no
`OPENAI_API_KEY` needed. Pull an embedding model and a chat model first:

```bash
ollama pull nomic-embed-text   # embeddings (768 dims)
ollama pull qwen2.5:3b         # chat
```

Then point the example at Ollama by supplying the env vars inline on each
command. `EMBED_DIM` **must** match the embedding model — `nomic-embed-text` is
768, not OpenAI's 1536:

```bash
# 1. Ingest:
QDRANT_URL=http://localhost:6333 \
QDRANT_API_KEY=your-qdrant-api-key \
LLM_BASE_URL=http://localhost:11434 \
EMBED_MODEL=nomic-embed-text \
EMBED_DIM=768 \
CHAT_MODEL=qwen2.5:3b \
  go run ./examples/qdrant -ingest

# 2. Query:
QDRANT_URL=http://localhost:6333 \
QDRANT_API_KEY=your-qdrant-api-key \
LLM_BASE_URL=http://localhost:11434 \
EMBED_MODEL=nomic-embed-text \
EMBED_DIM=768 \
CHAT_MODEL=qwen2.5:3b \
  go run ./examples/qdrant
```

`QDRANT_URL` defaults to `http://localhost:6333`, and `QDRANT_API_KEY` is only
needed for a secured instance — drop both lines when running a local, unsecured
Qdrant.

`LLM_BASE_URL` is the API root **without** the `/v1` suffix — the adapter appends
`/v1/embeddings` and `/v1/chat/completions` itself. Any other OpenAI-compatible
server (vLLM, LM Studio, a proxy) works the same way.

### Against a live / remote Qdrant

Supply `QDRANT_URL` (and `QDRANT_API_KEY` for a secured instance) inline on the
command — they override the `http://localhost:6333` default:

```bash
# Ingest into a remote Qdrant:
QDRANT_URL=https://your-qdrant.example.com:6333 \
QDRANT_API_KEY=your-qdrant-api-key \
  go run ./examples/qdrant -ingest

# Query against it:
QDRANT_URL=https://your-qdrant.example.com:6333 \
QDRANT_API_KEY=your-qdrant-api-key \
  go run ./examples/qdrant
```

For Qdrant Cloud, use your cluster endpoint as `QDRANT_URL` and the cluster API
key as `QDRANT_API_KEY`. Drop `QDRANT_API_KEY` for an unsecured instance.

## Notes

- Documents store their text under the `content` payload key, matching the
  retriever's default `textKey`.
- The embedding dimension defaults to 1536 (`text-embedding-3-small`). Override
  it with `EMBED_DIM` — along with `EMBED_MODEL`/`CHAT_MODEL` — when pointing at
  a different embeddings model. It must match the model's true vector size.
