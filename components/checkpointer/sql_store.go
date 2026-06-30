package checkpointer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Dialect describes a SQL flavour for SQLStore: positional placeholder rendering.
// Both shipped dialects use ON CONFLICT upsert (SQLite >= 3.24, Postgres >= 9.5).
type Dialect struct {
	name        string
	placeholder func(n int) string // 1-based
}

var (
	// SQLite uses ? placeholders.
	SQLite = Dialect{name: "sqlite", placeholder: func(int) string { return "?" }}
	// Postgres uses $N placeholders.
	Postgres = Dialect{name: "postgres", placeholder: func(n int) string { return fmt.Sprintf("$%d", n) }}
)

// SQLStore persists blobs in a single SQL table (id PRIMARY KEY, <column>). It
// owns no schema migration; callers create the table (see CreateTableSQL). Only
// database/sql (stdlib) is imported — the driver is supplied by the caller's *sql.DB.
type SQLStore struct {
	db      *sql.DB
	table   string
	column  string
	dialect Dialect
}

// SQLStoreOption configures a SQLStore.
type SQLStoreOption func(*SQLStore)

// WithTable sets the table name (default "checkpoints").
func WithTable(name string) SQLStoreOption { return func(s *SQLStore) { s.table = name } }

// WithColumn sets the blob column name (default "state").
func WithColumn(name string) SQLStoreOption { return func(s *SQLStore) { s.column = name } }

// WithDialect sets the SQL dialect (default SQLite).
func WithDialect(d Dialect) SQLStoreOption { return func(s *SQLStore) { s.dialect = d } }

// NewSQLStore returns a SQLStore over db. db may be nil only for tests that read
// the generated SQL without executing it.
func NewSQLStore(db *sql.DB, opts ...SQLStoreOption) *SQLStore {
	s := &SQLStore{db: db, table: "checkpoints", column: "state", dialect: SQLite}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *SQLStore) upsertSQL() string {
	return fmt.Sprintf(
		"INSERT INTO %s (id, %s) VALUES (%s, %s) ON CONFLICT(id) DO UPDATE SET %s = excluded.%s",
		s.table, s.column, s.dialect.placeholder(1), s.dialect.placeholder(2), s.column, s.column)
}

func (s *SQLStore) selectSQL() string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE id = %s", s.column, s.table, s.dialect.placeholder(1))
}

// CreateTableSQL returns DDL to create the backing table. Convenience for callers;
// SQLStore never runs it.
func (s *SQLStore) CreateTableSQL() string {
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, %s BLOB NOT NULL)", s.table, s.column)
}

func (s *SQLStore) Put(ctx context.Context, id string, blob []byte) error {
	_, err := s.db.ExecContext(ctx, s.upsertSQL(), id, blob)
	return err
}

func (s *SQLStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, s.selectSQL(), id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return blob, true, nil
}

var _ Store = (*SQLStore)(nil)
