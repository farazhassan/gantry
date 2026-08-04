module github.com/farazhassan/gantry/components/checkpointer/redis

go 1.24

replace github.com/farazhassan/gantry => ../../..

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/farazhassan/gantry v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.22.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)
