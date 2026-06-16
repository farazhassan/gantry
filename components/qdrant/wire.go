package qdrant

// Mirrors the subset of Qdrant's REST API this package uses. Private: callers
// see Point and Hit only.

type createCollectionRequest struct {
	Vectors vectorParams `json:"vectors"`
}

type vectorParams struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

type upsertRequest struct {
	Points []wirePoint `json:"points"`
}

type wirePoint struct {
	ID      uint64         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

type searchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	WithPayload bool      `json:"with_payload"`
}

type searchResponse struct {
	Result []wireHit `json:"result"`
}

type wireHit struct {
	ID      uint64         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}
