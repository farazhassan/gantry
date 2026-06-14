package session

import "errors"

// ErrSaveFailed wraps a store Save failure from Session.Run. The terminal State
// is still returned alongside it so the caller can decide whether to retry,
// alert, or proceed — but the turn was NOT persisted, so the next turn will not
// see it. Detect with errors.Is.
var ErrSaveFailed = errors.New("gantry/session: save failed")
