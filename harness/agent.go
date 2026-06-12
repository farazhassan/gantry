package harness

import (
	"context"
	"errors"
	"fmt"
)

const defaultMaxIterations = 25

// namedMW pairs a middleware with a unique identifier so callers can
// insert other middleware relative to it via UseBefore / UseAfter.
type namedMW struct {
	name string
	mw   Middleware
}

// Agent is the configured runner. Build one with New and option functions.
type Agent struct {
	llm           LLMClient
	tracer        Tracer
	maxIterations int

	chains      map[Phase][]namedMW
	inner       map[Phase]Handler
	phases      []Phase
	anonCounter int
}

// Option configures an Agent during New.
type Option func(*Agent) error

// New returns a new Agent. WithLLM is required; all other options are optional.
func New(opts ...Option) (*Agent, error) {
	a := &Agent{
		maxIterations: defaultMaxIterations,
		chains:        map[Phase][]namedMW{},
		inner:         map[Phase]Handler{},
		phases:        DefaultPhases(),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(a); err != nil {
			return nil, err
		}
	}
	if a.llm == nil {
		return nil, errors.New("gantry: WithLLM is required")
	}
	return a, nil
}

// WithLLM supplies the required LLMClient.
func WithLLM(c LLMClient) Option {
	return func(a *Agent) error {
		if c == nil {
			return errors.New("gantry: WithLLM client is nil")
		}
		a.llm = c
		return nil
	}
}

// WithMaxIterations bounds how many loop iterations may run.
func WithMaxIterations(n int) Option {
	return func(a *Agent) error {
		if n <= 0 {
			return errors.New("gantry: WithMaxIterations must be positive")
		}
		a.maxIterations = n
		return nil
	}
}

// WithTracer sets a custom Tracer. When not provided, a default in-memory
// tracer is created per-run and writes to state.Trace.
//
// Note: a custom Tracer writes spans wherever its implementation directs.
// state.Trace will remain empty unless your Tracer also records into it.
// On error, the TraceCarrier returned by Run will carry state.Trace, which
// may be empty in this case. If you need both a custom Tracer AND a
// populated state.Trace, have your Tracer fan out (write to both).
func WithTracer(t Tracer) Option {
	return func(a *Agent) error {
		a.tracer = t
		return nil
	}
}

// Use appends an anonymous middleware to the phase chain.
// Registration order = innermost-first (see Compose).
//
// Anonymous middleware get an auto-generated unique name so UseBefore /
// UseAfter remain consistent.
func (a *Agent) Use(phase Phase, mw Middleware) {
	a.anonCounter++
	name := fmt.Sprintf("anon_%d", a.anonCounter)
	a.chains[phase] = append(a.chains[phase], namedMW{name: name, mw: mw})
}

// UseNamed appends a middleware with an explicit name.
// Returns an error if name already exists in the phase chain.
func (a *Agent) UseNamed(phase Phase, name string, mw Middleware) error {
	if a.findIndex(phase, name) >= 0 {
		return fmt.Errorf("gantry: middleware %q already registered on phase %q", name, phase)
	}
	a.chains[phase] = append(a.chains[phase], namedMW{name: name, mw: mw})
	return nil
}

// UseBefore inserts a named middleware immediately before the anchor.
// Returns an error if anchor is not found or newName already exists.
func (a *Agent) UseBefore(phase Phase, anchor, newName string, mw Middleware) error {
	idx := a.findIndex(phase, anchor)
	if idx < 0 {
		return fmt.Errorf("gantry: anchor %q not found on phase %q", anchor, phase)
	}
	if a.findIndex(phase, newName) >= 0 {
		return fmt.Errorf("gantry: middleware %q already registered on phase %q", newName, phase)
	}
	chain := a.chains[phase]
	a.chains[phase] = append(chain[:idx], append([]namedMW{{name: newName, mw: mw}}, chain[idx:]...)...)
	return nil
}

// UseAfter inserts a named middleware immediately after the anchor.
// Returns an error if anchor is not found or newName already exists.
func (a *Agent) UseAfter(phase Phase, anchor, newName string, mw Middleware) error {
	idx := a.findIndex(phase, anchor)
	if idx < 0 {
		return fmt.Errorf("gantry: anchor %q not found on phase %q", anchor, phase)
	}
	if a.findIndex(phase, newName) >= 0 {
		return fmt.Errorf("gantry: middleware %q already registered on phase %q", newName, phase)
	}
	chain := a.chains[phase]
	insertAt := idx + 1
	a.chains[phase] = append(chain[:insertAt], append([]namedMW{{name: newName, mw: mw}}, chain[insertAt:]...)...)
	return nil
}

func (a *Agent) findIndex(phase Phase, name string) int {
	for i, n := range a.chains[phase] {
		if n.name == name {
			return i
		}
	}
	return -1
}

