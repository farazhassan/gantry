package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/task"
)

const (
	announceSession = "announce-1"
	detachedSession = "schedule-posts"
	draftTaskID     = "task-1"
	proofreadTaskID = "task-2"
)

// Result bundles the observable milestones of the lifecycle so the test asserts
// on data rather than stdout.
type Result struct {
	Task1Status      task.TaskStatus // TaskDone — the draft, after reject -> revise -> accept
	Task1Rejections  int             // 1 — one critic rejection before acceptance
	ProofreadStatus  task.TaskStatus // TaskDone — same-session FIFO follow-on
	DetachedStatus   task.TaskStatus // TaskAwaitingInput — headless park
	Notified         *task.Task      // the parked task handed to the notifier
	NotifiedQuestion string          // questions[0].text pulled from Notified.Pending[0]
}

// answer builds a no-tool-call final answer turn (also used for critic verdicts).
func answer(content string) eval.MockTurn {
	return eval.MockTurn{Response: gantry.LLMResponse{
		Content:    content,
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 10, OutputTokens: 5},
	}}
}

// toolCalls builds a turn that emits one or more tool calls.
func toolCalls(calls ...gantry.ToolCall) eval.MockTurn {
	return eval.MockTurn{Response: gantry.LLMResponse{
		ToolCalls:  calls,
		StopReason: gantry.StopReasonToolUse,
		Usage:      gantry.Usage{InputTokens: 10, OutputTokens: 5},
	}}
}

// call builds one tool call, JSON-encoding input into the Input raw message.
func call(id, name string, input any) gantry.ToolCall {
	raw, err := json.Marshal(input)
	if err != nil {
		panic("task-lifecycle example: bad tool-call input: " + err.Error())
	}
	return gantry.ToolCall{ID: id, Name: name, Input: raw}
}

// RunExample is implemented in Task 2.
func RunExample(ctx context.Context) (*Result, error) {
	return nil, errors.New("not implemented")
}

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	_ = res // main() printing is fleshed out in Task 3
}
