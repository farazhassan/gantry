// Package openai implements embeddings.Embeddings against the OpenAI
// /v1/embeddings endpoint. A configurable base URL means the same adapter
// serves OpenAI, Ollama (/v1/embeddings compatibility), and other
// OpenAI-compatible providers. Standard library only — no SDK.
package openai
