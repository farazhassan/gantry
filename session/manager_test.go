package session_test

import (
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

func newTestAgent(t *testing.T, responses ...harness.LLMResponse) *harness.Agent {
	t.Helper()
	if len(responses) == 0 {
		responses = []harness.LLMResponse{{Content: "ok", StopReason: harness.StopReasonEnd}}
	}
	a, err := harness.NewAgent(harness.WithLLM(eval.NewMockLLMClient(responses...)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestNewManagerNilAgentPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewManager(nil, store): want panic")
		}
	}()
	session.NewManager(nil, checkpointer.NewInMemory())
}

func TestNewManagerNilStorePanics(t *testing.T) {
	a := newTestAgent(t)
	defer func() {
		if recover() == nil {
			t.Error("NewManager(agent, nil): want panic")
		}
	}()
	session.NewManager(a, nil)
}

func TestManagerSessionGetOrCreate(t *testing.T) {
	mgr := session.NewManager(newTestAgent(t), checkpointer.NewInMemory())

	s1 := mgr.Session("alice")
	s1again := mgr.Session("alice")
	s2 := mgr.Session("bob")

	if s1 != s1again {
		t.Error("Session(\"alice\") returned different handles; want the same cached handle")
	}
	if s1 == s2 {
		t.Error("Session(\"alice\") and Session(\"bob\") returned the same handle; want distinct")
	}
	if s1.ID() != "alice" {
		t.Errorf("ID() = %q, want alice", s1.ID())
	}
}
