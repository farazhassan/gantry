package router_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/router"
)

// classifierFunc adapts a plain func to router.Classifier for stubbing.
type classifierFunc func(ctx context.Context, input string, recent []gantry.Message) (string, error)

func (f classifierFunc) Classify(ctx context.Context, input string, recent []gantry.Message) (string, error) {
	return f(ctx, input, recent)
}

// prefixRule claims inputs starting with prefix for key.
func prefixRule(prefix, key string) router.Rule {
	return func(_ context.Context, input string) (string, bool) {
		if strings.HasPrefix(input, prefix) {
			return key, true
		}
		return "", false
	}
}

func TestRuleRouterFirstMatchWins(t *testing.T) {
	r := router.NewRuleRouter(
		prefixRule("bill:", "billing"),
		prefixRule("bill", "support"), // would also match; must not win
	)
	key, err := r.Classify(context.Background(), "bill: refund order 7", nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if key != "billing" {
		t.Errorf("key = %q, want billing (first matching rule)", key)
	}
}

func TestRuleRouterNoMatchIsErrNoRoute(t *testing.T) {
	r := router.NewRuleRouter(prefixRule("bill:", "billing"))
	_, err := r.Classify(context.Background(), "write a poem", nil)
	if !errors.Is(err, router.ErrNoRoute) {
		t.Errorf("err = %v, want ErrNoRoute", err)
	}
}

func TestRuleRouterRouteAppends(t *testing.T) {
	r := router.NewRuleRouter().
		Route(prefixRule("bill:", "billing")).
		Route(prefixRule("help:", "support"))
	key, err := r.Classify(context.Background(), "help: reset password", nil)
	if err != nil || key != "support" {
		t.Errorf("Classify = (%q, %v), want (support, nil)", key, err)
	}
}

func TestChainTriesRulesFirst(t *testing.T) {
	second := 0
	c := router.Chain(
		router.NewRuleRouter(prefixRule("bill:", "billing")),
		classifierFunc(func(context.Context, string, []gantry.Message) (string, error) {
			second++
			return "support", nil
		}),
	)
	key, err := c.Classify(context.Background(), "bill: refund", nil)
	if err != nil || key != "billing" {
		t.Fatalf("Classify = (%q, %v), want (billing, nil)", key, err)
	}
	if second != 0 {
		t.Errorf("second classifier consulted %d times, want 0", second)
	}
}

func TestChainFallsThroughOnErrNoRoute(t *testing.T) {
	c := router.Chain(
		router.NewRuleRouter(), // no rules: always ErrNoRoute
		classifierFunc(func(context.Context, string, []gantry.Message) (string, error) {
			return "support", nil
		}),
	)
	key, err := c.Classify(context.Background(), "anything", nil)
	if err != nil || key != "support" {
		t.Errorf("Classify = (%q, %v), want (support, nil)", key, err)
	}
}

func TestChainPropagatesHardError(t *testing.T) {
	boom := errors.New("boom")
	c := router.Chain(
		classifierFunc(func(context.Context, string, []gantry.Message) (string, error) {
			return "", boom
		}),
		classifierFunc(func(context.Context, string, []gantry.Message) (string, error) {
			t.Error("second classifier consulted after a hard error")
			return "support", nil
		}),
	)
	_, err := c.Classify(context.Background(), "anything", nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestChainAllMissIsErrNoRoute(t *testing.T) {
	c := router.Chain(router.NewRuleRouter())
	if _, err := c.Classify(context.Background(), "x", nil); !errors.Is(err, router.ErrNoRoute) {
		t.Errorf("err = %v, want ErrNoRoute", err)
	}
	empty := router.Chain()
	if _, err := empty.Classify(context.Background(), "x", nil); !errors.Is(err, router.ErrNoRoute) {
		t.Errorf("empty chain err = %v, want ErrNoRoute", err)
	}
}
