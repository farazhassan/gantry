// components/tool/resumable_test.go
package tool_test

import (
	"context"
	"encoding/json"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// fakeResumable is a minimal tool.ResumableTool used across this package's
// tests to exercise pending/suspend/resume without depending on
// components/subagent. Configure invokeErr to make Invoke return that error
// unwrapped (e.g. a *gantry.PendingResult); leave it nil for a normal
// success returning {"output":"invoked"}. Configure resumeFn to control
// Resume's outcome per call.
type fakeResumable struct {
	def       gantry.ToolDef
	invokeErr error
	resumeFn  func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error)
}

func (f *fakeResumable) Definition() gantry.ToolDef { return f.def }

func (f *fakeResumable) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	return json.RawMessage(`{"output":"invoked"}`), nil
}

func (f *fakeResumable) Resume(ctx context.Context, resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
	if f.resumeFn != nil {
		return f.resumeFn(resume, results)
	}
	return json.RawMessage(`{"output":"resumed"}`), nil
}

var _ tool.ResumableTool = (*fakeResumable)(nil)
