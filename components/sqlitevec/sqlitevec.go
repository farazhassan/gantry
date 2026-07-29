package sqlitevec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/farazhassan/gantry/components/vectorstore"

	// NOTE: go-sqlite3 is version-pinned to match the bindings' embedded WASM build; see go.mod.
	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces" // WASM SQLite build with vec0 compiled in
	_ "github.com/ncruces/go-sqlite3/driver"             // database/sql driver named "sqlite3"
)

const defaultTable = "memories"

// tableName gates WithTable input: table names are interpolated into DDL/DML
// (SQLite cannot parameterize identifiers), so only identifier-shaped names
// are accepted.
var tableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Store is a vectorstore.Store backed by SQLite + the sqlite-vec extension.
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

// Add stores items in one transaction: the text row and its vector are
// inserted with the same rowid, so either both land or neither does.
// Vectors are serialized as JSON, which sqlite-vec accepts for float columns.
func (s *Store) Add(ctx context.Context, items ...vectorstore.Item) error {
	if len(items) == 0 {
		return nil
	}
	for i, it := range items {
		if len(it.Vector) != s.dim {
			return fmt.Errorf("sqlitevec: item %d has dimension %d, want %d", i, len(it.Vector), s.dim)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitevec: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insText := fmt.Sprintf(`INSERT INTO %s (text, metadata) VALUES (?, ?)`, s.table)
	insVec := fmt.Sprintf(`INSERT INTO %s_vec (rowid, embedding) VALUES (?, ?)`, s.table)
	for _, it := range items {
		var meta any // stays NULL when there is no metadata
		if len(it.Metadata) > 0 {
			b, err := json.Marshal(it.Metadata)
			if err != nil {
				return fmt.Errorf("sqlitevec: marshal metadata: %w", err)
			}
			meta = string(b)
		}
		res, err := tx.ExecContext(ctx, insText, it.Text, meta)
		if err != nil {
			return fmt.Errorf("sqlitevec: insert text: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("sqlitevec: last insert id: %w", err)
		}
		vec, err := json.Marshal(it.Vector)
		if err != nil {
			return fmt.Errorf("sqlitevec: marshal vector: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insVec, id, string(vec)); err != nil {
			return fmt.Errorf("sqlitevec: insert vector: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitevec: commit: %w", err)
	}
	return nil
}

// Search returns the k nearest items by cosine similarity. Score is
// 1 - cosine distance (higher = more similar). Hit.Vector is left nil.
func (s *Store) Search(ctx context.Context, vector []float32, k int) ([]vectorstore.Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	if len(vector) != s.dim {
		return nil, fmt.Errorf("sqlitevec: query has dimension %d, want %d", len(vector), s.dim)
	}
	vec, err := json.Marshal(vector)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec: marshal vector: %w", err)
	}
	q := fmt.Sprintf(`
		SELECT m.text, m.metadata, v.distance
		FROM %s_vec v
		JOIN %s m ON m.id = v.rowid
		WHERE v.embedding MATCH ? AND v.k = ?
		ORDER BY v.distance`, s.table, s.table)
	rows, err := s.db.QueryContext(ctx, q, string(vec), k)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec: search: %w", err)
	}
	defer rows.Close()

	var hits []vectorstore.Hit
	for rows.Next() {
		var text string
		var meta sql.NullString
		var distance float64
		if err := rows.Scan(&text, &meta, &distance); err != nil {
			return nil, fmt.Errorf("sqlitevec: scan: %w", err)
		}
		var md map[string]any
		if meta.Valid {
			if err := json.Unmarshal([]byte(meta.String), &md); err != nil {
				return nil, fmt.Errorf("sqlitevec: unmarshal metadata: %w", err)
			}
		}
		hits = append(hits, vectorstore.Hit{
			Item:  vectorstore.Item{Text: text, Metadata: md},
			Score: 1 - distance,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec: rows: %w", err)
	}
	return hits, nil
}
