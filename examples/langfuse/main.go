package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tracers/langfuse"
	"github.com/farazhassan/gantry/eval"
)

// RunExample runs the smallest possible agent under the supplied tracer and
// returns the terminal State. The tracer is a parameter so the hermetic test
// can inject an in-memory tracer while main() wires the real Langfuse client.
func RunExample(ctx context.Context, tracer gantry.Tracer) (*gantry.State, error) {
	// A scripted mock LLM keeps the run hermetic with respect to LLM providers:
	// the point of this program is to exercise the *tracer*, not a model.
	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Hello! I'm a minimal gantry agent.",
		StopReason: gantry.StopReasonEnd,
	})

	a, err := gantry.NewAgent(gantry.WithLLM(llm), gantry.WithTracer(tracer))
	if err != nil {
		return nil, err
	}
	return a.Run(ctx, "introduce yourself")
}

// redactSensitive is an illustrative Redactor: it drops the full state snapshot
// and masks the captured input/output so no message content reaches Langfuse —
// the shape you would use to keep sensitive data (e.g. PHI) out of traces.
// Structural attrs (iteration, done, done_reason) are unaffected and still flow
// to metadata.
func redactSensitive(key string, value any) (any, bool) {
	switch key {
	case gantry.AttrState:
		return nil, false // drop entirely
	case gantry.AttrInput, gantry.AttrOutput:
		return "[redacted]", true // mask, but keep the field present
	default:
		return value, true
	}
}

func main() {
	if os.Getenv("LANGFUSE_PUBLIC_KEY") == "" || os.Getenv("LANGFUSE_SECRET_KEY") == "" {
		log.Fatal("set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY to run the smoke test " +
			"(LANGFUSE_HOST is optional; defaults to https://cloud.langfuse.com)")
	}

	// Build the real Langfuse client from the environment. New panics on missing
	// credentials, but the precheck above turns that into a friendly message.
	//
	// Content capture is on by default: the "run" trace carries the run
	// input/output and a sanitized state snapshot, and the llm_call span ships as
	// a Langfuse "generation" with the assembled prompt and the model reply. Set
	// LANGFUSE_REDACT=1 to exercise the other path — a redactor that masks message
	// content and drops the state snapshot before anything leaves the process.
	opts := []langfuse.Option{}
	if os.Getenv("LANGFUSE_REDACT") != "" {
		opts = append(opts, langfuse.WithRedactor(redactSensitive))
	}
	lf := langfuse.New(opts...)

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

	// The whole point of the smoke test: any failed send — a non-success HTTP
	// status (>= 300), or a transport error (DNS/TLS/timeout/unreachable host) —
	// is counted by FailedSends. Fail loudly so it can't hide behind a "flushed"
	// message.
	if n := lf.FailedSends(); n > 0 {
		log.Fatalf("ingestion failed (%d batch send(s)) — see the langfuse: errors logged above. "+
			"Likely causes: wrong credentials, a wire-contract mismatch (bad status), "+
			"or an unreachable/misconfigured host", n)
	}
	if lf.Dropped() > 0 {
		log.Fatalf("buffer dropped %d events before flush — increase batch/flush settings", lf.Dropped())
	}
	fmt.Printf("flushed cleanly — open %s and look for the most recent \"run\" trace\n", lf.Host())
	if os.Getenv("LANGFUSE_REDACT") != "" {
		fmt.Println("redaction on: the trace input/output show \"[redacted]\" and the state snapshot is absent")
	} else {
		fmt.Println("the run trace shows input/output, a state snapshot, and a nested \"generation\" with the prompt and reply")
	}
}
