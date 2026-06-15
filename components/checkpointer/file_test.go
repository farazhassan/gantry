package checkpointer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/harness"
)

func TestFileCheckpointer_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fc, err := checkpointer.NewFile(dir)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	ctx := context.Background()
	want := &harness.State{Input: "hi", Messages: []harness.Message{{Role: harness.RoleUser, Content: "hi"}}}
	if err := fc.Save(ctx, "sess1", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := fc.Load(ctx, "sess1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Input != "hi" || len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
}

func TestFileCheckpointer_LoadMissingReturnsErrNotFound(t *testing.T) {
	fc, _ := checkpointer.NewFile(t.TempDir())
	_, err := fc.Load(context.Background(), "nope")
	if !errors.Is(err, checkpointer.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFileCheckpointer_SaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	fc, _ := checkpointer.NewFile(dir)
	ctx := context.Background()
	if err := fc.Save(ctx, "s", &harness.State{Input: "first"}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := fc.Save(ctx, "s", &harness.State{Input: "second"}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got, _ := fc.Load(ctx, "s")
	if got.Input != "second" {
		t.Fatalf("want overwrite to 'second', got %q", got.Input)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestNewFile_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	if _, err := checkpointer.NewFile(dir); err != nil {
		t.Fatalf("NewFile should create dir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: err=%v", err)
	}
}

func TestFileCheckpointer_SaveNilStateErrors(t *testing.T) {
	fc, _ := checkpointer.NewFile(t.TempDir())
	if err := fc.Save(context.Background(), "s", nil); err == nil {
		t.Fatal("want error saving nil state, got nil")
	}
}

func TestNewFile_DirIsOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ckpt")
	if _, err := checkpointer.NewFile(dir); err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("want dir perm 0700, got %o", perm)
	}
}

func TestFileCheckpointer_IDSanitized(t *testing.T) {
	dir := t.TempDir()
	fc, _ := checkpointer.NewFile(dir)
	ctx := context.Background()
	if err := fc.Save(ctx, "a/../b", &harness.State{Input: "x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fc.Load(ctx, "a/../b")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Input != "x" {
		t.Fatalf("want x, got %q", got.Input)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 file under dir, got %d", len(entries))
	}
}
