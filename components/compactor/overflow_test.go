package compactor_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

// recordingCompactor keeps the last keep messages (the last forceKeep when the
// budget is forced and forceKeep > 0) and records every Budget it saw. err, if
// set, is returned from forced compactions.
type recordingCompactor struct {
	keep      int
	forceKeep int
	err       error
	budgets   []compactor.Budget
}

func (r *recordingCompactor) Compact(_ context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	r.budgets = append(r.budgets, b)
	keep := r.keep
	if b.Force {
		if r.err != nil {
			return nil, r.err
		}
		if r.forceKeep > 0 {
			keep = r.forceKeep
		}
	}
	start := 0
	if len(msgs) > keep {
		start = len(msgs) - keep
	}
	out := make([]gantry.Message, len(msgs)-start)
	copy(out, msgs[start:])
	return out, nil
}

func preload(t *testing.T, a *gantry.Agent, n int) {
	t.Helper()
	err := a.UseNamed(gantry.PhaseAssembleContext, "preload", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if len(s.Messages) < n {
				s.Messages = nil
				for i := 0; i < n; i++ {
					s.Messages = append(s.Messages, gantry.Message{Role: gantry.RoleUser, Content: "msg"})
				}
			}
			return next(ctx, s)
		}
	})
	if err != nil {
		t.Fatalf("preload: %v", err)
	}
}

func overflow(limit int) error {
	return &gantry.ContextLengthError{Limit: limit, Requested: limit + 1, Err: errors.New("too long")}
}

func TestOverflowRetriesOnceAfterForcedCompaction(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 6)
	rc := &recordingCompactor{keep: 100, forceKeep: 2} // assemble pass keeps all 6
	if err := a.With(compactor.New(rc, compactor.Budget{SoftLimit: 50})); err != nil {
		t.Fatalf("With: %v", err)
	}

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Requests()); got != 2 {
		t.Fatalf("LLM calls = %d, want 2", got)
	}
	if got := len(mock.Requests()[1].Messages); got != 2 {
		t.Errorf("retried request has %d messages, want 2 (compacted)", got)
	}
	last := rc.budgets[len(rc.budgets)-1]
	if !last.Force || last.MaxTokens != 300 || last.SoftLimit != 50 {
		t.Errorf("retry budget = %+v, want Force, MaxTokens 300, SoftLimit 50", last)
	}
	if got, _ := s.Meta[compactor.MetaOverflowRetries].(int); got != 1 {
		t.Errorf("Meta[%s] = %v, want 1", compactor.MetaOverflowRetries, s.Meta[compactor.MetaOverflowRetries])
	}
}

func TestOverflowTargetFallsBackToContextWindow(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: &gantry.ContextLengthError{Err: errors.New("too long")}}, // no Limit
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithContextWindow(800))
	preload(t, a, 3)
	rc := &recordingCompactor{keep: 100, forceKeep: 1}
	_ = a.With(compactor.New(rc, compactor.Budget{}))

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 800 {
		t.Errorf("retry MaxTokens = %d, want 800 (ContextWindow)", got)
	}
}

func TestOverflowTargetFallsBackToEstimate(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: &gantry.ContextLengthError{Err: errors.New("too long")}},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 4)
	rc := &recordingCompactor{keep: 100, forceKeep: 1}
	_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No anchor, no system/tools: estimate = 4×100 = 400; 75% = 300.
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 300 {
		t.Errorf("retry MaxTokens = %d, want 300 (75%% of estimate)", got)
	}
}

func TestOverflowTwiceReturnsTypedError(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Err: overflow(300)},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 3)
	_ = a.With(compactor.New(&recordingCompactor{keep: 100, forceKeep: 1}, compactor.Budget{}))

	_, err := a.Run(context.Background(), "")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Fatalf("Run err = %v, want ErrContextLengthExceeded", err)
	}
	if got := len(mock.Requests()); got != 2 {
		t.Errorf("LLM calls = %d, want 2 (one retry only)", got)
	}
}

