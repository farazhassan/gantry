package harness

// Phase identifies a stage in the agent loop. Users can define custom
// phases by declaring their own Phase constants and inserting them via
// Agent.RegisterPhase (Plan 2+).
type Phase string

// Position controls where a new phase is inserted relative to its anchor.
type Position int

const (
	PositionBefore Position = iota
	PositionAfter
)

// Default phases run in this order on every iteration except PhaseStart
// (once at the top) and PhaseEnd (once at the bottom).
const (
	PhaseStart           Phase = "start"
	PhaseAssembleContext Phase = "assemble_context"
	PhaseLLMCall         Phase = "llm_call"
	PhasePostLLM         Phase = "post_llm"
	PhaseToolExec        Phase = "tool_exec"
	PhaseObserve         Phase = "observe"
	PhaseEnd             Phase = "end"
)

// DefaultPhases returns the default phase order.
func DefaultPhases() []Phase {
	return []Phase{
		PhaseStart,
		PhaseAssembleContext,
		PhaseLLMCall,
		PhasePostLLM,
		PhaseToolExec,
		PhaseObserve,
		PhaseEnd,
	}
}

// LoopPhases returns the phases that run on every iteration (excludes
// PhaseStart and PhaseEnd).
func LoopPhases() []Phase {
	return []Phase{
		PhaseAssembleContext,
		PhaseLLMCall,
		PhasePostLLM,
		PhaseToolExec,
		PhaseObserve,
	}
}
