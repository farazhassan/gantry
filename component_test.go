package gantry

import (
	"errors"
	"testing"
)

// fakeComponent records installs and optionally fails.
type fakeComponent struct {
	name      string
	failWith  error
	installed *[]string
}

func (f fakeComponent) Install(a *Agent) error {
	if f.failWith != nil {
		return f.failWith
	}
	*f.installed = append(*f.installed, f.name)
	return nil
}

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := NewAgent(WithLLM(streamingStub{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return a
}

func TestWithInstallsInOrder(t *testing.T) {
	a := newTestAgent(t)
	var got []string
	err := a.With(
		fakeComponent{name: "a", installed: &got},
		fakeComponent{name: "b", installed: &got},
	)
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("install order = %v, want [a b]", got)
	}
}

func TestWithSkipsNil(t *testing.T) {
	a := newTestAgent(t)
	var got []string
	if err := a.With(nil, fakeComponent{name: "a", installed: &got}); err != nil {
		t.Fatalf("With: %v", err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want [a]", got)
	}
}

func TestWithStopsAtFirstError(t *testing.T) {
	a := newTestAgent(t)
	var got []string
	sentinel := errors.New("boom")
	err := a.With(
		fakeComponent{name: "a", installed: &got},
		fakeComponent{failWith: sentinel},
		fakeComponent{name: "c", installed: &got},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want only [a] before failure", got)
	}
}

func TestWithComponentsOption(t *testing.T) {
	var got []string
	_, err := NewAgent(
		WithLLM(streamingStub{}),
		WithComponents(fakeComponent{name: "a", installed: &got}),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want [a]", got)
	}
}

func TestWithComponentsOptionPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := NewAgent(
		WithLLM(streamingStub{}),
		WithComponents(fakeComponent{failWith: sentinel}),
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
