package file

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

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Store persists one file per id under a directory, with atomic writes. Safe
// for concurrent use. Suitable for single-host resume across restarts.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns a Store writing to dir, creating dir (0700) if missing.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("checkpointer/file: NewStore requires a non-empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("checkpointer/file: create dir %q: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// path maps an id to a file under dir. The id is hashed so arbitrary ids
// (including ones with path separators) cannot escape the directory.
func (s *Store) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func (s *Store) Put(_ context.Context, id string, blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst := s.path(id)
	tmp, err := os.CreateTemp(s.dir, "ckpt-*.tmp")
	if err != nil {
		return fmt.Errorf("checkpointer/file: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if n, werr := tmp.Write(blob); werr != nil || n < len(blob) {
		if werr == nil {
			werr = io.ErrShortWrite
		}
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer/file: write %q: %w", id, werr)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer/file: close temp: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpointer/file: rename %q: %w", id, err)
	}
	return nil
}

func (s *Store) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("checkpointer/file: read %q: %w", id, err)
	}
	return data, true, nil
}

var _ checkpointer.Store = (*Store)(nil)

// New returns a Checkpointer backed by a Store at dir, persisting full State.
func New(dir string) (*checkpointer.StoreCheckpointer, error) {
	st, err := NewStore(dir)
	if err != nil {
		return nil, err
	}
	return checkpointer.FromStore(st)
}
