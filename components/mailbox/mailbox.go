// Package mailbox surfaces background-task notifications in a chat session.
// It installs PhaseStart middleware that, once per run, drains the session's
// pending Notifications from a taskmanager.NotificationStore and prepends ONE
// system message ("While you were away:" plus a bullet per notification) ahead
// of the turn's user input. Pair it with taskmanager.NotifyBridge on the
// Dispatcher: the bridge writes, the mailbox delivers.
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

// New returns a Component that delivers pending task notifications at the top
// of the session's next run. A nil store fails at Install (agent construction),
// not at run time.
func New(store taskmanager.NotificationStore) gantry.Component {
	return &component{store: store}
}

func (c *component) Install(a *gantry.Agent) error {
	if c.store == nil {
		return errors.New("mailbox: New requires a non-nil NotificationStore")
	}
	return a.UseNamed(gantry.PhaseStart, "components/mailbox",
		func(next gantry.Handler) gantry.Handler {
			return func(ctx context.Context, state *gantry.State) error {
				// Run the inner chain FIRST: DefaultStartHandler seeds the turn's
				// user input only when Messages is empty, so injecting before next
				// would swallow the input. Prepending after next still puts the
				// digest positionally before the user message.
				if err := next(ctx, state); err != nil {
					return err
				}
				sid, _ := state.Meta[gantry.MetaSessionID].(string)
				if sid == "" {
					return nil // not a session-identified run
				}
				if _, isTaskRun := state.Meta[gantry.MetaTaskID]; isTaskRun {
					// A task-driven run of a shared agent config: the mailbox
					// belongs to the chat turn, not the task transcript.
					return nil
				}
				notes, err := c.store.DrainFor(ctx, sid)
				if err != nil {
					return fmt.Errorf("mailbox: drain: %w", err)
				}
				if len(notes) == 0 {
					return nil
				}
				state.Messages = append([]gantry.Message{{
					Role:    gantry.RoleSystem,
					Content: digest(notes),
				}}, state.Messages...)
				return nil
			}
		})
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
