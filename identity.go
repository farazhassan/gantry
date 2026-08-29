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
// empty when unknown (no WithName, no task meta). ParentRunID/
// ParentToolCallID are set only for a run started via WithParentLink (a
// nested sub-agent run) — empty for a top-level run.
type eventIdentity struct {
	runID     string
	sessionID string
	taskID    string
	agent     string

	parentRunID      string
	parentToolCallID string

	parentToolName        string
	parentToolDescription string
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

// ParentLink identifies the run and specific tool call that is about to
// spawn a nested run. Set it on ctx with WithParentLink before calling
// Agent.Run (or RunStream) for the child; the child's own a.run folds it
// into the nested run's minted eventIdentity, so every event that run
// emits carries ParentRunID/ParentToolCallID back to the spawning call.
// Name/Description are the spawning tool's own identity (e.g.
// components/subagent.New's name/description) — what THIS run is, as a
// sub-agent, not anything about the parent itself.
type ParentLink struct {
	RunID      string
	ToolCallID string

	Name        string
	Description string
}

// parentLinkKey is the context key carrying an optional ParentLink.
type parentLinkKey struct{}

// WithParentLink returns a ctx carrying link, read once by the next
// Agent.Run/RunStream call made with this ctx. Used by components/subagent
// to record which run and tool call spawned a nested run.
func WithParentLink(ctx context.Context, link ParentLink) context.Context {
	return context.WithValue(ctx, parentLinkKey{}, link)
}

// parentLinkFrom extracts the ParentLink carried by ctx, if any.
func parentLinkFrom(ctx context.Context) (ParentLink, bool) {
	link, ok := ctx.Value(parentLinkKey{}).(ParentLink)
	return link, ok
}

// clearParentLink returns a ctx with any ParentLink shadowed by an empty
// value. Called by a.run immediately after folding an ambient ParentLink
// into this run's own identity, so a deeper nested run started from this
// ctx -- without an explicit new WithParentLink call -- sees no link at
// all, rather than silently inheriting this run's own (now-stale) parent
// attribution. This is what makes WithParentLink's "read once" doc comment
// actually true rather than aspirational.
func clearParentLink(ctx context.Context) context.Context {
	return context.WithValue(ctx, parentLinkKey{}, ParentLink{})
}

// CurrentIdentity reports the RunID and Agent name of the run ctx is
// currently executing under, if any. Exported so components in other
// packages (e.g. components/subagent) can capture the calling run's
// identity before starting a nested run, without access to the unexported
// eventIdentity type.
func CurrentIdentity(ctx context.Context) (runID, agent string, ok bool) {
	id, ok := identityFrom(ctx)
	if !ok {
		return "", "", false
	}
	return id.runID, id.agent, true
}
