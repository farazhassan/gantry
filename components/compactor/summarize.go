package compactor

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry/harness"
)

// Summarizing keeps head messages and tail messages, replacing the dropped
// middle with an LLM-generated summary.
type Summarizing struct {
	client     harness.LLMClient
	head, tail int
}

// NewSummarizing returns a Summarizing compactor. It panics unless head >= 0
// and tail >= 0: negative counts would panic inside Compact. head and tail may
// both be zero, in which case the entire history is replaced by a single
// summary.
func NewSummarizing(client harness.LLMClient, head, tail int) *Summarizing {
	if head < 0 || tail < 0 {
		panic(fmt.Sprintf("compactor: NewSummarizing requires head >= 0 and tail >= 0, got head=%d tail=%d", head, tail))
	}
	return &Summarizing{client: client, head: head, tail: tail}
}

func (s *Summarizing) Compact(ctx context.Context, msgs []harness.Message, b Budget) ([]harness.Message, error) {
	// Count tokens; if under SoftLimit, no compaction needed.
	if b.SoftLimit > 0 {
		total := 0
		for _, m := range msgs {
			total += b.Count(m)
		}
		if total <= b.SoftLimit {
			out := make([]harness.Message, len(msgs))
			copy(out, msgs)
			return out, nil
		}
	}
	if len(msgs) <= s.head+s.tail {
		out := make([]harness.Message, len(msgs))
		copy(out, msgs)
		return out, nil
	}

	headSlice := msgs[:s.head]
	tailSlice := msgs[len(msgs)-s.tail:]
	middle := msgs[s.head : len(msgs)-s.tail]

	// Build a summarization request.
	var b2 strings.Builder
	b2.WriteString("Summarize the following messages concisely, preserving key facts and decisions.\n\n")
	for _, m := range middle {
		b2.WriteString(string(m.Role))
		b2.WriteString(": ")
		b2.WriteString(m.Content)
		b2.WriteString("\n")
	}
	req := harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: b2.String()}},
	}
	resp, err := s.client.Generate(ctx, req)
	if err != nil {
		return nil, err
	}

	summary := harness.Message{
		Role:    harness.RoleSystem,
		Content: resp.Content,
	}
	out := make([]harness.Message, 0, len(headSlice)+1+len(tailSlice))
	out = append(out, headSlice...)
	out = append(out, summary)
	out = append(out, tailSlice...)
	return out, nil
}
