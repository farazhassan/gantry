package checkpointer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// FileStore persists one file per id under a directory, with atomic writes. Safe
// for concurrent use. Suitable for single-host resume across restarts.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a FileStore writing to dir, creating dir (0700) if missing.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("checkpointer: NewFileStore requires a non-empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("checkpointer: create dir %q: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// path maps an id to a file under dir. The id is hashed so arbitrary ids
// (including ones with path separators) cannot escape the directory.
func (s *FileStore) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func (s *FileStore) Put(_ context.Context, id string, blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst := s.path(id)
	tmp, err := os.CreateTemp(s.dir, "ckpt-*.tmp")
	if err != nil {
		return fmt.Errorf("checkpointer: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if n, werr := tmp.Write(blob); werr != nil || n < len(blob) {
		if werr == nil {
			werr = io.ErrShortWrite
		}
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer: write %q: %w", id, werr)
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

func (s *FileStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("checkpointer: read %q: %w", id, err)
	}
	return data, true, nil
}

var _ Store = (*FileStore)(nil)

// NewFile returns a Checkpointer backed by a FileStore at dir, persisting full
// State. Preserved for API compatibility (was *FileCheckpointer).
func NewFile(dir string) (*StoreCheckpointer, error) {
	st, err := NewFileStore(dir)
	if err != nil {
		return nil, err
	}
	return FromStore(st)
}
