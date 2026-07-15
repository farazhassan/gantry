package gantry

import "context"

// Canonical State.Meta keys under which the task layer records which task and
// session a run belongs to. They live here (not in package task) because the
// core run loop reads them when stamping event identity, and package gantry
// must never import package task (import cycle). Package task re-declares
// task.MetaTaskID / task.MetaSessionID as aliases of these constants, so every
// existing task-layer caller keeps compiling unchanged. Values are strings.
const (
	MetaTaskID    = "task.id"
	MetaSessionID = "task.session_id"
)

// eventIdentity is the per-run identity minted once by a.run: emit stamps it
// onto every Event, and the run span records it as attributes. Fields are
// empty when unknown (no WithName, no task meta).
type eventIdentity struct {
	runID     string
	sessionID string
	taskID    string
	agent     string
}

// identityCtxKey is the context key under which the run's identity is stored.
// Mirrors sinkKey in stream.go.
type identityCtxKey struct{}

// withIdentity returns a ctx carrying the run's identity so emit — called deep
// inside handlers and middleware — can stamp events without threading identity
// through every signature.
func withIdentity(ctx context.Context, id eventIdentity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// identityFrom extracts the run identity from ctx, reporting whether one is
// set. A nested a.run overwrites the parent's identity in its child ctx, so
// events that (deliberately) share a sink still attribute to their own run.
func identityFrom(ctx context.Context) (eventIdentity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(eventIdentity)
	return id, ok
}

// newRunID mints a run identifier: "run-" plus 8 random bytes hex-encoded
// (16 hex chars). It reuses the tracer's newID source (default_tracer.go) so
// ids stay collision-safe with no third-party dependency.
func newRunID() string {
	return "run-" + newID()
}
