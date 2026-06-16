package eval

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/farazhassan/gantry"
)

// ExactMatchScorer compares RunResult.FinalOutput to a string stored at
// Case.Metadata[ExpectedKey].
type ExactMatchScorer struct {
	ScorerName  string // optional; defaults to "exact_match"
	ExpectedKey string
}

func (s ExactMatchScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "exact_match"
}

func (s ExactMatchScorer) Score(_ context.Context, c Case, r RunResult) (Score, error) {
	v, ok := c.Metadata[s.ExpectedKey]
	if !ok {
		return Score{}, fmt.Errorf("eval: case %q missing metadata %q", c.ID, s.ExpectedKey)
	}
	want, ok := v.(string)
	if !ok {
		return Score{}, fmt.Errorf("eval: case %q metadata %q not a string (%T)", c.ID, s.ExpectedKey, v)
	}
	pass := r.FinalOutput == want
	val := 0.0
	if pass {
		val = 1.0
	}
	return Score{Name: s.Name(), Value: val, Pass: pass}, nil
}

// RegexScorer passes when Pattern matches FinalOutput.
type RegexScorer struct {
	ScorerName string
	Pattern    string
}

func (s RegexScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "regex"
}

func (s RegexScorer) Score(_ context.Context, _ Case, r RunResult) (Score, error) {
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return Score{}, err
	}
	pass := re.MatchString(r.FinalOutput)
	val := 0.0
	if pass {
		val = 1.0
	}
	return Score{Name: s.Name(), Value: val, Pass: pass}, nil
}

// ContainsScorer passes when every Required substring appears in FinalOutput.
type ContainsScorer struct {
	ScorerName string
	Required   []string
}

func (s ContainsScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "contains"
}

func (s ContainsScorer) Score(_ context.Context, _ Case, r RunResult) (Score, error) {
	for _, want := range s.Required {
		if !strings.Contains(r.FinalOutput, want) {
			return Score{Name: s.Name(), Value: 0, Pass: false, Notes: "missing: " + want}, nil
		}
	}
	return Score{Name: s.Name(), Value: 1.0, Pass: true}, nil
}

// TraceScorer applies a user predicate to RunResult.Trace.
type TraceScorer struct {
	ScorerName string
	Pred       func(*gantry.Trace) bool
}

func (s TraceScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "trace"
}

func (s TraceScorer) Score(_ context.Context, _ Case, r RunResult) (Score, error) {
	if r.Trace == nil {
		return Score{Name: s.Name(), Pass: false, Notes: "no trace"}, nil
	}
	pass := s.Pred(r.Trace)
	val := 0.0
	if pass {
		val = 1.0
	}
	return Score{Name: s.Name(), Value: val, Pass: pass}, nil
}

// UsageScorer passes when MaxTokens and MaxCost are both honored.
// A zero value disables that bound.
type UsageScorer struct {
	ScorerName string
	MaxTokens  int
	MaxCost    float64
}

func (s UsageScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "usage"
}

func (s UsageScorer) Score(_ context.Context, _ Case, r RunResult) (Score, error) {
	tokens := r.Usage.InputTokens + r.Usage.OutputTokens
	if s.MaxTokens > 0 && tokens > s.MaxTokens {
		return Score{Name: s.Name(), Pass: false, Notes: fmt.Sprintf("tokens %d > %d", tokens, s.MaxTokens)}, nil
	}
	if s.MaxCost > 0 && r.Usage.Cost > s.MaxCost {
		return Score{Name: s.Name(), Pass: false, Notes: fmt.Sprintf("cost %.4f > %.4f", r.Usage.Cost, s.MaxCost)}, nil
	}
	return Score{Name: s.Name(), Pass: true, Value: 1.0}, nil
}

// LatencyScorer passes when RunResult.Duration <= Max.
type LatencyScorer struct {
	ScorerName string
	Max        time.Duration
}

func (s LatencyScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "latency"
}

func (s LatencyScorer) Score(_ context.Context, _ Case, r RunResult) (Score, error) {
	pass := r.Duration <= s.Max
	val := 0.0
	if pass {
		val = 1.0
	}
	return Score{Name: s.Name(), Value: val, Pass: pass}, nil
}

// LLMJudgeScorer uses an LLMClient to evaluate the output against a rubric.
// The judge LLM is prompted with the rubric as system, and the case input +
// final output as the user message. The reply is parsed for "VERDICT: PASS"
// (case-insensitive) to set Pass=true; everything else is Pass=false.
type LLMJudgeScorer struct {
	ScorerName string
	Client     gantry.LLMClient
	Rubric     string
}

func (s LLMJudgeScorer) Name() string {
	if s.ScorerName != "" {
		return s.ScorerName
	}
	return "llm_judge"
}

func (s LLMJudgeScorer) Score(ctx context.Context, c Case, r RunResult) (Score, error) {
	prompt := fmt.Sprintf("Case input:\n%s\n\nAgent output:\n%s", c.Input, r.FinalOutput)
	resp, err := s.Client.Generate(ctx, gantry.LLMRequest{
		System:   s.Rubric,
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: prompt}},
	})
	if err != nil {
		return Score{}, err
	}
	pass := strings.Contains(strings.ToUpper(resp.Content), "VERDICT: PASS") ||
		strings.Contains(strings.ToUpper(resp.Content), "VERDICT:PASS")
	val := 0.0
	if pass {
		val = 1.0
	}
	return Score{Name: s.Name(), Value: val, Pass: pass, Notes: resp.Content}, nil
}