func TestNonOverflowErrorIsNotRetried(t *testing.T) {
	boom := errors.New("boom")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: boom}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.With(compactor.New(&recordingCompactor{keep: 100}, compactor.Budget{}))

	_, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want boom", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("LLM calls = %d, want 1", got)
	}
}

func TestAssembleResetsAnchorWhenCompactionShrinks(t *testing.T) {
	var seen []gantry.ContextUsage
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 5)
	_ = a.UseNamed(gantry.PhaseAssembleContext, "seed-anchor", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.ContextUsage = gantry.ContextUsage{PromptTokens: 900, MessageCount: 4}
			return next(ctx, s)
		}
	})
	_ = a.With(compactor.New(&recordingCompactor{keep: 2}, compactor.Budget{}))
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			seen = append(seen, s.ContextUsage)
			return next(ctx, s)
		}
	})

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen[0] != (gantry.ContextUsage{}) {
		t.Errorf("anchor before LLM call = %+v, want reset after shrinking compaction", seen[0])
	}
}

func setSystem(a *gantry.Agent, n int) {
	_ = a.UseNamed(gantry.PhaseAssembleContext, "set-system", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.System = strings.Repeat("x", n)
			return next(ctx, s)
		}
	})
}

func TestOverflowTargetSubtractsSystemAndTools(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{{300, 200}, {50, 1}} {
		mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
			{Err: overflow(tc.limit)},
			{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
		})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))
		preload(t, a, 4)
		setSystem(a, 400) // 100 tokens
		rc := &recordingCompactor{keep: 100, forceKeep: 1}
		_ = a.With(compactor.New(rc, compactor.Budget{}))
		if _, err := a.Run(context.Background(), ""); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != tc.want {
			t.Errorf("limit %d: MaxTokens = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestOverflowNotRetriedWhenCompactionDoesNotShrink(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: overflow(300)}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 3)
	_ = a.With(compactor.New(&recordingCompactor{keep: 100}, compactor.Budget{}))

	s, err := a.Run(context.Background(), "")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Fatalf("Run err = %v, want ErrContextLengthExceeded", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("LLM calls = %d, want 1", got)
	}
	if _, ok := s.Meta[compactor.MetaOverflowRetries]; ok {
		t.Errorf("Meta[%s] set, want absent", compactor.MetaOverflowRetries)
	}
}

func TestOverflowCompactionErrorIsJoined(t *testing.T) {
	cerr := errors.New("compact failed")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: overflow(300)}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 3)
	_ = a.With(compactor.New(&recordingCompactor{keep: 100, err: cerr}, compactor.Budget{}))

	_, err := a.Run(context.Background(), "")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) || !errors.Is(err, cerr) {
		t.Fatalf("Run err = %v, want both ErrContextLengthExceeded and compaction error", err)
	}
}

func TestOverflowOnWrapUpTurnDoesNotLeakPrompt(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(300)}, // wrap-up turn overflows
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	_ = a.With(compactor.New(&recordingCompactor{keep: 100, forceKeep: 2}, compactor.Budget{}))

	s, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q, want final", s.FinalOutput)
	}
	for _, m := range s.Messages {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("wrap-up prompt leaked into transcript: %+v", s.Messages)
		}
	}
}

// headOnlyOnForce returns msgs unchanged normally, and only the first message
// when forced.
type headOnlyOnForce struct{}

func (headOnlyOnForce) Compact(_ context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	if b.Force && len(msgs) > 1 {
		return append([]gantry.Message(nil), msgs[:1]...), nil
	}
	return append([]gantry.Message(nil), msgs...), nil
}

