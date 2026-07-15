package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestMetaKeyAliasesMatchCanonicalGantryConstants(t *testing.T) {
	if MetaTaskID != gantry.MetaTaskID {
		t.Errorf("task.MetaTaskID = %q, want gantry.MetaTaskID (%q)", MetaTaskID, gantry.MetaTaskID)
	}
	if MetaSessionID != gantry.MetaSessionID {
		t.Errorf("task.MetaSessionID = %q, want gantry.MetaSessionID (%q)", MetaSessionID, gantry.MetaSessionID)
	}
	// Wire values are frozen: Meta maps persisted by older runs must keep
	// resolving under the same keys.
	if gantry.MetaTaskID != "task.id" || gantry.MetaSessionID != "task.session_id" {
		t.Errorf("canonical values changed: %q / %q, want task.id / task.session_id",
			gantry.MetaTaskID, gantry.MetaSessionID)
	}
}
