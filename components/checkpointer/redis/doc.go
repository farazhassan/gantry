// Package redis implements checkpointer.Store on Redis via
// github.com/redis/go-redis/v9.
//
// This package lives in its own Go module so the root gantry module stays
// dependency-free. Wire it into an agent with components/checkpointer:
//
//	rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
//	cp, err := checkpointer.FromStore(redis.New(rdb))
//	// handle err
//	err = agent.With(checkpointer.New(cp, "session-id"))
//
// Callers own the goredis.Cmdable connection (client, cluster, or ring) —
// this package only issues GET/SET against it, so TLS, auth, pooling, and
// lifecycle are entirely the caller's responsibility.
package redis
