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

## Notes

- Documents store their text under the `content` payload key, matching the
  retriever's default `textKey`.
- `embedDim` (1536) is set for `text-embedding-3-small`. Change it (and the
  models) if you point at a different embeddings model.
