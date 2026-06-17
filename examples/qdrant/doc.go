// Command qdrant demonstrates semantic retrieval backed by a Qdrant vector
// store. Run with -ingest to embed and upsert seed documents, then run without
// the flag to ask a question whose context is retrieved from Qdrant.
//
// Prerequisites (see README.md): a running Qdrant and an OpenAI-compatible
// embeddings endpoint.
package main
