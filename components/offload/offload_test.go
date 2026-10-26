package offload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

type scopeKey struct{}

func scopeFromCtx(ctx context.Context) string { s, _ := ctx.Value(scopeKey{}).(string); return s }

func withScope(s string) context.Context {
	return context.WithValue(context.Background(), scopeKey{}, s)
}

func newComp(store Store) *component {
	return New(store, Options{Scope: scopeFromCtx}).(*component)
}

func history(result string, assistantsAfter int) []gantry.Message {
	msgs := []gantry.Message{
		{Role: gantry.RoleUser, Content: "q"},
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "get_metric"}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: result},
	}
	for i := 0; i < assistantsAfter; i++ {
		msgs = append(msgs, gantry.Message{Role: gantry.RoleAssistant, Content: "ok"})
	}
	return msgs
}

func TestShrinksALargeResultOnceAnAssistantFollowsIt(t *testing.T) {
	store := NewMemStore()
	c := newComp(store)
	big := bigMetric(400)
	s := &gantry.State{Messages: history(big, 1), ContextUsage: gantry.ContextUsage{PromptTokens: 9}}
	c.shrink(withScope("chat1"), s)

	got := s.Messages[2].Content
	meta, ok := previewMeta(got)
	if !ok {
		t.Fatalf("result not shrunk: %.80s", got)
	}
	full, found, _ := store.Get(context.Background(), "chat1:"+meta.Ref)
	if !found || full != big {
		t.Errorf("store lacks the full result under %s", meta.Ref)
	}
	if idx, found, _ := store.Get(context.Background(), "chat1:src:mtr_abc"); !found || !strings.Contains(idx, meta.Ref) {
		t.Errorf("src index = %q, %v", idx, found)
	}
	if s.ContextUsage != (gantry.ContextUsage{}) {
		t.Error("ContextUsage not reset after Messages changed")
	}
	if s.Meta[MetaPreviews] != 1 || s.Meta[MetaSavedChars] != len(big)-len(got) {
		t.Errorf("meta = %v", s.Meta)
	}
}

func TestLeavesUnseenSmallErroredAndPreviewResultsAlone(t *testing.T) {
	c := newComp(NewMemStore())
	big := bigMetric(400)
	cases := map[string][]gantry.Message{
		"unseen": history(big, 0),
		"small":  history(`{"src":"mtr_s","value":1}`, 3),
	}
	errored := history(big, 1)
	errored[2].IsError = true
	cases["errored"] = errored
	for name, msgs := range cases {
		s := &gantry.State{Messages: msgs, ContextUsage: gantry.ContextUsage{PromptTokens: 9}}
		before := msgs[2].Content
		c.shrink(withScope("chat1"), s)
		if s.Messages[2].Content != before {
			t.Errorf("%s: content changed", name)
		}
		if s.ContextUsage.PromptTokens != 9 {
			t.Errorf("%s: ContextUsage reset although nothing changed", name)
		}
	}
	// An existing preview is not shrunk again.
	s := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), s)
	once := s.Messages[2].Content
	s.Messages = append(s.Messages, gantry.Message{Role: gantry.RoleAssistant})
	c.shrink(withScope("chat1"), s)
	if s.Messages[2].Content != once {
		t.Error("preview shrunk twice")
	}
}

func TestNoScopeMeansNoShrink(t *testing.T) {
	c := newComp(NewMemStore())
	s := &gantry.State{Messages: history(bigMetric(400), 2)}
	c.shrink(context.Background(), s)
	if _, ok := previewMeta(s.Messages[2].Content); ok {
		t.Error("shrunk without a scope")
	}
}

type failingStore struct{ Store }

func (failingStore) Put(context.Context, string, string) error { return errors.New("down") }

func TestStoreFailureKeepsTheFullResultAndReports(t *testing.T) {
	var reported error
	c := New(failingStore{NewMemStore()}, Options{Scope: scopeFromCtx, OnError: func(_ context.Context, err error) { reported = err }}).(*component)
	big := bigMetric(400)
	s := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), s)
	if s.Messages[2].Content != big {
		t.Error("result shrunk although the store write failed")
	}
	if reported == nil {
		t.Error("OnError not called")
	}
}

