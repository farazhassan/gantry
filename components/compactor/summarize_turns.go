package compactor

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

const (
	defaultSummaryMaxTokens = 1024
	// minSummaryTokens is the smallest summary length requested when the
	// budget leaves less room.
	minSummaryTokens = 32
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
// default is 1024. When Budget.MaxTokens leaves less room, a shorter summary
// is requested (at least 32 tokens). It panics if n < 1.
func WithSummaryMaxTokens(n int) SummarizeOption {
	if n < 1 {
		panic(fmt.Sprintf("compactor: WithSummaryMaxTokens requires n >= 1, got %d", n))
	}
	return func(s *summarizeTurns) { s.maxTokens = n }
}

// SummarizeTurns returns a step that replaces the fewest oldest turns (all
// but the newest keepTurns, never the preamble) needed to fit
// Budget.MaxTokens with one LLM-written RoleUser summary placed after the
// preamble; with MaxTokens 0 every older turn is summarized, and if the
// messages already fit MaxTokens the LLM is not called. Any existing summary
// among the older turns is fed into the new one and replaced, so there is at
// most one; with no other older turn to add, nothing changes. Each
// message is capped at 2,000 bytes in the summarizer prompt. An LLM error is
// returned; an empty summary, or one that would not shrink the messages,
// leaves the input unchanged. It panics if c is nil
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
	if b.MaxTokens > 0 && total <= b.MaxTokens {
		return cloneMessages(msgs), nil
	}
	// A prior summary among the candidates is always rolled into the new one,
	// so the selection extends at least past the last one.
	lastSummary := -1
	for i := range older {
		if isSummary(msgs[turns[i].start]) {
			lastSummary = i
		}
	}
	// The inserted summary costs its generated text plus the prefix and
	// per-message framing.
	wrapper := b.Count(gantry.Message{Role: gantry.RoleUser, Content: summaryPrefix})
	reserve := s.maxTokens + wrapper
	removed, n, plain := 0, 0, 0
	for n < older {
		t := turns[n]
		removed += totalTokens(msgs[t.start:t.end], b)
		if !isSummary(msgs[t.start]) {
			plain++
		}
		n++
		if n > lastSummary && plain > 0 && b.MaxTokens > 0 && total-removed+reserve <= b.MaxTokens {
			break
		}
	}
	if plain == 0 {
		return cloneMessages(msgs), nil
	}
	selected := msgs[turns[0].start:turns[n-1].end]
	// Never ask for a longer summary than the budget has room for: a summary
	// is kept by every later step, so an oversized one could not be undone.
	outMax := s.maxTokens
	if b.MaxTokens > 0 {
		if room := b.MaxTokens - (total - removed) - wrapper; room < outMax {
			outMax = max(room, minSummaryTokens)
		}
	}

	resp, err := s.client.Generate(ctx, gantry.LLMRequest{
		Messages:  []gantry.Message{{Role: gantry.RoleUser, Content: summaryPrompt(selected)}},
		MaxTokens: outMax,
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
	out = append(out, gantry.WithTag(gantry.Message{Role: gantry.RoleUser, Content: summaryPrefix + resp.Content}, summaryTag))
	out = append(out, rest...)
	if totalTokens(out, b) >= total {
		// A summary no smaller than what it replaces would grow the prompt,
		// and later steps never drop a summary: keep the turns instead.
		return cloneMessages(msgs), nil
	}
	return out, nil
}

// summaryPrompt renders the messages to summarize, with tool calls as
// name(input preview). Each rendered message, content and call previews
// together, is capped at summaryMessageCap bytes.
func summaryPrompt(msgs []gantry.Message) string {
	var sb strings.Builder
	sb.WriteString(summaryInstruction)
	for _, m := range msgs {
		var line strings.Builder
		if isSummary(m) {
			line.WriteString("previous summary: ")
			line.WriteString(strings.TrimPrefix(m.Content, summaryPrefix))
		} else {
			line.WriteString(string(m.Role))
			line.WriteString(": ")
			line.WriteString(m.Content)
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&line, " [call %s(%s)]", tc.Name, capBytes(string(tc.Input), summaryInputPreviewCap))
			}
		}
		sb.WriteString(capBytes(line.String(), summaryMessageCap))
		sb.WriteString("\n")
	}
	return sb.String()
}
