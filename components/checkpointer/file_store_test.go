package checkpointer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
)

func TestFileStore_PutGetRoundTrip(t *testing.T) {
	s, err := checkpointer.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Put(context.Background(), "id1", []byte("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.Get(context.Background(), "id1")
	if err != nil || !found || string(got) != "data" {
		t.Fatalf("Get: got=%q found=%v err=%v", got, found, err)
	}
}

func TestFileStore_GetMissing(t *testing.T) {
	s, _ := checkpointer.NewFileStore(t.TempDir())
	_, found, err := s.Get(context.Background(), "ghost")
	if err != nil || found {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
}

func TestFileStore_OverwritesAtomicallyNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	s, _ := checkpointer.NewFileStore(dir)
	ctx := context.Background()
	_ = s.Put(ctx, "k", []byte("first"))
	_ = s.Put(ctx, "k", []byte("second"))
	got, _, _ := s.Get(ctx, "k")
	if string(got) != "second" {
		t.Fatalf("want second, got %q", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestFileStore_IDHashedToOneFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := checkpointer.NewFileStore(dir)
	if err := s.Put(context.Background(), "a/../b", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 file, got %d", len(entries))
	}
}

func TestNewFileStore_CreatesOwnerOnlyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "ckpt")
	if _, err := checkpointer.NewFileStore(dir); err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("want 0700, got %o", perm)
	}
}

func TestNewFileStore_EmptyDirErrors(t *testing.T) {
	if _, err := checkpointer.NewFileStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}
