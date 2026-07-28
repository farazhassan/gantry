module github.com/farazhassan/gantry/components/sqlitevec

go 1.25.0

replace github.com/farazhassan/gantry => ../..

require (
	github.com/asg017/sqlite-vec-go-bindings v0.1.6
	// Pinned: sqlite-vec-go-bindings v0.1.6's embedded WASM build is incompatible
	// with go-sqlite3 >= v0.21 (see asg017/sqlite-vec-go-bindings#2, #5). Upgrade
	// only after upstream ships a rebuilt binary.
	github.com/ncruces/go-sqlite3 v0.20.3
)

require (
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/tetratelabs/wazero v1.8.2 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
