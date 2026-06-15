package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/farazhassan/gantry/components/tracers/langfuse"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// RunExample runs the smallest possible agent under the supplied tracer and
// returns the terminal State. The tracer is a parameter so the hermetic test
// can inject an in-memory tracer while main() wires the real Langfuse client.
func RunExample(ctx context.Context, tracer harness.Tracer) (*harness.State, error) {
	// A scripted mock LLM keeps the run hermetic with respect to LLM providers:
	// the point of this program is to exercise the *tracer*, not a model.
	llm := eval.NewMockLLMClient(harness.LLMResponse{
		Content:    "Hello! I'm a minimal gantry agent.",
		StopReason: harness.StopReasonEnd,
	})

	a, err := harness.New(harness.WithLLM(llm), harness.WithTracer(tracer))
	if err != nil {
		return nil, err
	}
	return a.Run(ctx, "introduce yourself")
}

func main() {
	if os.Getenv("LANGFUSE_PUBLIC_KEY") == "" || os.Getenv("LANGFUSE_SECRET_KEY") == "" {
		log.Fatal("set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY to run the smoke test " +
			"(LANGFUSE_HOST is optional; defaults to https://cloud.langfuse.com)")
	}

	// Build the real Langfuse client from the environment. New panics on missing
	// credentials, but the precheck above turns that into a friendly message.
	lf := langfuse.New()

	state, err := RunExample(context.Background(), lf)
	if err != nil {
		// Still try to flush whatever was buffered before exiting.
		_ = lf.Close()
		log.Fatalf("agent run failed: %v", err)
	}

	// Close drains the buffer with a final synchronous flush and stops the
	// worker. Sends are best-effort so Close always returns nil; delivery health
	// is reported via FailedSends/Dropped instead.
	_ = lf.Close()

	fmt.Println("final output:", state.FinalOutput)
	fmt.Println("done reason: ", state.DoneReason)
	fmt.Println("dropped items:", lf.Dropped())
	fmt.Println("failed sends: ", lf.FailedSends())

	// The whole point of the smoke test: a wire-contract or auth mismatch shows
	// up as a non-2xx/3xx (or a transport error), counted by FailedSends. Fail
	// loudly so the mismatch can't hide behind a "flushed" message.
	if n := lf.FailedSends(); n > 0 {
		log.Fatalf("ingestion failed (%d batch send(s)) — see the langfuse: errors logged above; "+
			"the wire contract or credentials may be wrong", n)
	}
	if lf.Dropped() > 0 {
		log.Fatalf("buffer dropped %d events before flush — increase batch/flush settings", lf.Dropped())
	}
	fmt.Printf("flushed cleanly — open %s and look for the most recent \"run\" trace\n", lf.Host())
}
