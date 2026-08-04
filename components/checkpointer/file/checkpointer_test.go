package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/file"
)

func TestNew_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fc, err := file.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	want := &gantry.State{Input: "hi", Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}
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

func TestNew_LoadMissingReturnsErrNotFound(t *testing.T) {
	fc, _ := file.New(t.TempDir())
	_, err := fc.Load(context.Background(), "nope")
	if !errors.Is(err, checkpointer.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestNew_SaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	fc, _ := file.New(dir)
	ctx := context.Background()
	if err := fc.Save(ctx, "s", &gantry.State{Input: "first"}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := fc.Save(ctx, "s", &gantry.State{Input: "second"}); err != nil {
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

func TestNew_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	if _, err := file.New(dir); err != nil {
		t.Fatalf("New should create dir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: err=%v", err)
	}
}

func TestNew_SaveNilStateErrors(t *testing.T) {
	fc, _ := file.New(t.TempDir())
	if err := fc.Save(context.Background(), "s", nil); err == nil {
		t.Fatal("want error saving nil state, got nil")
	}
}

func TestNew_DirIsOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ckpt")
	if _, err := file.New(dir); err != nil {
		t.Fatalf("New: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("want dir perm 0700, got %o", perm)
	}
}

func TestNew_IDSanitized(t *testing.T) {
	dir := t.TempDir()
	fc, _ := file.New(dir)
	ctx := context.Background()
	if err := fc.Save(ctx, "a/../b", &gantry.State{Input: "x"}); err != nil {
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
