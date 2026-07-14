package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

// Router glues classification to dispatch: classify the input, look up the
// winning agent in the registry, run it.
//
// Routing lives at the caller level by design — gantry's phase loop binds
// exactly one LLMClient when it resolves PhaseLLMCall (Agent.resolveInner →
// DefaultLLMCallHandler(a.llm)), so choosing between agents has to happen
// above Run/RunFrom, not inside the loop.
type Router struct {
	classifier Classifier
	registry   *Registry
}

// New pairs a classifier with a route table.
func New(classifier Classifier, reg *Registry) (*Router, error) {
	if classifier == nil {
		return nil, errors.New("router: New requires a non-nil classifier")
	}
	if reg == nil || len(reg.Keys()) == 0 {
		return nil, errors.New("router: New requires a registry with at least one route")
	}
	return &Router{classifier: classifier, registry: reg}, nil
}

// Run classifies input, then delegates to the selected agent's Run. It returns
// the winning route key alongside the agent's terminal state. When
// classification fails the returned state is nil; once dispatched, the state
// and error follow the selected agent's Run termination contract (the state is
// non-nil even on error).
func (r *Router) Run(ctx context.Context, input string) (string, *gantry.State, error) {
	key, agent, err := r.pick(ctx, input, nil)
	if err != nil {
		return key, nil, err
	}
	st, err := agent.Run(ctx, input)
	return key, st, err
}

// RunFrom classifies input with the prior transcript as context, then
// delegates to the selected agent's RunFrom seeded with prior. A nil prior
// behaves like Run. The transcript carries across agents: a turn routed to a
// different agent than the last one continues the same conversation under the
// new agent's configuration.
func (r *Router) RunFrom(ctx context.Context, prior *gantry.State, input string) (string, *gantry.State, error) {
	var recent []gantry.Message
	if prior != nil {
		recent = prior.Messages
	}
	key, agent, err := r.pick(ctx, input, recent)
	if err != nil {
		return key, nil, err
	}
	st, err := agent.RunFrom(ctx, prior, input)
	return key, st, err
}

// pick runs the classifier and resolves the agent, guarding against a
// classifier that returns a key missing from the registry.
func (r *Router) pick(ctx context.Context, input string, recent []gantry.Message) (string, *gantry.Agent, error) {
	key, err := r.classifier.Classify(ctx, input, recent)
	if err != nil {
		return "", nil, err
	}
	agent, ok := r.registry.Get(key)
	if !ok {
		return key, nil, fmt.Errorf("router: classifier chose unregistered route %q", key)
	}
	return key, agent, nil
}
