// Package langfuse implements gantry.Tracer by shipping agent traces to
// Langfuse (https://langfuse.com) through its HTTP batch ingestion API.
//
// A Client buffers trace events and flushes them to Langfuse from a background
// goroutine, so tracing never blocks agent execution. Each agent run maps to
// one Langfuse trace: the top-level (parentless) span opens the trace, nested
// spans become observations under it, and RecordEvent calls become event
// observations. Tracing is best-effort — buffer-full and network failures are
// counted/logged, never returned to the agent.
//
// Callers must call Close (or Flush) before process exit to drain buffered
// events:
//
//	lf := langfuse.New(langfuse.WithPublicKey(pk), langfuse.WithSecretKey(sk))
//	defer lf.Close()
//	agent, err := gantry.NewAgent(gantry.WithLLM(llm), gantry.WithTracer(lf))
//	if err != nil {
//		// handle error
//	}
//
// Content capture: when used with the gantry agent, the run span carries the
// run input/output and a sanitized state snapshot, and the llm_call span is
// exported as a Langfuse "generation" with the assembled prompt as input and
// the model reply as output. Use WithRedactor to drop or rewrite any of these
// reserved values before they leave the process (e.g. to mask sensitive
// content); a nil redactor exports them unchanged.
package langfuse
