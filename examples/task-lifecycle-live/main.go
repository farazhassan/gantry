package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/llm/ollama"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/task"
	"github.com/farazhassan/gantry/taskmanager"
)

const (
	announceSession = "announce-1"
	detachedSession = "schedule-posts"

	rubric = "You are a release-notes critic. Reply with PASS only if the " +
		"announcement names the product version and includes a clear " +
		"call-to-action; otherwise explain what is missing."
)

// newOllamaLLM is the LLM seam: it returns a gantry.LLMClient for the given
// model and endpoint. Swapping in openai/anthropic later is a one-line change
// here. Mirrors examples/assistant/agent.go.
func newOllamaLLM(model, baseURL string) gantry.LLMClient {
	opts := []ollama.Option{}
	if baseURL != "" {
		opts = append(opts, ollama.WithBaseURL(baseURL))
	}
	return ollama.New(model, opts...)
}

// RunLiveExample wires the task stack exactly like the deterministic mock
// example (examples/task-lifecycle) but with live Ollama clients. It runs one
// release-announcement task to completion, then runs the Dispatcher headlessly
// for up to dispatchTimeout, printing whatever the live model actually does.
func RunLiveExample(ctx context.Context, model, ollamaURL string, dispatchTimeout time.Duration) error {
	// Two live clients, mirroring the mock example's agent/critic separation.
	agentLLM := newOllamaLLM(model, ollamaURL)
	criticLLM := newOllamaLLM(model, ollamaURL)

	agent, err := gantry.NewAgent(gantry.WithLLM(agentLLM))
	if err != nil {
		return err
	}
	// Server tools execute inline and buffer spawns into the task collector.
	// ask_user MUST be a client tool: only a client-tool call parks the task at
	// awaiting_input (a server tool would execute inline and never suspend).
	//
	// tool.Client must install before tool.FromTools: both share the same
	// underlying suspend-detection middleware (components/tool.
	// SuspendClientCalls), and only the first of the two to install actually
	// claims it — Client's own Install has no "already installed" guard for
	// it (unlike FromTools's, added so a bare FromTools/New agent with no
	// Client also gets suspend detection), so installing Client second would
	// hit that already-registered error.
	if err := agent.With(
		tool.Client(ask.Definition()),
		tool.FromTools(1, taskmanager.NewCreateTaskTool(), taskmanager.NewSpawnSessionTool()),
	); err != nil {
		return err
	}

	verifier := task.NewCriticVerifier(critic.NewLLM(criticLLM, rubric))

	taskStore := task.NewInMemory()
	metaStore := taskmanager.NewInMemoryMetaStore()
	readyQueue := taskmanager.NewInMemoryReadyQueue()

	// Stream task events. The Driver is constructed once and drives EVERY task,
	// so this sink is process-global: events from the synchronous StartTask
	// phase and the headless Dispatcher phase interleave on it, and we
	// demultiplex by ev.TaskID (the Driver seeds task identity into State.Meta;
	// the agent stamps it onto each Event). The BufferedSink decouples printing
	// from the run goroutine so a slow terminal can never stall the Dispatcher;
	// ev.Dropped reports any gap it had to cut.
	printSink := func(ev gantry.Event) error {
		if ev.Dropped > 0 {
			fmt.Printf("event  [%s] (%d earlier events dropped)\n", ev.TaskID, ev.Dropped)
		}
		switch ev.Type {
		case gantry.EventTextDelta:
			fmt.Printf("event  [%s] text: %q\n", ev.TaskID, ev.TextDelta)
		case gantry.EventToolCall:
			fmt.Printf("event  [%s] tool call: %s\n", ev.TaskID, ev.ToolCall.Name)
		case gantry.EventToolResult:
			fmt.Printf("event  [%s] tool result for %s (is_error=%v)\n", ev.TaskID, ev.ToolResult.CallID, ev.ToolResult.IsError)
		case gantry.EventDone:
			fmt.Printf("event  [%s] done: %s\n", ev.TaskID, ev.DoneReason)
		}
		return nil // phase_start/phase_end ignored to keep output readable
	}
	sink, stopSink := gantry.NewBufferedSink(printSink, 256)
	defer stopSink() // drain and join the print consumer before returning

	driver := task.NewDriver(agent, taskStore,
		task.WithVerifier(verifier),
		task.WithEventSink(sink),
	)

	// Deterministic id/session minters, kept for parity with the mock example.
	var idCount int
	tm := taskmanager.NewTaskManager(driver, taskStore, metaStore, readyQueue,
		taskmanager.WithIDFunc(func() string {
			idCount++
			return fmt.Sprintf("task-%d", idCount)
		}),
		taskmanager.WithSessionIDFunc(func() string { return detachedSession }),
	)

	fmt.Printf("starting task on session %q with a live model (%s)...\n", announceSession, model)

	// --- Synchronous phase: the draft task (and any same-session follow-on). ---
	driven, err := tm.StartTask(ctx, announceSession, "Draft the v2 release announcement")
	if err != nil {
		return fmt.Errorf("start task: %w", err)
	}
	if driven != nil {
		fmt.Printf("driven task    : %q -> %s after %d critic rejection(s)\n",
			driven.Goal, driven.Status, driven.TotalRejections)
	}

	// --- Headless phase: a real Dispatcher drains the ReadyQueue. We cannot
	// assume a park, so we print whatever happens and stop after dispatchTimeout.
	fmt.Printf("running Dispatcher headless for up to %s...\n", dispatchTimeout)

	dispCtx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	disp := taskmanager.NewDispatcher(tm,
		taskmanager.WithPollInterval(50*time.Millisecond),
		taskmanager.WithErrorHandler(func(err error) {
			fmt.Printf("dispatch error : %v\n", err)
		}),
		taskmanager.WithNotifier(func(t *task.Task) {
			fmt.Printf("notifier fired : session %q parked at awaiting_input asking: %q\n",
				t.SessionID, firstQuestion(t))
		}),
	)
	disp.Start(dispCtx)
	<-dispCtx.Done() // run until the timeout elapses
	disp.Stop()      // idempotent; joins the dispatch goroutine

	fmt.Println("headless window ended.")
	return nil
}

// firstQuestion best-effort extracts questions[0].text from a parked task's
// first pending ask_user call; returns "" if it cannot.
func firstQuestion(t *task.Task) string {
	if t == nil || len(t.Pending) == 0 {
		return ""
	}
	var req ask.Request
	if err := json.Unmarshal(t.Pending[0].Input, &req); err != nil {
		return ""
	}
	if len(req.Questions) == 0 {
		return ""
	}
	return req.Questions[0].Text
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	var (
		model           = flag.String("model", envOr("TASK_LIFECYCLE_MODEL", "llama3.1"), "Ollama model name")
		ollamaURL       = flag.String("ollama-url", os.Getenv("OLLAMA_URL"), "Ollama base URL (empty = ollama default)")
		dispatchTimeout = flag.Duration("dispatch-timeout", 30*time.Second, "how long to run the headless Dispatcher")
	)
	flag.Parse()

	if err := RunLiveExample(context.Background(), *model, *ollamaURL, *dispatchTimeout); err != nil {
		log.Fatal(err)
	}
}
