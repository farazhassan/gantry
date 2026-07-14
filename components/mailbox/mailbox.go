// Package mailbox surfaces background-task notifications in a chat session.
// It installs PhaseStart middleware that, once per run, peeks the session's
// pending Notifications from a taskmanager.NotificationStore and prepends ONE
// system message ("While you were away:" plus a bullet per notification) ahead
// of the turn's user input, plus PhaseEnd middleware that acks the delivered
// notifications once the run completes. Pair it with taskmanager.NotifyBridge
// on the Dispatcher: the bridge writes, the mailbox delivers.
//
// Delivery is at-least-once: PhaseEnd runs only when the run completes, so a
// run that errors mid-turn (e.g. a provider failure) never acks — the peeked
// notifications stay in the store and are redelivered on the session's next
// run. A duplicate digest is possible only when a completed turn's ack fails
// (the store call errors, or the process dies between run completion and the
// ack); a lost digest is possible only if the caller persists the turn and
// that persistence fails after the ack.
//
// Install it on the user-facing chat agent. Runs without a session id in
// State.Meta (gantry.MetaSessionID), runs that are themselves task-driven
// (gantry.MetaTaskID present — the task Driver seeds both keys), and runs with
// an empty mailbox are all no-ops.
package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/taskmanager"
)

type component struct {
	store taskmanager.NotificationStore
}

// ackMetaKey is the State.Meta key carrying the ids peeked (and injected) by
// this run, for the PhaseEnd ack. The "/" marks it component-private under the
// Meta transfer contract (see HandoffState): it never transfers to another
// agent, and PhaseStart clears any stale copy before deciding whether to
// inject.
const ackMetaKey = "components/mailbox:ack"

// New returns a Component that delivers pending task notifications at the top
// of the session's next run and acks them when that run completes. A nil store
// fails at Install (agent construction), not at run time.
func New(store taskmanager.NotificationStore) gantry.Component {
	return &component{store: store}
}

func (c *component) Install(a *gantry.Agent) error {
	if c.store == nil {
		return errors.New("mailbox: New requires a non-nil NotificationStore")
	}
	if err := a.UseNamed(gantry.PhaseStart, "components/mailbox", c.inject); err != nil {
		return err
	}
	return a.UseNamed(gantry.PhaseEnd, "components/mailbox", c.ack)
}

// inject peeks the session's pending notifications, prepends the digest, and
// records the peeked ids in State.Meta for the PhaseEnd ack. It deliberately
// does NOT remove anything from the store: if the run dies before PhaseEnd,
// the notifications must survive for redelivery.
func (c *component) inject(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		// Run the inner chain FIRST: DefaultStartHandler seeds the turn's
		// user input only when Messages is empty, so injecting before next
		// would swallow the input. Prepending after next still puts the
		// digest positionally before the user message.
		if err := next(ctx, state); err != nil {
			return err
		}
		// A stale key can only be left by a prior errored run whose State is
		// being reused directly (a session layer discards failed turns).
		// Clear it before any skip branch so PhaseEnd never acks a delivery
		// this run did not make.
		delete(state.Meta, ackMetaKey)
		sid, _ := state.Meta[gantry.MetaSessionID].(string)
		if sid == "" {
			return nil // not a session-identified run
		}
		if _, isTaskRun := state.Meta[gantry.MetaTaskID]; isTaskRun {
			// A task-driven run of a shared agent config: the mailbox
			// belongs to the chat turn, not the task transcript.
			return nil
		}
		notes, err := c.store.PeekFor(ctx, sid)
		if err != nil {
			return fmt.Errorf("mailbox: peek: %w", err)
		}
		if len(notes) == 0 {
			return nil
		}
		ids := make([]string, len(notes))
		for i, n := range notes {
			ids[i] = n.ID
		}
		state.Meta[ackMetaKey] = ids
		state.Messages = append([]gantry.Message{{
			Role:    gantry.RoleSystem,
			Content: digest(notes),
		}}, state.Messages...)
		return nil
	}
}

// ack settles this run's delivered notifications. PhaseEnd runs only when the
// run completes, so an errored run never acks — that is the redelivery
// guarantee. An ack failure is deliberately swallowed: the turn already
// succeeded, and failing it over cleanup would trade a possible duplicate
// digest (next turn redelivers) for a lost turn — the wrong trade.
func (c *component) ack(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		if err := next(ctx, state); err != nil {
			return err
		}
		ids, _ := state.Meta[ackMetaKey].([]string)
		if len(ids) == 0 {
			return nil
		}
		delete(state.Meta, ackMetaKey)
		sid, _ := state.Meta[gantry.MetaSessionID].(string)
		_ = c.store.Ack(ctx, sid, ids)
		return nil
	}
}

// digest renders the one system message: a header plus a bullet per
// notification, in store (FIFO) order.
func digest(notes []*taskmanager.Notification) string {
	var b strings.Builder
	b.WriteString("While you were away:")
	for _, n := range notes {
		b.WriteString("\n- ")
		b.WriteString(bullet(n))
	}
	return b.String()
}

// bullet renders one notification as "[kind] title: body", falling back to the
// task id when the title is empty and omitting the body clause when empty.
func bullet(n *taskmanager.Notification) string {
	title := n.Title
	if title == "" {
		title = n.TaskID
	}
	if n.Body == "" {
		return fmt.Sprintf("[%s] %s", n.Kind, title)
	}
	return fmt.Sprintf("[%s] %s: %s", n.Kind, title, n.Body)
}
