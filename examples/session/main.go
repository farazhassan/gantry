package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

// Result bundles the three turns so the test can assert continuity, cumulative
// usage, and durable resume.
type Result struct {
	Turn1   *harness.State
	Turn2   *harness.State
	Resumed *harness.State
}

// reply builds a scripted no-tool-call response carrying a fixed usage so the
// example can show cumulative tokens across turns.
func reply(content string) harness.LLMResponse {
	return harness.LLMResponse{
		Content:    content,
		StopReason: harness.StopReasonEnd,
		Usage:      harness.Usage{InputTokens: 10, OutputTokens: 5},
	}
}

// RunExample drives one session across two turns through a Manager, then opens a
// SECOND Manager over the same store + id to show durable resume. The agent
// carries neither memory nor checkpointer middleware — the Session owns the
// transcript. The MockLLMClient holds a persistent script cursor that advances
// across turns, so we script one reply per expected turn.
func RunExample(ctx context.Context) (*Result, error) {
	const sessionID = "user-42"

	llm := eval.NewMockLLMClient(
		reply("Nice to meet you, Faraz."),
		reply("Your name is Faraz."),
		reply("Welcome back, Faraz."),
	)

	agent, err := harness.New(harness.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	// One shared durable store stands in for Redis/SQL. Both managers point at
	// it; the in-memory store is process-local, so sharing the instance is how
	// the example demonstrates the cross-process resume round-trip.
	store := checkpointer.NewInMemory()

	mgr := session.NewManager(agent, store)
	s := mgr.Session(sessionID)

	turn1, err := s.Run(ctx, "my name is Faraz.")
	if err != nil {
		return nil, err
	}
	turn2, err := s.Run(ctx, "what is my name?")
	if err != nil {
		return nil, err
	}

	// A brand-new Manager over the SAME store + id continues transparently.
	mgr2 := session.NewManager(agent, store)
	resumed, err := mgr2.Session(sessionID).Run(ctx, "thanks!")
	if err != nil {
		return nil, err
	}

	return &Result{Turn1: turn1, Turn2: turn2, Resumed: resumed}, nil
}

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("turn 1  : %d messages, %d input tokens\n", len(res.Turn1.Messages), res.Turn1.Usage.InputTokens)
	fmt.Printf("turn 2  : %d messages, %d input tokens (cumulative)\n", len(res.Turn2.Messages), res.Turn2.Usage.InputTokens)
	fmt.Printf("resumed : %d messages, %d input tokens (new Manager, same store)\n", len(res.Resumed.Messages), res.Resumed.Usage.InputTokens)
	fmt.Println("final   :", res.Resumed.FinalOutput)
}
