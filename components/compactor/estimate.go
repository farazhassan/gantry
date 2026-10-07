package compactor

import "github.com/farazhassan/gantry"

// EstimatePromptTokens returns the prompt size the next LLM call will send.
//
// When s.ContextUsage holds a valid anchor (0 < MessageCount <= len(Messages))
// it returns the provider-measured PromptTokens plus b.Count for each message
// after the measured request — Messages[MessageCount:], starting with the
// assistant reply (estimated from its visible content, since the provider's
// output tokens can include hidden reasoning). Otherwise it estimates the
// whole prompt:
// System, Tools (name, description, schema) and every message.
func EstimatePromptTokens(s *gantry.State, b Budget) int {
	cu := s.ContextUsage
	if cu.MessageCount > 0 && cu.MessageCount <= len(s.Messages) {
		total := cu.PromptTokens
		for i := cu.MessageCount; i < len(s.Messages); i++ {
			total += b.Count(s.Messages[i])
		}
		return total
	}
	total := estimateFixedTokens(s)
	for _, m := range s.Messages {
		total += b.Count(m)
	}
	return total
}

// estimateFixedTokens estimates the tokens of the prompt parts compaction
// cannot shrink: System and Tools (name, description, schema).
func estimateFixedTokens(s *gantry.State) int {
	total := bytesToTokens(len(s.System))
	for _, t := range s.Tools {
		total += bytesToTokens(len(t.Name) + len(t.Description) + len(t.Schema))
	}
	return total
}
