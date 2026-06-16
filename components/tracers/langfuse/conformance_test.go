package langfuse

import (
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/conformance"
)

func TestLangfuseConformsToTracer(t *testing.T) {
	conformance.TracerSuite(t, func() gantry.Tracer {
		c, _ := newServerClient(t)
		return c
	})
}
