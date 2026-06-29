package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/task"
	"github.com/farazhassan/gantry/taskmanager"
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

// RunExample drives one release-announcement task through its full lifecycle.
//
// Synchronous phase (one StartTask call): the draft task spawns a same-session
// proofread follow-on (create_task) and an unrelated detached session
// (spawn_session); the critic rejects the first draft and accepts the revision;
// the proofread follow-on is then popped from the session FIFO and driven to
// done; the detached session lands on the ReadyQueue.
//
// Headless phase: a real Dispatcher drains the ReadyQueue, driving the detached
// session with no human attached until its agent asks a question (ask_user), at
// which point the task parks at awaiting_input and the WithNotifier callback
// fires with the parked task.
func RunExample(ctx context.Context) (*Result, error) {
	// Agent script — 5 turns, consumed in order (see plan for the turn table).
	agentLLM := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		// Turn 1: the draft task spawns a same-session follow-on and a detached session.
		toolCalls(
			call("call-create-1", "create_task", map[string]string{"goal": "Proofread the announcement draft"}),
			call("call-spawn-1", "spawn_session", map[string]string{"goal": "Schedule the launch social posts"}),
		),
		// Turn 2: draft WITHOUT a call-to-action -> critic rejects.
		answer("Gantry v2 is out. It adds a durable task layer."),
		// Turn 3: revised draft WITH a call-to-action -> critic accepts -> done.
		answer("Gantry v2 is out, with a durable task layer. Upgrade today at gantry.dev/v2!"),
		// Turn 4: the proofread follow-on (popped from the FIFO queue) -> done.
		answer("Proofread: typo-free and on-message. Ship it."),
		// Turn 5: the detached session asks a question -> parks at awaiting_input.
		toolCalls(call("call-ask-1", "ask_user", ask.Request{
			Questions: []ask.Question{{
				Header: "Timezone",
				Text:   "Which timezone should the posts target?",
			}},
		})),
	})

	// Critic script — 3 turns: reject the first draft, accept the revision, accept the proofread.
	criticLLM := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		answer("Reject: the announcement needs an explicit call-to-action."),
		answer("PASS — names the version and includes a clear call-to-action."),
		answer("PASS."),
	})

	agent, err := gantry.NewAgent(gantry.WithLLM(agentLLM))
	if err != nil {
		return nil, err
	}
	// Server tools execute inline and buffer spawns into the task collector.
	// ask_user MUST be a client tool: only a client-tool call suspends the task
	// at awaiting_input (a server tool would execute inline and never park).
	if err := agent.With(
		tool.FromTools(1, taskmanager.NewCreateTaskTool(), taskmanager.NewSpawnSessionTool()),
		tool.Client(ask.Definition()),
	); err != nil {
		return nil, err
	}

	rubric := "You are a release-notes critic. Reply with PASS only if the " +
		"announcement names the product version and includes a clear " +
		"call-to-action; otherwise explain what is missing."
	verifier := task.NewCriticVerifier(critic.NewLLM(criticLLM, rubric))

	taskStore := task.NewInMemory()
	metaStore := taskmanager.NewInMemoryMetaStore()
	readyQueue := taskmanager.NewInMemoryReadyQueue()

	driver := task.NewDriver(agent, taskStore, task.WithVerifier(verifier))

	// Deterministic id minters make the example readable and the draft/proofread
	// tasks addressable by a fixed id. All id minting happens synchronously inside
	// StartTask (before the Dispatcher goroutine starts), so no lock is needed.
	var idCount int
	tm := taskmanager.NewTaskManager(driver, taskStore, metaStore, readyQueue,
		taskmanager.WithIDFunc(func() string {
			idCount++
			return fmt.Sprintf("task-%d", idCount)
		}),
		taskmanager.WithSessionIDFunc(func() string { return detachedSession }),
	)

	// --- Synchronous phase: draft + proofread run to completion here. ---
	if _, err := tm.StartTask(ctx, announceSession, "Draft the v2 release announcement"); err != nil {
		return nil, err
	}

	draft, err := taskStore.LoadTask(ctx, draftTaskID)
	if err != nil {
		return nil, fmt.Errorf("load draft task: %w", err)
	}
	proofread, err := taskStore.LoadTask(ctx, proofreadTaskID)
	if err != nil {
		return nil, fmt.Errorf("load proofread task: %w", err)
	}

	// --- Headless phase: the real Dispatcher drives the detached session. ---
	var (
		mu       sync.Mutex
		notified *task.Task
	)
	disp := taskmanager.NewDispatcher(tm,
		taskmanager.WithPollInterval(5*time.Millisecond),
		taskmanager.WithNotifier(func(t *task.Task) {
			mu.Lock()
			notified = t
			mu.Unlock()
		}),
	)
	disp.Start(ctx)
	defer disp.Stop()

	// Wait until the detached task parks and the notifier fires.
	parked, err := waitForNotification(&mu, &notified)
	if err != nil {
		return nil, err
	}
	disp.Stop() // stop driving once we've observed the park

	question, err := firstQuestion(parked)
	if err != nil {
		return nil, err
	}

	return &Result{
		Task1Status:      draft.Status,
		Task1Rejections:  draft.TotalRejections,
		ProofreadStatus:  proofread.Status,
		DetachedStatus:   parked.Status,
		Notified:         parked,
		NotifiedQuestion: question,
	}, nil
}

// waitForNotification polls until the notifier has recorded a parked task, or
// times out. notified is read under mu because the notifier writes it from the
// Dispatcher goroutine.
func waitForNotification(mu *sync.Mutex, notified **task.Task) (*task.Task, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := *notified
		mu.Unlock()
		if got != nil {
			return got, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for headless park notification")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// firstQuestion extracts questions[0].text from a parked task's first pending
// ask_user call.
func firstQuestion(t *task.Task) (string, error) {
	if len(t.Pending) == 0 {
		return "", errors.New("parked task has no pending tool calls")
	}
	var req ask.Request
	if err := json.Unmarshal(t.Pending[0].Input, &req); err != nil {
		return "", fmt.Errorf("decode ask_user input: %w", err)
	}
	if len(req.Questions) == 0 {
		return "", errors.New("ask_user call carried no questions")
	}
	return req.Questions[0].Text, nil
}

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("draft task     : %s after %d critic rejection(s) (reject -> revise -> accept)\n",
		res.Task1Status, res.Task1Rejections)
	fmt.Printf("proofread task : %s (same-session FIFO follow-on)\n", res.ProofreadStatus)
	fmt.Printf("detached task  : %s (driven headless by the Dispatcher)\n", res.DetachedStatus)
	fmt.Printf("notifier fired : session %q asked: %q\n", res.Notified.SessionID, res.NotifiedQuestion)
}
