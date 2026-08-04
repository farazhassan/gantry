package sql

import (
	"context"
	gosql "database/sql"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Dialect describes a SQL flavour for Store: positional placeholder rendering.
// Both shipped dialects use ON CONFLICT upsert (SQLite >= 3.24, Postgres >= 9.5).
type Dialect struct {
	placeholder func(n int) string // 1-based
}

var (
	// SQLite uses ? placeholders.
	SQLite = Dialect{placeholder: func(int) string { return "?" }}
	// Postgres uses $N placeholders.
	Postgres = Dialect{placeholder: func(n int) string { return fmt.Sprintf("$%d", n) }}
)

// Store persists blobs in a single SQL table (id PRIMARY KEY, <column>). It owns
// no schema migration; callers create the table (see CreateTableSQL). Only
// database/sql (stdlib) is imported — the driver is supplied by the caller's
// *sql.DB, so this package needs no third-party dependency of its own.
type Store struct {
	db      *gosql.DB
	table   string
	column  string
	dialect Dialect
}

// Option configures a Store.
type Option func(*Store)

// WithTable sets the table name (default "checkpoints"). The name is interpolated
// directly into SQL (identifiers cannot be parameterized), so it must come from
// trusted code, never from user input.
func WithTable(name string) Option { return func(s *Store) { s.table = name } }

// WithColumn sets the blob column name (default "state"). Like WithTable, the name
// is interpolated directly into SQL and must come from trusted code, not user input.
func WithColumn(name string) Option { return func(s *Store) { s.column = name } }

// WithDialect sets the SQL dialect (default SQLite).
func WithDialect(d Dialect) Option { return func(s *Store) { s.dialect = d } }

// New returns a Store over db. db may be nil only for tests that read the
// generated SQL without executing it.
func New(db *gosql.DB, opts ...Option) *Store {
	s := &Store{db: db, table: "checkpoints", column: "state", dialect: SQLite}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Store) upsertSQL() string {
	return fmt.Sprintf(
		"INSERT INTO %s (id, %s) VALUES (%s, %s) ON CONFLICT(id) DO UPDATE SET %s = excluded.%s",
		s.table, s.column, s.dialect.placeholder(1), s.dialect.placeholder(2), s.column, s.column)
}

func (s *Store) selectSQL() string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE id = %s", s.column, s.table, s.dialect.placeholder(1))
}

// CreateTableSQL returns DDL to create the backing table. Convenience for callers;
// Store never runs it.
func (s *Store) CreateTableSQL() string {
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, %s BLOB NOT NULL)", s.table, s.column)
}

func (s *Store) Put(ctx context.Context, id string, blob []byte) error {
	_, err := s.db.ExecContext(ctx, s.upsertSQL(), id, blob)
	return err
}

func (s *Store) Get(ctx context.Context, id string) ([]byte, bool, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, s.selectSQL(), id).Scan(&blob)
	if errors.Is(err, gosql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return blob, true, nil
}

var _ checkpointer.Store = (*Store)(nil)