func TestShrinkDoesNotMutateTheCallersBackingArray(t *testing.T) {
	c := newComp(NewMemStore())
	msgs := history(bigMetric(400), 1)
	orig := msgs[2].Content
	s := &gantry.State{Messages: msgs}
	c.shrink(withScope("chat1"), s)
	if msgs[2].Content != orig {
		t.Error("shrink wrote through to the original slice")
	}
}

func TestRebuiltHistoryGetsTheSamePreview(t *testing.T) {
	c := newComp(NewMemStore())
	big := bigMetric(400)
	a := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), a)
	b := &gantry.State{Messages: history(big, 5)} // a later turn's rebuilt history
	c.shrink(withScope("chat1"), b)
	if a.Messages[2].Content != b.Messages[2].Content {
		t.Error("same result, different previews across turns")
	}
}

func TestResolve(t *testing.T) {
	store := NewMemStore()
	c := newComp(store)
	big := bigMetric(400)
	s := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), s)
	full, err := Resolve(context.Background(), store, "chat1", s.Messages[2].Content)
	if err != nil || full != big {
		t.Errorf("Resolve(preview) = %.40s, %v", full, err)
	}
	if got, err := Resolve(context.Background(), store, "chat1", `{"a":1}`); err != nil || got != `{"a":1}` {
		t.Errorf("Resolve(plain) = %q, %v", got, err)
	}
	if _, err := Resolve(context.Background(), store, "other", s.Messages[2].Content); err == nil {
		t.Error("Resolve in another scope found the result")
	}
	if got, err := Resolve(context.Background(), store, "", s.Messages[2].Content); err == nil || got != s.Messages[2].Content {
		t.Error("Resolve with empty scope should return the content and an error")
	}
}

func TestShrinksOnTheCallAfterTheModelReadsTheResult(t *testing.T) {
	big := bigMetric(400)
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "get_metric", Input: []byte(`{}`)}}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "looking", ToolCalls: []gantry.ToolCall{{ID: "c2", Name: "get_metric", Input: []byte(`{}`)}}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatal(err)
	}
	a.Use(gantry.PhaseToolExec, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			for _, call := range s.PendingToolCalls {
				s.ToolResults = append(s.ToolResults, gantry.ToolResult{CallID: call.ID, Content: big})
			}
			return next(ctx, s)
		}
	})
	if err := a.With(New(NewMemStore(), Options{Scope: scopeFromCtx})); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range a.MiddlewareNames(gantry.PhaseAssembleContext) {
		found = found || n == "offload"
	}
	if !found {
		t.Fatalf("offload middleware not registered: %v", a.MiddlewareNames(gantry.PhaseAssembleContext))
	}
	if _, err := a.Run(withScope("chat1"), "go"); err != nil {
		t.Fatal(err)
	}
	reqs := mock.Requests()
	if len(reqs) != 3 {
		t.Fatalf("LLM calls = %d, want 3", len(reqs))
	}
	c1 := func(r gantry.LLMRequest) string {
		for _, m := range r.Messages {
			if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
				return m.Content
			}
		}
		return ""
	}
	if c1(reqs[1]) != big {
		t.Error("2nd request: c1 result should still be full")
	}
	if _, ok := previewMeta(c1(reqs[2])); !ok {
		t.Errorf("3rd request: c1 result should be a preview: %.80s", c1(reqs[2]))
	}
}

func BenchmarkShrinkSteadyState(b *testing.B) {
	c := newComp(NewMemStore())
	big := bigMetric(400)
	var msgs []gantry.Message
	for i := 0; i < 400; i++ {
		content := `{"src":"mtr_s","value":1}`
		if i < 50 {
			content = big
		}
		msgs = append(msgs, gantry.Message{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "x"}}},
			gantry.Message{Role: gantry.RoleTool, ToolCallID: "x", Content: content})
	}
	msgs = append(msgs, gantry.Message{Role: gantry.RoleAssistant})
	s := &gantry.State{Messages: msgs}
	ctx := withScope("chat1")
	c.shrink(ctx, s) // first call does the work
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.shrink(ctx, s)
	}
}

