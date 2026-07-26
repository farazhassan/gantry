package sqlitevec_test

import (
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry/components/sqlitevec"
)

func newStore(t *testing.T, dim int) *sqlitevec.Store {
	t.Helper()
	s, err := sqlitevec.New(filepath.Join(t.TempDir(), "mem.db"), sqlitevec.WithDim(dim))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewCreatesSchemaAndClose(t *testing.T) {
	s := newStore(t, 2)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewPanicsWithoutDim(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New without WithDim did not panic")
		}
	}()
	_, _ = sqlitevec.New(":memory:")
}

func TestNewRejectsInvalidTableName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New with an invalid table name did not panic")
		}
	}()
	_, _ = sqlitevec.New(":memory:", sqlitevec.WithDim(2),
		sqlitevec.WithTable("bad name; drop--"))
}
