package mailbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/taskmanager"
)

// newAgent builds an agent with a scripted LLM (turns plain-text replies) and
// the mailbox component over store.
func newAgent(t *testing.T, store taskmanager.NotificationStore, turns int) *gantry.Agent {
	t.Helper()
	responses := make([]gantry.LLMResponse, turns)
	for i := range responses {
		responses[i] = gantry.LLMResponse{Content: "ok"}
	}
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(responses...)),
		gantry.WithComponents(New(store)),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return a
}

// digests returns the mailbox system messages in msgs.
func digests(msgs []gantry.Message) []gantry.Message {
	var out []gantry.Message
	for _, m := range msgs {
		if m.Role == gantry.RoleSystem && strings.HasPrefix(m.Content, "While you were away:") {
			out = append(out, m)
		}
	}
	return out
}

func seedNote(t *testing.T, store taskmanager.NotificationStore, sessionID string) {
	t.Helper()
	err := store.Append(context.Background(), &taskmanager.Notification{
		ID: "n1", SessionID: sessionID, TaskID: "t1",
		Kind: taskmanager.NotificationDone, Title: "research", Body: "found 3 papers",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestMailboxInjectsDigestBeforeUserInput(t *testing.T) {
	store := taskmanager.NewInMemoryNotificationStore()
	seedNote(t, store, "s1")
	a := newAgent(t, store, 1)

	st := gantry.NewState("hello")
	st.Meta[gantry.MetaSessionID] = "s1"
	out, err := a.Resume(context.Background(), st)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	ds := digests(out.Messages)
	if len(ds) != 1 {
		t.Fatalf("digest count = %d, want 1", len(ds))
	}
	if !strings.Contains(ds[0].Content, "- [done] research: found 3 papers") {
		t.Errorf("digest = %q, want the [done] research bullet", ds[0].Content)
	}
	// The digest sits positionally BEFORE the turn's user input.
	if out.Messages[0].Role != gantry.RoleSystem || !strings.HasPrefix(out.Messages[0].Content, "While you were away:") {
		t.Errorf("Messages[0] = %+v, want the mailbox digest", out.Messages[0])
	}
	if out.Messages[1].Role != gantry.RoleUser || out.Messages[1].Content != "hello" {
		t.Errorf("Messages[1] = %+v, want the user input (not swallowed)", out.Messages[1])
	}
}

func TestMailboxDrainsSoSecondTurnInjectsNothing(t *testing.T) {
	store := taskmanager.NewInMemoryNotificationStore()
	seedNote(t, store, "s1")
	a := newAgent(t, store, 2)
	ctx := context.Background()

	st := gantry.NewState("hello")
	st.Meta[gantry.MetaSessionID] = "s1"
	first, err := a.Resume(ctx, st)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	second, err := a.RunFrom(ctx, first, "and again")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}

	if got := len(digests(second.Messages)); got != 1 {
		t.Errorf("digest count after two turns = %d, want 1 (drained; injected once)", got)
	}
}

func TestMailboxNoSessionIDIsNoOp(t *testing.T) {
	store := taskmanager.NewInMemoryNotificationStore()
	seedNote(t, store, "s1")
	a := newAgent(t, store, 1)

	out, err := a.Run(context.Background(), "hello") // fresh state: Meta has no session id
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(digests(out.Messages)); got != 0 {
		t.Errorf("digest count = %d, want 0 without a session id", got)
	}
	// The notification is still queued for its real session.
	left, _ := store.DrainFor(context.Background(), "s1")
	if len(left) != 1 {
		t.Errorf("store drained by a session-less run: %d left, want 1", len(left))
	}
}

func TestMailboxEmptyStoreIsNoOp(t *testing.T) {
	store := taskmanager.NewInMemoryNotificationStore()
	a := newAgent(t, store, 1)

	st := gantry.NewState("hello")
	st.Meta[gantry.MetaSessionID] = "s1"
	out, err := a.Resume(context.Background(), st)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := len(digests(out.Messages)); got != 0 {
		t.Errorf("digest count = %d, want 0 for an empty store", got)
	}
}

func TestMailboxSkipsTaskRuns(t *testing.T) {
	store := taskmanager.NewInMemoryNotificationStore()
	seedNote(t, store, "s1")
	a := newAgent(t, store, 1)

	// A task-driven run: the Driver seeds BOTH identity keys into State.Meta.
	st := gantry.NewState("task goal")
	st.Meta[gantry.MetaSessionID] = "s1"
	st.Meta[gantry.MetaTaskID] = "t1"
	out, err := a.Resume(context.Background(), st)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := len(digests(out.Messages)); got != 0 {
		t.Errorf("digest count = %d, want 0 inside a task run", got)
	}
	left, _ := store.DrainFor(context.Background(), "s1")
	if len(left) != 1 {
		t.Errorf("task run drained the chat session's mailbox: %d left, want 1", len(left))
	}
}

func TestNewNilStoreFailsInstall(t *testing.T) {
	_, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient()),
		gantry.WithComponents(New(nil)),
	)
	if err == nil {
		t.Errorf("NewAgent with mailbox.New(nil) = nil error, want an install error")
	}
}

func TestDigestFormatting(t *testing.T) {
	notes := []*taskmanager.Notification{
		{ID: "n1", TaskID: "t1", Kind: taskmanager.NotificationDone, Title: "research", Body: "found 3 papers"},
		{ID: "n2", TaskID: "t2", Kind: taskmanager.NotificationParked, Body: "Which color?"},
		{ID: "n3", TaskID: "t3", Kind: taskmanager.NotificationFailed, Title: "deploy"},
	}
	want := "While you were away:\n" +
		"- [done] research: found 3 papers\n" +
		"- [parked] t2: Which color?\n" +
		"- [failed] deploy"
	if got := digest(notes); got != want {
		t.Errorf("digest =\n%q\nwant\n%q", got, want)
	}
}
