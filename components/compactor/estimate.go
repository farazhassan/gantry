package compactor

import (
	"math"

	"github.com/farazhassan/gantry"
)

// EstimatePromptTokens returns the prompt size the next LLM call will send.
//
// When s.ContextUsage holds a valid anchor (PromptTokens > 0 and
// 0 <= MessageCount <= len(Messages); MessageCount may be 0 for a request that sent
// no transcript messages)
// it returns the provider-measured PromptTokens plus b.Count for each message
// after the measured request — Messages[MessageCount:], starting with the
// assistant reply (estimated from its visible content, since the provider's
// output tokens can include hidden reasoning). Otherwise it estimates the
// whole prompt:
// System, Tools (name, description, schema) and every message.
func EstimatePromptTokens(s *gantry.State, b Budget) int {
	cu := s.ContextUsage
	if cu.PromptTokens > 0 && cu.MessageCount >= 0 && cu.MessageCount <= len(s.Messages) {
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

// Calibration bounds: the measured/estimated ratio is clamped so one bad
// measurement cannot distort targets wildly.
const (
	minCalibration = 0.5
	maxCalibration = 2.0
)

// calibration returns how many provider tokens one estimated token is worth:
// the measured (or estimated) prompt size over its raw estimate, clamped to
// [minCalibration, maxCalibration]; 1 when either is unknown.
func calibration(prompt, raw int) float64 {
	if prompt <= 0 || raw <= 0 {
		return 1
	}
	return min(max(float64(prompt)/float64(raw), minCalibration), maxCalibration)
}

// calibrate converts n estimated tokens to provider tokens, rounding up.
func calibrate(n int, ratio float64) int { return int(math.Ceil(float64(n) * ratio)) }
