package compactor

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

const (
	defaultSummaryMaxTokens = 1024
	// summaryMessageCap bounds each message in the summarizer prompt, so the
	// summarizer's own request cannot overflow.
	summaryMessageCap = 2000
	// summaryInputPreviewCap bounds each tool-call input preview.
	summaryInputPreviewCap = 200
)

const summaryInstruction = "Summarize the earlier part of this conversation so the summary can replace those messages. " +
	"Preserve facts, decisions, open tasks and what tools found. Be concise.\n\n"

type summarizeTurns struct {
	client    gantry.LLMClient
	keep      int
	maxTokens int
}

// SummarizeOption configures SummarizeTurns.
type SummarizeOption func(*summarizeTurns)

// WithSummaryMaxTokens caps the summary's length (LLMRequest.MaxTokens); the
// default is 1024. It panics if n < 1.
func WithSummaryMaxTokens(n int) SummarizeOption {
	if n < 1 {
		panic(fmt.Sprintf("compactor: WithSummaryMaxTokens requires n >= 1, got %d", n))
	}
	return func(s *summarizeTurns) { s.maxTokens = n }
}

// SummarizeTurns returns a step that replaces the fewest oldest turns (all
// but the newest keepTurns, never the preamble) needed to fit
// Budget.MaxTokens with one LLM-written RoleUser summary placed after the
// preamble; with MaxTokens 0 every older turn is summarized. An existing
// summary is fed into the new one and replaced, so there is at most one. Each
// message is capped at 2,000 bytes in the summarizer prompt. An LLM error is
// returned; an empty summary leaves the input unchanged. It panics if c is nil
// or keepTurns < 0.
func SummarizeTurns(c gantry.LLMClient, keepTurns int, opts ...SummarizeOption) Compactor {
	if c == nil {
		panic("compactor: SummarizeTurns requires a non-nil LLMClient")
	}
	if keepTurns < 0 {
		panic(fmt.Sprintf("compactor: SummarizeTurns requires keepTurns >= 0, got %d", keepTurns))
	}
	s := &summarizeTurns{client: c, keep: keepTurns, maxTokens: defaultSummaryMaxTokens}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (*summarizeTurns) Name() string { return "summarize_turns" }

func (s *summarizeTurns) Compact(ctx context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	pre, turns := segment(msgs)
	older := len(turns) - s.keep
	if older <= 0 {
		return cloneMessages(msgs), nil
	}
	total := totalTokens(msgs, b)
	removed, n := 0, 0
	if isSummary(msgs[turns[0].start]) {
		// A prior summary is always rolled into the new one.
		removed += totalTokens(msgs[turns[0].start:turns[0].end], b)
		n = 1
	}
	if n >= older {
		return cloneMessages(msgs), nil
	}
	for n < older {
		t := turns[n]
		removed += totalTokens(msgs[t.start:t.end], b)
		n++
		if b.MaxTokens > 0 && total-removed+s.maxTokens <= b.MaxTokens {
			break
		}
	}
	selected := msgs[turns[0].start:turns[n-1].end]

	resp, err := s.client.Generate(ctx, gantry.LLMRequest{
		Messages:  []gantry.Message{{Role: gantry.RoleUser, Content: summaryPrompt(selected)}},
		MaxTokens: s.maxTokens,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return cloneMessages(msgs), nil
	}
	rest := msgs[turns[n-1].end:]
	out := make([]gantry.Message, 0, pre.end+1+len(rest))
	out = append(out, msgs[pre.start:pre.end]...)
	out = append(out, gantry.Message{Role: gantry.RoleUser, Content: summaryPrefix + resp.Content})
	return append(out, rest...), nil
}

// summaryPrompt renders the messages to summarize, each capped, with tool
// calls as name(input preview).
func summaryPrompt(msgs []gantry.Message) string {
	var sb strings.Builder
	sb.WriteString(summaryInstruction)
	for _, m := range msgs {
		if isSummary(m) {
			sb.WriteString("previous summary: ")
			sb.WriteString(strings.TrimPrefix(m.Content, summaryPrefix))
			sb.WriteString("\n")
			continue
		}
		sb.WriteString(string(m.Role))
		sb.WriteString(": ")
		sb.WriteString(capBytes(m.Content, summaryMessageCap))
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&sb, " [call %s(%s)]", tc.Name, capBytes(string(tc.Input), summaryInputPreviewCap))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
