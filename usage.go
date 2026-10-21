package gantry

// Usage is a running tally of LLM and tool consumption.
//
// InputTokens is the total prompt size, including any tokens served from or
// written to a provider's prompt cache — adapters normalize to this so it
// means "prompt size" on every provider. CacheReadTokens and CacheWriteTokens
// are subsets of InputTokens. Cost is optional; adapters that cannot compute
// cost leave it at 0.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int     // subset of InputTokens served from cache
	CacheWriteTokens int     // subset of InputTokens written to cache
	Cost             float64 // USD; 0 means unknown
}

// Add returns the sum of two Usage values. It does not mutate either receiver.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + other.InputTokens,
		OutputTokens:     u.OutputTokens + other.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + other.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + other.CacheWriteTokens,
		Cost:             u.Cost + other.Cost,
	}
}
