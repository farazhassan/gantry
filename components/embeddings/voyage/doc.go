// Package voyage implements embeddings.Embeddings against the Voyage AI
// /v1/embeddings endpoint. Voyage has no Go SDK and its wire format matches the
// OpenAI embeddings shape, so this adapter is standard library only. A
// configurable base URL supports proxies and test servers.
package voyage
