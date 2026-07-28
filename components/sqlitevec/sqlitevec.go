package sqlitevec

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	// NOTE: go-sqlite3 is version-pinned to match the bindings' embedded WASM build; see go.mod.
	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces" // WASM SQLite build with vec0 compiled in
	_ "github.com/ncruces/go-sqlite3/driver"             // database/sql driver named "sqlite3"
)

const defaultTable = "memories"

// tableName gates WithTable input: table names are interpolated into DDL/DML
// (SQLite cannot parameterize identifiers), so only identifier-shaped names
// are accepted.
var tableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Store is a semantic.Store backed by SQLite + the sqlite-vec extension.
// Vectors live in a vec0 virtual table (cosine distance); text and metadata
// live in a companion table linked by rowid. Safe for concurrent use.
type Store struct {
	db    *sql.DB
	table string
	dim   int
}

// Option configures a Store at construction.
type Option func(*Store)

// WithDim sets the vector dimensionality (required).
func WithDim(dim int) Option {
	return func(s *Store) { s.dim = dim }
}

// WithTable sets the base table name (default "memories"). The vec0 table is
// named "<table>_vec". Empty is ignored.
func WithTable(name string) Option {
	return func(s *Store) {
		if name != "" {
			s.table = name
		}
	}
}

// New opens (creating if needed) the database at path and ensures the schema
// exists, idempotently. path accepts a plain filepath, a file: URI, or
// ":memory:". WithDim is required; New panics on a missing dimension or a
// non-identifier table name (programmer errors). Runtime failures — opening
// the file, running DDL — return errors.
func New(path string, opts ...Option) (*Store, error) {
	s := &Store{table: defaultTable}
	for _, opt := range opts {
		opt(s)
	}
	if s.dim <= 0 {
		panic("sqlitevec: New requires WithDim")
	}
	if !tableName.MatchString(s.table) {
		panic(fmt.Sprintf("sqlitevec: invalid table name %q", s.table))
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec: open %s: %w", path, err)
	}
	// database/sql pools connections, and each connection to ":memory:" gets
	// its own private database. Pin the pool to one connection so all callers
	// see the same data.
	if strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory") {
		db.SetMaxOpenConns(1)
	}
	s.db = db
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	ddl := []string{
		fmt.Sprintf(
			`CREATE VIRTUAL TABLE IF NOT EXISTS %s_vec USING vec0(embedding float[%d] distance_metric=cosine)`,
			s.table, s.dim),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id         INTEGER PRIMARY KEY,
				text       TEXT NOT NULL,
				metadata   TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`, s.table),
	}
	for _, q := range ddl {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("sqlitevec: create schema: %w", err)
		}
	}
	return nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error { return s.db.Close() }
