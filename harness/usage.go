package harness

// Usage is a running tally of LLM and tool consumption.
// Cost is optional; adapters that cannot compute cost leave it at 0.
type Usage struct {
	InputTokens  int
	OutputTokens int
	Cost         float64 // USD; 0 means unknown
}

// Add returns the sum of two Usage values. It does not mutate either receiver.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		Cost:         u.Cost + other.Cost,
	}
}
