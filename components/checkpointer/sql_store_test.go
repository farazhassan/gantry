package checkpointer

import "testing"

func TestSQLStore_DefaultSQLiteStatements(t *testing.T) {
	s := NewSQLStore(nil)
	if got, want := s.upsertSQL(),
		"INSERT INTO checkpoints (id, state) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET state = excluded.state"; got != want {
		t.Errorf("upsert:\n got=%q\nwant=%q", got, want)
	}
	if got, want := s.selectSQL(), "SELECT state FROM checkpoints WHERE id = ?"; got != want {
		t.Errorf("select:\n got=%q\nwant=%q", got, want)
	}
	if got, want := s.CreateTableSQL(),
		"CREATE TABLE IF NOT EXISTS checkpoints (id TEXT PRIMARY KEY, state BLOB NOT NULL)"; got != want {
		t.Errorf("ddl:\n got=%q\nwant=%q", got, want)
	}
}

func TestSQLStore_TableAndColumnInjected(t *testing.T) {
	s := NewSQLStore(nil, WithTable("gantry_checkpoints"), WithColumn("blob"))
	if got, want := s.selectSQL(), "SELECT blob FROM gantry_checkpoints WHERE id = ?"; got != want {
		t.Errorf("select:\n got=%q\nwant=%q", got, want)
	}
}

func TestSQLStore_PostgresDialect(t *testing.T) {
	s := NewSQLStore(nil, WithDialect(Postgres))
	if got, want := s.upsertSQL(),
		"INSERT INTO checkpoints (id, state) VALUES ($1, $2) ON CONFLICT(id) DO UPDATE SET state = excluded.state"; got != want {
		t.Errorf("upsert:\n got=%q\nwant=%q", got, want)
	}
	if got, want := s.selectSQL(), "SELECT state FROM checkpoints WHERE id = $1"; got != want {
		t.Errorf("select:\n got=%q\nwant=%q", got, want)
	}
}
