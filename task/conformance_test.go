package task_test

import (
	"testing"

	"github.com/farazhassan/gantry/conformance"
	"github.com/farazhassan/gantry/task"
)

func TestInMemoryConformance(t *testing.T) {
	conformance.TaskStoreSuite(t, func() task.TaskStore { return task.NewInMemory() })
}
