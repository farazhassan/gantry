package memory_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/eval"
)

func TestWithMemoryPreloadsMessagesIntoState(t *testing.T) {
	store := memory.NewInMemoryStore()
	store.Append(context.Background(), gantry.Message{Role: gantry.RoleUser, Content: "earlier turn"})

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(memory.New(store)); err != nil {
		t.Fatalf("install memory: %v", err)
	}

	if _, err := a.Run(context.Background(), "now"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	msgs := reqs[0].Messages
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (history + current); got %+v", msgs)
	}
	if msgs[0].Content != "earlier turn" {
		t.Errorf("history not prepended; messages[0] = %+v", msgs[0])
	}
}

func TestWithMemoryAppendsAssistantResponse(t *testing.T) {
	store := memory.NewInMemoryStore()
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "hello", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(memory.New(store)); err != nil {
		t.Fatalf("install memory: %v", err)
	}

	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := store.Read(context.Background())
	// Expect: user("hi"), assistant("hello")
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2; got %+v", len(got), got)
	}
	if got[0].Role != gantry.RoleUser || got[0].Content != "hi" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Role != gantry.RoleAssistant || got[1].Content != "hello" {
		t.Errorf("got[1] = %+v", got[1])
	}
}
