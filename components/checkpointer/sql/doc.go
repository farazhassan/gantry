// Package sql implements checkpointer.Store on any database/sql (stdlib)
// connection — SQLite, Postgres, or another dialect via Dialect.
//
// Unlike components/checkpointer/redis and components/sqlitevec, this package
// needs no third-party dependency: the caller supplies an already-connected
// *sql.DB (via whichever driver they choose), so it lives in the root module
// rather than its own. Wire it into an agent with components/checkpointer:
//
//	db, err := gosql.Open("sqlite3", "gantry.db")
//	// handle err
//	s := sql.New(db)
//	if _, err := db.Exec(s.CreateTableSQL()); err != nil {
//		// handle err
//	}
//	cp, err := checkpointer.FromStore(s)
//	// handle err
//	err = agent.With(checkpointer.New(cp, "session-id"))
//
// Store owns no schema migration beyond the DDL returned by CreateTableSQL —
// callers run it (or their own migration) before first use.
package sql
