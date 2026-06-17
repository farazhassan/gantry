package openai

// Mirrors OpenAI's /v1/embeddings wire format. Private: callers only ever see
// [][]float32.

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []embedDatum `json:"data"`
}

type embedDatum struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}
