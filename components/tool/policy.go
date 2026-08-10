package tool

// BatchFailureMode controls what happens to the other pending tool calls in
// a parallel dispatch batch when one of them fails.
type BatchFailureMode int

const (
	// KeepGoing lets every pending call run to completion regardless of
	// sibling failures. This is the default (zero value) and matches
	// gantry's historical dispatch behavior.
	KeepGoing BatchFailureMode = iota
	// StopWaitInFlight stops launching calls that haven't started yet, but
	// lets calls already in flight finish naturally.
	StopWaitInFlight
	// StopCancelInFlight stops launching calls that haven't started yet and
	// cancels the context passed to calls already in flight.
	StopCancelInFlight
)

// FailureDisposition controls what happens to the run once a dispatch batch
// has resolved with at least one failure.
type FailureDisposition int

const (
	// FeedbackToLLM records the failure as an error ToolResult and lets the
	// run continue so the LLM can see it and decide what to do next. This is
	// the default (zero value) and matches gantry's historical dispatch
	// behavior.
	FeedbackToLLM FailureDisposition = iota
	// HarnessStop aborts the run: Agent.Run returns an error wrapping
	// gantry.ErrToolPolicyAborted and state.DoneReason is set to
	// gantry.DoneToolPolicyAborted.
	HarnessStop
)

// Policy configures how a components/tool dispatch batch behaves when one
// or more tool calls fail. The zero value (Policy{}) reproduces gantry's
// historical behavior: full parallelism, every call runs to completion, and
// failures are fed back to the LLM as error ToolResults.
//
// A tool call whose error is wrapped with gantry.ErrToolAuth or
// gantry.ErrToolPersistent always forces OnFailure = StopCancelInFlight and
// Disposition = HarnessStop for that batch, regardless of the configured
// Policy: retrying or continuing past a persistent failure is assumed
// pointless.
type Policy struct {
	// Parallelism caps how many tool calls run concurrently. <= 0 means
	// full parallelism (every pending call runs at once), matching
	// gantry.RunParallel's own convention.
	Parallelism int
	// OnFailure controls sibling calls within the same batch. Defaults to
	// KeepGoing.
	OnFailure BatchFailureMode
	// Disposition controls whether the run continues after the batch
	// resolves. Defaults to FeedbackToLLM.
	Disposition FailureDisposition
}
