package session

import "errors"

// ErrSaveFailed wraps a store Save failure from Session.Run. The terminal State
// is still returned alongside it so the caller can decide whether to retry,
// alert, or proceed — but the turn was NOT persisted, so the next turn will not
// see it. Detect with errors.Is.
var ErrSaveFailed = errors.New("gantry/session: save failed")

// ErrHandoffLoop reports that one turn chained more consecutive transfer
// handoffs than allowed (see maxConsecutiveHandoffs in session.go). The last
// DoneHandoff state is returned alongside it, UNSAVED — the checkpoint stays
// at the previous turn. Detect with errors.Is.
var ErrHandoffLoop = errors.New("gantry/session: handoff loop")

// ErrHandoffTargetUnknown reports that the configured Resolver returned nil
// for a transfer handoff's target. The DoneHandoff state is returned
// alongside it, UNSAVED. Detect with errors.Is.
var ErrHandoffTargetUnknown = errors.New("gantry/session: unknown handoff target")