// RegisterPhase inserts a new phase into the loop. Anchor must be an
// already-registered phase. Returns an error if anchor is missing or if
// the new phase already exists.
//
// Custom phases run in the same place on every iteration if they sit
// inside LoopPhases (between PhaseAssembleContext and PhaseObserve);
// they run once if they sit outside (before PhaseStart or after PhaseEnd,
// or anchored to those).
func (a *Agent) RegisterPhase(phase Phase, pos Position, anchor Phase) error {
	for _, p := range a.phases {
		if p == phase {
			return fmt.Errorf("gantry: phase %q already registered", phase)
		}
	}
	idx := -1
	for i, p := range a.phases {
		if p == anchor {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("gantry: anchor phase %q not found", anchor)
	}
	insertAt := idx
	if pos == PositionAfter {
		insertAt = idx + 1
	}
	a.phases = append(a.phases[:insertAt], append([]Phase{phase}, a.phases[insertAt:]...)...)
	return nil
}

// MaxIterations returns the loop iteration cap.
func (a *Agent) MaxIterations() int { return a.maxIterations }

// Tracer returns the configured tracer (may be nil before Run is called
// if no custom Tracer was supplied; the default tracer is created per-run).
func (a *Agent) Tracer() Tracer { return a.tracer }

// MiddlewareCount returns how many middleware are registered for a phase.
func (a *Agent) MiddlewareCount(phase Phase) int { return len(a.chains[phase]) }

// MiddlewareNames returns the registered middleware names for a phase, in order.
func (a *Agent) MiddlewareNames(phase Phase) []string {
	out := make([]string, len(a.chains[phase]))
	for i, n := range a.chains[phase] {
		out[i] = n.name
	}
	return out
}

// Run executes the agent loop until state.Done or MaxIterations is reached.
// The returned *State is always non-nil — even on error, the partial state is
// returned so callers can inspect the trace and state.DoneReason.
//
// Termination convention:
//
//   - Resource/normal stops — DoneNoToolCalls, DoneMaxIterations,
//     DoneBudgetExceeded — set state.Done and state.DoneReason and return a nil
//     error.
//   - Active blocks/aborts — DoneGuardrailBlocked, DoneHumanAborted — set
//     state.Done and state.DoneReason and additionally return a sentinel error
//     (ErrGuardrailBlocked, ErrHumanAborted) so callers can branch via
//     errors.Is.
//
// Inspect state.DoneReason for the terminal reason in all cases; use errors.Is
// for the blocking sentinels.
func (a *Agent) Run(ctx context.Context, input string) (*State, error) {
	state := NewState(input)

	// Resolve tracer: prefer the configured one; otherwise build a default
	// tracer that writes to state.Trace.
	tracer := a.tracer
	if tracer == nil {
		tracer = NewDefaultTracer(state.Trace)
	}

	wrap := func(err error) error {
		return wrapError(err, state.Trace)
	}

	if err := ctx.Err(); err != nil {
		return state, wrap(err)
	}

	// PhaseStart (once).
	if err := a.runPhase(ctx, tracer, PhaseStart, state); err != nil {
		return state, wrap(err)
	}

	for state.Iteration < a.maxIterations && !state.Done {
		for _, ph := range a.phases {
			if ph == PhaseStart || ph == PhaseEnd {
				continue
			}
			if state.Done {
				break
			}
			if err := ctx.Err(); err != nil {
				return state, wrap(err)
			}
			if err := a.runPhase(ctx, tracer, ph, state); err != nil {
				return state, wrap(err)
			}
		}
		state.Iteration++
	}

	if !state.Done {
		state.Done = true
		state.DoneReason = DoneMaxIterations
	}

	// PhaseEnd (once).
	if err := a.runPhase(ctx, tracer, PhaseEnd, state); err != nil {
		return state, wrap(err)
	}

	return state, nil
}

// runPhase resolves the inner handler for the phase, composes the chain,
// opens a span, and invokes the result.
func (a *Agent) runPhase(ctx context.Context, tracer Tracer, phase Phase, state *State) error {
	ctx, span := tracer.StartSpan(ctx, "phase:"+string(phase))
	span.SetAttr("iteration", state.Iteration)

	inner := a.resolveInner(phase)
	mws := make([]Middleware, len(a.chains[phase]))
	for i, n := range a.chains[phase] {
		mws[i] = n.mw
	}
	handler := Compose(inner, mws)
	err := handler(ctx, state)

	if state.Done {
		span.SetAttr("done", true)
		span.SetAttr("done_reason", string(state.DoneReason))
	}
	span.End(err)
	return err
}

// resolveInner returns the built-in inner handler for the given phase.
// In Plan 1, PhaseStart, PhaseLLMCall, PhasePostLLM, and PhaseObserve have
// non-noop inners. Other phases default to noop and rely entirely on
// middleware.
func (a *Agent) resolveInner(phase Phase) Handler {
	if h, ok := a.inner[phase]; ok {
		return h
	}
	switch phase {
	case PhaseStart:
		return DefaultStartHandler
	case PhaseLLMCall:
		return DefaultLLMCallHandler(a.llm)
	case PhasePostLLM:
		return DefaultPostLLMHandler
	case PhaseObserve:
		return DefaultObserveHandler
	default:
		return noopHandler
	}
}
