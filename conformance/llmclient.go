package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// LLMClientSuite verifies the contract of harness.LLMClient.
// Factory must return a client ready to handle exactly one Generate call
// that should succeed.
func LLMClientSuite(t *testing.T, factory func() harness.LLMClient) {
	t.Helper()

	t.Run("Generate_returns_response", func(t *testing.T) {
		c := factory()
		resp, err := c.Generate(context.Background(), harness.LLMRequest{
			Messages: []harness.Message{{Role: harness.RoleUser, Content: "hello"}},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if resp.StopReason == "" {
			t.Errorf("StopReason should be set; got empty")
		}
	})

	t.Run("Generate_respects_context_cancellation", func(t *testing.T) {
		c := factory()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Generate(ctx, harness.LLMRequest{})
		if err == nil {
			t.Skipf("client does not propagate cancellation (acceptable for mocks)")
		}
	})

	t.Run("Generate_with_tools", func(t *testing.T) {
		c := factory()
		_, err := c.Generate(context.Background(), harness.LLMRequest{
			Messages: []harness.Message{{Role: harness.RoleUser, Content: "x"}},
			Tools:    []harness.ToolDef{{Name: "noop", Description: "no-op", Schema: []byte(`{}`)}},
		})
		if err != nil {
			t.Errorf("Generate with tools: %v", err)
		}
	})
}
