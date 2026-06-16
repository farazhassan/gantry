package checkpointer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/farazhassan/gantry"
)

// FileCheckpointer persists one JSON file per session id under a directory.
// It is safe for concurrent use. Suitable for single-host resume across
// process restarts; for multi-host use a networked store.
type FileCheckpointer struct {
	dir string
	mu  sync.Mutex
}

// NewFile returns a FileCheckpointer writing to dir, creating dir (and any
// parents) if missing. It returns an error if dir cannot be created.
//
// The directory is created with 0700 (owner-only) permissions because
// checkpoint files can hold sensitive conversation and tool output. A caller
// that deliberately wants a shared location can pre-create dir with broader
// permissions; MkdirAll leaves an existing directory's mode untouched.
func NewFile(dir string) (*FileCheckpointer, error) {
	if dir == "" {
		return nil, errors.New("checkpointer: NewFile requires a non-empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("checkpointer: create dir %q: %w", dir, err)
	}
	return &FileCheckpointer{dir: dir}, nil
}

// path maps a session id to a file under dir. The id is hashed so that
// arbitrary ids (including ones containing path separators) cannot escape
// the directory and are always filesystem-safe.
func (c *FileCheckpointer) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

// Save writes state as JSON to a temp file in the same directory and renames
// it over the destination for atomic replacement. The rename is atomic and
// replaces any existing file on every supported OS, including Windows, where
// Go's os.Rename uses MoveFileEx with MOVEFILE_REPLACE_EXISTING.
//
// A nil state is rejected: persisting it would write JSON null and later load
// back a zero-value State, silently masking an upstream bug.
func (c *FileCheckpointer) Save(_ context.Context, id string, state *gantry.State) error {
	if state == nil {
		return errors.New("checkpointer: Save requires a non-nil state")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("checkpointer: marshal %q: %w", id, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	dst := c.path(id)
	tmp, err := os.CreateTemp(c.dir, "ckpt-*.tmp")
	if err != nil {
		return fmt.Errorf("checkpointer: temp file: %w", err)
	}
	tmpName := tmp.Name()
	// A short write (n < len(data)) without an error would persist truncated
	// JSON, so treat it as a failure.
	if n, err := tmp.Write(data); err != nil || n < len(data) {
		if err == nil {
			err = io.ErrShortWrite
		}
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer: write %q: %w", id, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer: close temp: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer: rename %q: %w", id, err)
	}
	return nil
}

// Load reads and decodes the session file, returning ErrNotFound (wrapped)
// when no file exists for id.
func (c *FileCheckpointer) Load(_ context.Context, id string) (*gantry.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.path(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
		}
		return nil, fmt.Errorf("checkpointer: read %q: %w", id, err)
	}
	var state gantry.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("checkpointer: unmarshal %q: %w", id, err)
	}
	return &state, nil
}

// Compile-time check.
var _ Checkpointer = (*FileCheckpointer)(nil)