func TestOverflowOnWrapUpTurnKeepsGenuineUserMessageWithSameContent(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(300)}, // wrap-up turn overflows; forced compaction drops the injected prompt
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	seeded := false
	_ = a.UseNamed(gantry.PhaseAssembleContext, "seed-genuine", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if !seeded {
				seeded = true
				s.Messages = append([]gantry.Message{{Role: gantry.RoleUser, Content: gantry.MaxIterationsWrapUpPrompt}}, s.Messages...)
			}
			return next(ctx, s)
		}
	})
	_ = a.With(compactor.New(headOnlyOnForce{}, compactor.Budget{}))

	s, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, m := range s.Messages {
		if m.Role == gantry.RoleUser && m.Content == gantry.MaxIterationsWrapUpPrompt {
			found = true
		}
	}
	if !found {
		t.Errorf("genuine user message with wrap-up prompt content was removed: %+v", s.Messages)
	}
}

// rewriteCompactor returns a same-length copy whose first message is replaced
// by a summary, as a Summarizing compactor does when head+tail+1 == len(msgs).
type rewriteCompactor struct{}

func (rewriteCompactor) Compact(_ context.Context, msgs []gantry.Message, _ compactor.Budget) ([]gantry.Message, error) {
	out := append([]gantry.Message(nil), msgs...)
	if len(out) > 0 {
		out[0] = gantry.Message{Role: gantry.RoleUser, Content: "summary"}
	}
	return out, nil
}

func TestAssembleResetsAnchorOnSameLengthRewrite(t *testing.T) {
	var seen []gantry.ContextUsage
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 5)
	_ = a.UseNamed(gantry.PhaseAssembleContext, "seed-anchor", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.ContextUsage = gantry.ContextUsage{PromptTokens: 900, MessageCount: 4}
			return next(ctx, s)
		}
	})
	_ = a.With(compactor.New(rewriteCompactor{}, compactor.Budget{}))
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			seen = append(seen, s.ContextUsage)
			return next(ctx, s)
		}
	})
	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen[0] != (gantry.ContextUsage{}) {
		t.Errorf("anchor before LLM call = %+v, want reset after same-length rewrite", seen[0])
	}
}

// forcedRewrite leaves the transcript alone normally and rewrites one message
// (same length) when forced.
type forcedRewrite struct{}

func (forcedRewrite) Compact(ctx context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	if b.Force {
		return rewriteCompactor{}.Compact(ctx, msgs, b)
	}
	return append([]gantry.Message(nil), msgs...), nil
}

func TestOverflowRetriesOnSameLengthRewrite(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 3)
	_ = a.With(compactor.New(forcedRewrite{}, compactor.Budget{}))

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Requests()); got != 2 {
		t.Errorf("LLM calls = %d, want 2", got)
	}
	if n, _ := s.Meta[compactor.MetaOverflowRetries].(int); n != 1 {
		t.Errorf("Meta[%s] = %d, want 1", compactor.MetaOverflowRetries, n)
	}
}

// rebuildCompactor rebuilds every message field-by-field (dropping any
// unexported state) and, under Force, keeps only the head. It records every
// message it was given.
type rebuildCompactor struct{ seen []gantry.Message }

func (r *rebuildCompactor) Compact(_ context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	r.seen = append(r.seen, msgs...)
	if b.Force && len(msgs) > 1 {
		msgs = msgs[:1]
	}
	out := make([]gantry.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, gantry.Message{Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, Name: m.Name})
	}
	return out, nil
}

func TestWrapUpPromptNeverReachesCompactor(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(300)}, // wrap-up turn overflows
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	rc := &rebuildCompactor{}
	_ = a.With(compactor.New(rc, compactor.Budget{}))

	s, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q, want final", s.FinalOutput)
	}
	if len(rc.seen) == 0 {
		t.Fatal("compactor never ran")
	}
	for _, m := range rc.seen {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("compactor received the injected wrap-up prompt: %+v", m)
		}
	}
	for _, m := range s.Messages {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("wrap-up prompt leaked into transcript: %+v", s.Messages)
		}
	}
}

