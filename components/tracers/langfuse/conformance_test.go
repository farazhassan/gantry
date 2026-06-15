package langfuse

import (
	"testing"

	"github.com/farazhassan/gantry/conformance"
	"github.com/farazhassan/gantry/harness"
)

func TestLangfuseConformsToTracer(t *testing.T) {
	conformance.TracerSuite(t, func() harness.Tracer {
		c, _ := newServerClient(t)
		return c
	})
}
