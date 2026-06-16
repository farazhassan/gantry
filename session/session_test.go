package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/session"
)

func resp(content string, in, out int) gantry.LLMResponse {
	return gantry.LLMResponse{
		Content:    content,
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: in, OutputTokens: out},
	}
}

// fakeStore lets tests inject Load/Save errors. With no injected error and
// nothing saved, Load reports ErrNotFound (the first-turn signal).
type fakeStore struct {
	loadErr error
	saveErr error
	saved   *gantry.State
}

func (f *fakeStore) Save(_ context.Context, _ string, s *gantry.State) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := *s
	f.saved = &cp
	return nil
}

func (f *fakeStore) Load(_ context.Context, id string) (*gantry.State, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	if f.saved == nil {
		return nil, checkpointer.ErrNotFound
	}
	cp := *f.saved
	return &cp, nil
}

func TestSessionMultiTurnContinuity(t *testing.T) {
	a := newTestAgent(t, resp("nice to meet you", 10, 5), resp("your name is Faraz", 10, 5))
	mgr := session.NewManager(a, checkpointer.NewInMemory())
	s := mgr.Session("user-1")
	ctx := context.Background()

	t1, err := s.Run(ctx, "my name is Faraz")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if len(t1.Messages) != 2 {
		t.Fatalf("turn 1 len(Messages) = %d, want 2", len(t1.Messages))
	}

	t2, err := s.Run(ctx, "what is my name?")
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	// Continuity: turn 2 carries turn 1's transcript.
	if len(t2.Messages) != 4 {
		t.Fatalf("turn 2 len(Messages) = %d, want 4", len(t2.Messages))
	}
	if t2.Messages[0].Content != "my name is Faraz" {
		t.Errorf("turn 2 first message = %q, want turn 1's user input", t2.Messages[0].Content)
	}
	// Cumulative usage across turns.
	if t2.Usage.InputTokens != 20 {
		t.Errorf("turn 2 Usage.InputTokens = %d, want 20 (cumulative)", t2.Usage.InputTokens)
	}
}

func TestSessionHistory(t *testing.T) {
	a := newTestAgent(t, resp("hi there", 1, 1))
	mgr := session.NewManager(a, checkpointer.NewInMemory())
	s := mgr.Session("user-1")
	ctx := context.Background()

	// Before any turn: empty history, no error.
	h0, err := s.History(ctx)
	if err != nil {
		t.Fatalf("History before any turn: %v", err)
	}
	if len(h0) != 0 {
		t.Errorf("History before any turn = %d messages, want 0", len(h0))
	}

	if _, err := s.Run(ctx, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h1, err := s.History(ctx)
	if err != nil {
		t.Fatalf("History after turn: %v", err)
	}
	if len(h1) != 2 {
		t.Errorf("History after one turn = %d messages, want 2", len(h1))
	}
}

func TestSessionDurableResumeViaSecondManager(t *testing.T) {
	a := newTestAgent(t, resp("turn1", 10, 5), resp("turn2", 10, 5))
	store := checkpointer.NewInMemory()
	ctx := context.Background()

	mgr1 := session.NewManager(a, store)
	if _, err := mgr1.Session("user-9").Run(ctx, "first"); err != nil {
		t.Fatalf("mgr1 turn: %v", err)
	}

	// A brand-new Manager over the SAME store + id continues transparently.
	mgr2 := session.NewManager(a, store)
	t2, err := mgr2.Session("user-9").Run(ctx, "second")
	if err != nil {
		t.Fatalf("mgr2 turn: %v", err)
	}
	if len(t2.Messages) != 4 {
		t.Errorf("resumed turn len(Messages) = %d, want 4 (history carried via store)", len(t2.Messages))
	}
	if t2.Messages[0].Content != "first" {
		t.Errorf("resumed first message = %q, want \"first\"", t2.Messages[0].Content)
	}
	if t2.Usage.InputTokens != 20 {
		t.Errorf("resumed Usage.InputTokens = %d, want 20 (cumulative across managers)", t2.Usage.InputTokens)
	}
}

func TestSessionLoadNotFoundIsFirstTurn(t *testing.T) {
	a := newTestAgent(t, resp("ok", 1, 1))
	// fakeStore with nothing saved => Load returns ErrNotFound.
	mgr := session.NewManager(a, &fakeStore{})
	st, err := mgr.Session("new").Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first turn on empty store: %v", err)
	}
	if st.FinalOutput != "ok" {
		t.Errorf("FinalOutput = %q, want ok", st.FinalOutput)
	}
}

func TestSessionLoadErrorIsReturned(t *testing.T) {
	a := newTestAgent(t, resp("should not run", 0, 0))
	boom := errors.New("backend down")
	mgr := session.NewManager(a, &fakeStore{loadErr: boom})

	st, err := mgr.Session("x").Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("want error from failed Load, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap the backend error", err)
	}
	if st != nil {
		t.Errorf("state = %+v, want nil when Load fails before running", st)
	}
}

func TestSessionSaveErrorIsSurfaced(t *testing.T) {
	a := newTestAgent(t, resp("answer", 1, 1))
	mgr := session.NewManager(a, &fakeStore{saveErr: errors.New("disk full")})

	st, err := mgr.Session("x").Run(context.Background(), "hello")
	if !errors.Is(err, session.ErrSaveFailed) {
		t.Errorf("error = %v, want errors.Is(..., ErrSaveFailed)", err)
	}
	if st == nil {
		t.Error("state is nil; want the terminal state returned alongside ErrSaveFailed")
	} else if st.FinalOutput != "answer" {
		t.Errorf("FinalOutput = %q, want answer (run completed, only save failed)", st.FinalOutput)
	}
}