func TestWrapUpPromptHeldOutWhenNotLast(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(300)}, // wrap-up turn overflows
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	// Installed before the compactor, so it runs inside the overflow retry and
	// appends after the wrap-up prompt before the call fails.
	appended := false
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if n := len(s.Messages); !appended && n > 0 && gantry.IsWrapUpPrompt(s.Messages[n-1]) {
				appended = true
				s.Messages = append(s.Messages, gantry.Message{Role: gantry.RoleUser, Content: "late"})
			}
			return next(ctx, s)
		}
	})
	rc := &rebuildCompactor{}
	_ = a.With(compactor.New(rc, compactor.Budget{}))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range rc.seen {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("compactor received the injected wrap-up prompt: %+v", m)
		}
	}
	reqs := mock.Requests()
	retry := reqs[len(reqs)-1].Messages
	if n := len(retry); n < 2 || retry[n-2].Content != gantry.MaxIterationsWrapUpPrompt || retry[n-1].Content != "late" {
		t.Errorf("retried request tail = %+v, want [wrap-up prompt, late]", retry)
	}
}

func TestOverflowFallbackTargetExcludesFixedTokens(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: &gantry.ContextLengthError{Err: errors.New("too long")}}, // no Limit, no window
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 2)
	_ = a.UseNamed(gantry.PhaseAssembleContext, "big-system", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.System = strings.Repeat("x", 3200) // 800 tokens
			return next(ctx, s)
		}
	})
	rc := &recordingCompactor{keep: 100, forceKeep: 1}
	_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Messages are 2×100 = 200 of a 1000-token prompt; the messages-only budget
	// must be 75% of the message tokens, not 75% of the whole prompt (750).
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 150 {
		t.Errorf("retry MaxTokens = %d, want 150", got)
	}
}

func TestWrapUpRetryBudgetExcludesHeldOutSuffix(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(1000)}, // wrap-up turn overflows
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	rc := &recordingCompactor{keep: 100, forceKeep: 1}
	_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No system/tools: limit 1000 minus the held-out wrap-up prompt (100).
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 900 {
		t.Errorf("retry MaxTokens = %d, want 900", got)
	}
}

func TestOverflowRetryRerunsMiddlewareInstalledAfterCompactor(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 4)
	_ = a.With(compactor.New(&recordingCompactor{keep: 100, forceKeep: 1}, compactor.Budget{}))
	// Installed after the compactor, like the documented guardrail order: it
	// must re-check the compacted input on the retry.
	var checked []int
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			checked = append(checked, len(s.Messages))
			return next(ctx, s)
		}
	})
	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(checked) != 2 || checked[1] != 1 {
		t.Errorf("guard saw message counts %v, want [4 1] (re-checked after compaction)", checked)
	}
}

func TestInstallFailsAtomicallyWhenOverflowHandlerTaken(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 4)
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { return false, nil })
	if err := a.With(compactor.New(compactor.NewSlidingWindow(1), compactor.Budget{})); err == nil {
		t.Fatal("With: want error when an overflow handler is already registered")
	}
	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Requests()[0].Messages); got != 4 {
		t.Errorf("LLM saw %d messages, want 4: failed install must not leave compaction middleware behind", got)
	}
}

func TestOverflowRetryCountResetsEachRun(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.With(compactor.New(compactor.NewSlidingWindow(10), compactor.Budget{}))
	prior := gantry.NewState("hi")
	prior.Messages = []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}, {Role: gantry.RoleAssistant, Content: "ok"}}
	prior.Meta[compactor.MetaOverflowRetries] = 1
	prior.Done = true

	s, err := a.RunFrom(context.Background(), prior, "next")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if v, ok := s.Meta[compactor.MetaOverflowRetries]; ok {
		t.Errorf("Meta[%s] = %v on a turn with no overflow, want absent", compactor.MetaOverflowRetries, v)
	}
}
