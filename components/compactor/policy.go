package compactor

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/farazhassan/gantry"
)

// MetaLastCompaction is the State.Meta key holding the *Report of the most
// recent compaction a Policy performed (triggered or forced). It is
// overwritten by each such compaction and absent until the first.
const MetaLastCompaction = "components/compactor:last_compaction"

const (
	defaultTrigger = 0.8
	defaultTarget  = 0.5
	minCalibration = 0.5
	maxCalibration = 2.0
)

// Policy configures NewPolicy. Trigger and Target are fractions of the
// context window (defaults 0.8 and 0.5): compaction starts once the prompt
// reaches Trigger and aims for Target, so it runs rarely and the prompt
// prefix stays stable (and cacheable) in between. TriggerTokens and
// TargetTokens are absolute values used only when the window is unknown; if
// they are unset too, the policy compacts only when forced by the overflow
// handler. Steps are tried in order, cheapest first, until the messages fit.
type Policy struct {
	Trigger, Target             float64
	TriggerTokens, TargetTokens int
	Steps                       []Compactor
}

// Report describes one compaction a Policy performed. Token values are
// calibrated estimates for Messages only.
type Report struct {
	Forced                                  bool
	BeforeTokens, AfterTokens, TargetTokens int
	Steps                                   []StepReport
}

// StepReport describes one step that ran. Name is the step's Name() if it
// has one, else its Go type.
type StepReport struct {
	Name                      string
	BeforeTokens, AfterTokens int
}

type policy struct{ p Policy }

// NewPolicy returns a Compactor that applies p. The Budget fields filled by
// New drive it: it triggers on Budget.PromptTokens (the provider-measured
// prompt size), scales Budget.Count by the measured/estimated ratio (clamped
// to [0.5, 2]) so steps work in provider tokens, and passes each step
// MaxTokens = target minus Budget.FixedTokens (under Force, at most
// Budget.MaxTokens). If the steps cannot reach the target the best result is
// returned without error. Tool results whose call was removed by a step are
// dropped, as are tool calls left without a result (and an assistant message
// left with neither calls nor content). It panics unless 0 < Target < Trigger <= 1 (after defaults), the
// token values are both unset or 0 < TargetTokens < TriggerTokens, and Steps
// is non-empty with no nil step.
func NewPolicy(p Policy) Compactor {
	if p.Trigger == 0 {
		p.Trigger = defaultTrigger
	}
	if p.Target == 0 {
		p.Target = defaultTarget
	}
	if !(0 < p.Target && p.Target < p.Trigger && p.Trigger <= 1) {
		panic(fmt.Sprintf("compactor: NewPolicy requires 0 < Target < Trigger <= 1, got Target=%v Trigger=%v", p.Target, p.Trigger))
	}
	if (p.TriggerTokens != 0 || p.TargetTokens != 0) && !(0 < p.TargetTokens && p.TargetTokens < p.TriggerTokens) {
		panic(fmt.Sprintf("compactor: NewPolicy requires 0 < TargetTokens < TriggerTokens, got TargetTokens=%d TriggerTokens=%d", p.TargetTokens, p.TriggerTokens))
	}
	if len(p.Steps) == 0 {
		panic("compactor: NewPolicy requires at least one step")
	}
	for i, s := range p.Steps {
		if s == nil {
			panic(fmt.Sprintf("compactor: NewPolicy step %d is nil", i))
		}
	}
	p.Steps = slices.Clone(p.Steps)
	return &policy{p: p}
}

// limits returns the trigger and target in tokens for the whole prompt, or
// zeros when neither the window nor token values are known.
func (pc *policy) limits(window int) (trigger, target int) {
	if window > 0 {
		return int(pc.p.Trigger * float64(window)), int(pc.p.Target * float64(window))
	}
	return pc.p.TriggerTokens, pc.p.TargetTokens
}

