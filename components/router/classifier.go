package router

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// Classifier decides which route key should handle an input. recent optionally
// carries the tail of a prior conversation for context-aware classification;
// implementations may ignore it. A classifier that cannot decide returns an
// error wrapping ErrNoRoute so Chain can fall through to the next classifier.
type Classifier interface {
	Classify(ctx context.Context, input string, recent []gantry.Message) (string, error)
}

// ErrNoRoute reports that a classifier could not pick a route. Chain treats it
// as "try the next classifier"; any other error aborts classification.
var ErrNoRoute = errors.New("router: no route matched")

// Chain composes classifiers in priority order: each is tried in turn, falling
// through on ErrNoRoute; any other error aborts. Typical use is
// Chain(rules, llm) — cheap deterministic rules first, the LLM as backstop.
// When every classifier misses (or the chain is empty), Classify returns
// ErrNoRoute.
func Chain(classifiers ...Classifier) Classifier {
	return chain(classifiers)
}

type chain []Classifier

func (c chain) Classify(ctx context.Context, input string, recent []gantry.Message) (string, error) {
	for _, cl := range c {
		if cl == nil {
			continue
		}
		key, err := cl.Classify(ctx, input, recent)
		if err == nil {
			return key, nil
		}
		if errors.Is(err, ErrNoRoute) {
			continue
		}
		return "", err
	}
	return "", ErrNoRoute
}
