package checkpointer_test

import (
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
)

// Every State field must have a Field constant and vice versa, so projection by
// field name stays correct as State evolves.
func TestFieldConstantsMatchStateFields(t *testing.T) {
	stateFields := map[string]bool{}
	rt := reflect.TypeOf(gantry.State{})
	for i := 0; i < rt.NumField(); i++ {
		stateFields[rt.Field(i).Name] = true
	}
	constFields := map[string]bool{}
	for _, f := range checkpointer.AllFields() {
		constFields[string(f)] = true
	}
	for name := range stateFields {
		if !constFields[name] {
			t.Errorf("State field %q has no checkpointer.Field constant", name)
		}
	}
	for name := range constFields {
		if !stateFields[name] {
			t.Errorf("checkpointer.Field %q matches no State field", name)
		}
	}
}