func (pc *policy) Compact(ctx context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	trigger, target := pc.limits(b.ContextWindow)
	if !b.Force && trigger == 0 {
		return cloneMessages(msgs), nil
	}
	raw := b.FixedTokens + totalTokens(msgs, b)
	size := b.PromptTokens
	if size <= 0 {
		size = raw
	}
	if !b.Force && size < trigger {
		return cloneMessages(msgs), nil
	}

	ratio := 1.0
	if raw > 0 {
		ratio = min(max(float64(size)/float64(raw), minCalibration), maxCalibration)
	}
	counter := func(m gantry.Message) int { return int(math.Ceil(float64(b.Count(m)) * ratio)) }
	est := Budget{Counter: counter}

	msgTarget := 0
	if target > 0 {
		msgTarget = target - b.FixedTokens
	}
	if b.Force && b.MaxTokens > 0 && (target == 0 || b.MaxTokens < msgTarget) {
		msgTarget = b.MaxTokens
	}
	if target > 0 || (b.Force && b.MaxTokens > 0) {
		msgTarget = max(msgTarget, 1)
	}

	rep := &Report{Forced: b.Force, BeforeTokens: totalTokens(msgs, est), TargetTokens: msgTarget}
	cur := cloneMessages(msgs)
	for i, step := range pc.p.Steps {
		before := totalTokens(cur, est)
		if msgTarget > 0 && before <= msgTarget {
			break
		}
		next, err := step.Compact(ctx, cur, Budget{MaxTokens: msgTarget, Force: b.Force, Counter: counter, ContextWindow: b.ContextWindow})
		if err != nil {
			return nil, fmt.Errorf("compactor: policy step %d (%s): %w", i, stepName(step), err)
		}
		cur = next
		rep.Steps = append(rep.Steps, StepReport{Name: stepName(step), BeforeTokens: before, AfterTokens: totalTokens(cur, est)})
	}
	cur = repairOrphans(cur)
	rep.AfterTokens = totalTokens(cur, est)
	if slot := reportSlotFrom(ctx); slot != nil {
		slot.r = rep
	}
	return cur, nil
}

// stepName is s.Name() when s has one, else its Go type.
func stepName(s Compactor) string {
	if n, ok := s.(interface{ Name() string }); ok {
		return n.Name()
	}
	return fmt.Sprintf("%T", s)
}

// repairOrphans drops tool results whose ToolCallID matches no tool call in
// an earlier message, and tool calls with no later result (providers reject
// both). An assistant message left with no calls and no content is dropped.
// msgs is owned by the caller and is filtered in place; ToolCalls slices may
// be shared with the caller's input, so they are rebuilt, never mutated.
func repairOrphans(msgs []gantry.Message) []gantry.Message {
	calls := map[string]bool{}
	out := msgs[:0]
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == gantry.RoleTool && m.ToolCallID != "" && !calls[m.ToolCallID] {
			continue
		}
		out = append(out, m)
	}

	answered := map[string]bool{}
	keep := make([]bool, len(out))
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		if m.Role == gantry.RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
		keep[i] = true
		if len(m.ToolCalls) == 0 {
			continue
		}
		var kept []gantry.ToolCall
		for _, tc := range m.ToolCalls {
			if answered[tc.ID] {
				kept = append(kept, tc)
			}
		}
		if len(kept) == len(m.ToolCalls) {
			continue
		}
		m.ToolCalls = kept
		out[i] = m
		keep[i] = len(kept) > 0 || m.Content != ""
	}
	n := 0
	for i, m := range out {
		if keep[i] {
			out[n] = m
			n++
		}
	}
	return out[:n]
}

// reportSlot receives the Report of a Policy compaction run under New.
type reportSlot struct{ r *Report }

type reportKey struct{}

func withReportSlot(ctx context.Context, slot *reportSlot) context.Context {
	return context.WithValue(ctx, reportKey{}, slot)
}

func reportSlotFrom(ctx context.Context) *reportSlot {
	slot, _ := ctx.Value(reportKey{}).(*reportSlot)
	return slot
}
