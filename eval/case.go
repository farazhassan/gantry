package eval

// Case is one evaluation example.
type Case struct {
	ID       string         `json:"id"`
	Input    string         `json:"input"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
