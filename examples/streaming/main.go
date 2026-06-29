package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// calcTool adds two integers; the model calls it on the first turn.
type calcTool struct{}

func (calcTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        "calc",
		Description: "adds two integers a and b",
		Schema:      json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}`),
	}
}

func (calcTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, err
	}
	return json.Marshal(args.A + args.B)
}

// newAgent builds an agent with a scripted two-turn conversation and the calc
// tool registered. The scripted MockLLMClient is streaming-capable (its
// GenerateStream chunks Content), so the example exercises the real streaming
// path while staying hermetic.
func newAgent() (*gantry.Agent, error) {
	llm := eval.NewMockLLMClient(
		// Turn 1: call the calc tool (StopReasonToolUse keeps the loop going).
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "call-1", Name: "calc", Input: json.RawMessage(`{"a":2,"b":3}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		// Turn 2: report the final answer.
		gantry.LLMResponse{
			Content:    "2 + 3 = 5 (computed by the calc tool).",
			StopReason: gantry.StopReasonEnd,
		},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}
	if err := a.With(tool.FromTools(1, calcTool{})); err != nil {
		return nil, err
	}
	return a, nil
}

// streamHandler forwards every run Event to the client as an SSE data frame,
// flushing after each so a browser sees tokens live.
func streamHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	a, err := newAgent()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering disables response buffering in nginx and similar
	// reverse proxies, which otherwise defeats live SSE streaming.
	w.Header().Set("X-Accel-Buffering", "no")

	enc := json.NewEncoder(w)
	sink := func(ev gantry.Event) error {
		// SSE frame: "data: " + <json> + "\n" (from Encode) + "\n".
		if _, err := fmt.Fprint(w, "data: "); err != nil {
			return err
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, "\n"); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	// RunStream follows the request context: if the browser disconnects,
	// cancellation propagates and the run stops.
	// The final state is discarded here: its FinalOutput was already sent to
	// the client as the terminal done event.
	if _, err := a.RunStream(r.Context(), "what is 2 + 3?", sink); err != nil {
		log.Printf("run stream: %v", err)
	}
}

func main() {
	http.HandleFunc("/stream", streamHandler)
	log.Println("listening on http://localhost:8080/stream")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
