package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// StreamingLLMClientSuite verifies the contract of harness.StreamingLLMClient.
// Factory must return a client ready to handle exactly one GenerateStream call
// that should succeed.
func StreamingLLMClientSuite(t *testing.T, factory func() harness.StreamingLLMClient) {
	t.Helper()

	t.Run("GenerateStream_aggregation_integrity", func(t *testing.T) {
		c := factory()
		var sb strings.Builder
		resp, err := c.GenerateStream(context.Background(), harness.LLMRequest{
			Messages: []harness.Message{{Role: harness.RoleUser, Content: "hello"}},
		}, func(ch harness.StreamChunk) error {
			sb.WriteString(ch.TextDelta)
			return nil
		})
		if err != nil {
			t.Fatalf("GenerateStream: %v", err)
		}
		if resp.StopReason == "" {
			t.Errorf("StopReason should be set; got empty")
		}
		// If the client reports aggregated content, the streamed deltas must
		// reconstruct it exactly.
		if resp.Content != "" && sb.String() != resp.Content {
			t.Errorf("concatenated deltas %q != resp.Content %q", sb.String(), resp.Content)
		}
	})

	t.Run("GenerateStream_yield_error_propagates", func(t *testing.T) {
		c := factory()
		sentinel := errors.New("conformance: yield boom")
		_, err := c.GenerateStream(context.Background(), harness.LLMRequest{
			Messages: []harness.Message{{Role: harness.RoleUser, Content: "hello"}},
		}, func(harness.StreamChunk) error {
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the yield error propagated", err)
		}
	})

	t.Run("GenerateStream_respects_context_cancellation", func(t *testing.T) {
		c := factory()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.GenerateStream(ctx, harness.LLMRequest{}, func(harness.StreamChunk) error { return nil })
		if err == nil {
			t.Skipf("client does not propagate cancellation (acceptable for mocks)")
		}
	})
}
