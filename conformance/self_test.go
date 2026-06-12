package conformance_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/conformance"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestMockLLMClientConformance(t *testing.T) {
	conformance.LLMClientSuite(t, func() harness.LLMClient {
		return eval.NewMockLLMClient(
			harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd},
		)
	})
}

func TestInMemoryMemoryConformance(t *testing.T) {
	conformance.MemorySuite(t, func() memory.Memory {
		return memory.NewInMemoryStore()
	})
}

// echoTool is a minimal Tool used to test the ToolSuite against itself.
type echoTool struct{}

func (echoTool) Definition() harness.ToolDef {
	return harness.ToolDef{Name: "echo", Description: "echo", Schema: []byte(`{}`)}
}

func (echoTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	return in, nil
}

func TestEchoToolConformance(t *testing.T) {
	conformance.ToolSuite(t, func() tool.Tool { return echoTool{} })
}

func TestDefaultTracerConformance(t *testing.T) {
	conformance.TracerSuite(t, func() harness.Tracer {
		return harness.NewDefaultTracer(harness.NewTrace())
	})
}

func TestInMemoryCheckpointerConformance(t *testing.T) {
	conformance.CheckpointerSuite(t, func() checkpointer.Checkpointer {
		return checkpointer.NewInMemory()
	})
}

func TestSlidingCompactorConformance(t *testing.T) {
	conformance.CompactorSuite(t, func() compactor.Compactor {
		return compactor.NewSlidingWindow(3)
	})
}

func TestBudgetLimiterConformance(t *testing.T) {
	conformance.LimiterSuite(t, func() limiter.Limiter {
		return limiter.NewBudget(limiter.Limits{MaxTokens: 100})
	})
}

func TestStaticRetrieverConformance(t *testing.T) {
	conformance.RetrieverSuite(t, func() retriever.Retriever {
		return retriever.NewStatic([]harness.Document{{ID: "x", Content: "x"}})
	})
}

func TestRegexGuardrailConformance(t *testing.T) {
	conformance.GuardrailSuite(t, func() guardrail.Guardrail {
		return guardrail.NewRegex(`forbidden`, guardrail.DirectionInput)
	})
}

func TestLLMCriticConformance(t *testing.T) {
	conformance.CriticSuite(t, func() critic.Critic {
		return critic.NewLLM(eval.NewMockLLMClient(harness.LLMResponse{Content: "PASS"}), "rubric")
	})
}

func TestLLMPlannerConformance(t *testing.T) {
	conformance.PlannerSuite(t, func() planner.Planner {
		return planner.NewLLM(eval.NewMockLLMClient(harness.LLMResponse{Content: "1. step"}), "")
	})
}

func TestNoOpHumanInLoopConformance(t *testing.T) {
	conformance.HumanInLoopSuite(t, func() humanloop.HumanInLoop {
		return humanloop.NewNoOp()
	})
}