type countingStore struct {
	Store
	mu     sync.Mutex
	puts   int
	failAt int // fail the Nth Put (1-based); 0 = never
}

func (s *countingStore) Put(ctx context.Context, k, v string) error {
	s.mu.Lock()
	s.puts++
	n := s.puts
	s.mu.Unlock()
	if s.failAt != 0 && n == s.failAt {
		return errors.New("down")
	}
	return s.Store.Put(ctx, k, v)
}

func manySrcs(n int) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"src":"mtr_%03d","pad":"%s"}`, i, strings.Repeat("p", 30)))
	}
	return `{"items":[` + strings.Join(parts, ",") + `]}`
}

func TestRebuiltTurnsSkipPutsForStoredResults(t *testing.T) {
	cs := &countingStore{Store: NewMemStore()}
	c := newComp(cs)
	big := manySrcs(200)
	for turn := 1; turn <= 3; turn++ {
		s := &gantry.State{Messages: history(big, turn)}
		c.shrink(withScope("chat1"), s)
		if _, ok := previewMeta(s.Messages[2].Content); !ok {
			t.Fatalf("turn %d: not shrunk", turn)
		}
	}
	if cs.puts != 202 {
		t.Errorf("Puts = %d, want 202", cs.puts)
	}
}

func TestPartialIndexFailureIsRetriedNextCall(t *testing.T) {
	mem := NewMemStore()
	big := manySrcs(200)
	failing := &countingStore{Store: mem, failAt: 50}
	var reported error
	c := New(failing, Options{Scope: scopeFromCtx, OnError: func(_ context.Context, err error) { reported = err }}).(*component)
	s := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), s)
	if s.Messages[2].Content != big || reported == nil {
		t.Fatal("failed write should keep the full result and report")
	}
	c2 := newComp(mem)
	s2 := &gantry.State{Messages: history(big, 1)}
	c2.shrink(withScope("chat1"), s2)
	if _, ok := previewMeta(s2.Messages[2].Content); !ok {
		t.Fatal("retry did not shrink")
	}
	for i := 0; i < 200; i++ {
		if _, ok, _ := mem.Get(context.Background(), fmt.Sprintf("chat1:src:mtr_%03d", i)); !ok {
			t.Fatalf("index entry %d missing after retry", i)
		}
	}
}

type getFailStore struct{ Store }

func (getFailStore) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("down")
}

func TestGetFailureKeepsTheFullResultAndReports(t *testing.T) {
	var reported error
	c := New(getFailStore{NewMemStore()}, Options{Scope: scopeFromCtx, OnError: func(_ context.Context, err error) { reported = err }}).(*component)
	big := bigMetric(400)
	s := &gantry.State{Messages: history(big, 1)}
	c.shrink(withScope("chat1"), s)
	if s.Messages[2].Content != big || reported == nil {
		t.Error("Get failure should keep the result full and report")
	}
}

func TestMeterNeverGoesNegative(t *testing.T) {
	foreign := `{"_offloaded":{"ref":"res_x","chars":5,"read":"r"}}`
	c := newComp(NewMemStore())
	s := &gantry.State{Messages: []gantry.Message{{Role: gantry.RoleTool, Content: foreign}}}
	c.shrink(withScope("chat1"), s)
	if v := s.Meta[MetaSavedChars].(int); v < 0 {
		t.Errorf("saved_chars = %d", v)
	}
}

func TestPreviewCharsIsCapped(t *testing.T) {
	if got := (Options{PreviewChars: 1 << 20}).withDefaults().PreviewChars; got != 32<<10 {
		t.Errorf("PreviewChars = %d", got)
	}
}

func TestResolveEmptyScopeErrors(t *testing.T) {
	store := NewMemStore()
	c := newComp(store)
	s := &gantry.State{Messages: history(bigMetric(400), 1)}
	c.shrink(withScope("chat1"), s)
	p := s.Messages[2].Content
	got, err := Resolve(context.Background(), store, "", p)
	if err == nil || !strings.Contains(err.Error(), "no scope to resolve") || got != p {
		t.Errorf("Resolve = %v", err)
	}
}
