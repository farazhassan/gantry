package gantry

// Document is a retrieved chunk produced by a Retriever.
type Document struct {
	ID       string
	Content  string
	Score    float64
	Metadata map[string]any
}
