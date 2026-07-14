package router

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Rule inspects an input and either claims it (key, true) or passes (_, false).
// Rules are cheap, deterministic functions — prefix checks, regexes, exact
// commands — evaluated before any LLM is consulted.
type Rule func(ctx context.Context, input string) (key string, ok bool)

// RuleRouter classifies with an ordered list of Rules; the first rule to claim
// the input wins. It implements Classifier and ignores the recent transcript.
type RuleRouter struct {
	rules []Rule
}

// compile-time check: RuleRouter implements Classifier.
var _ Classifier = (*RuleRouter)(nil)

// NewRuleRouter builds a RuleRouter from zero or more rules.
func NewRuleRouter(rules ...Rule) *RuleRouter {
	return &RuleRouter{rules: rules}
}

// Route appends a rule and returns the router for chained registration.
// A nil rule is ignored.
func (r *RuleRouter) Route(rule Rule) *RuleRouter {
	if rule != nil {
		r.rules = append(r.rules, rule)
	}
	return r
}

// Classify tries each rule in registration order and returns the first claimed
// key. When no rule claims the input it returns ErrNoRoute, so a Chain can
// fall through to an LLM classifier.
func (r *RuleRouter) Classify(ctx context.Context, input string, _ []gantry.Message) (string, error) {
	for _, rule := range r.rules {
		if rule == nil {
			continue
		}
		if key, ok := rule(ctx, input); ok {
			return key, nil
		}
	}
	return "", ErrNoRoute
}
