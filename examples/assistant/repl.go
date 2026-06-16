package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/session"
)

const helpText = `Commands:
  /help        show this help
  /reset       start a fresh conversation (new session)
  /exit        quit the assistant
Anything else is sent to the assistant as a message.`

// armInterrupt installs an interrupt handler scoped to a single turn: it
// arranges for cancel to be called if the user interrupts (Ctrl-C), and
// returns a disarm func the caller invokes when the turn ends to remove the
// handler. main wires this to os.Interrupt via signal.Notify; tests inject a
// fake to simulate an interrupt deterministically. A nil arm means turns run
// without interrupt handling.
type armInterrupt func(cancel context.CancelFunc) (disarm func())

// runREPL reads lines from in, runs each as one assistant turn under the
// current session, and writes replies to out. It returns nil on /exit or
// when in reaches EOF.
//
// Each turn runs under its own cancellable child of ctx, and arm (if non-nil)
// installs an interrupt handler only for the duration of that turn. This is
// why interrupt handling is per-turn rather than wired to a single long-lived
// context: a process-wide signal context, once cancelled by the first Ctrl-C,
// would stay cancelled and abort every subsequent turn immediately. Scoping
// cancellation to each turn lets a cancelled turn be reported while the loop
// continues; an idle Ctrl-C at the prompt falls through to the default signal
// behaviour and terminates the process.
//
// in is wrapped in a bufio.Reader. When the caller already holds a
// *bufio.Reader over the same stdin (as main does, sharing it with the
// confirmer and ask prompter), bufio.NewReader returns that same reader —
// so a single buffer serves the whole program. This matters: a separate
// reader here would read-ahead and swallow the y/N lines the confirmer
// needs, denying every mutating action under piped input.
func runREPL(ctx context.Context, mgr *session.Manager, sessionID string, in io.Reader, out io.Writer, arm armInterrupt) error {
	reader := bufio.NewReader(in)

	resetCounter := 0
	fmt.Fprintf(out, "assistant ready (session %q). Type /help for commands.\n", sessionID)
	fmt.Fprint(out, "> ")

	for {
		raw, err := reader.ReadString('\n')
		if raw == "" && err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("assistant: read input: %w", err)
		}
		line := strings.TrimSpace(raw)

		switch {
		case line == "":
			fmt.Fprint(out, "> ")
		case line == "/exit":
			return nil
		case line == "/help":
			fmt.Fprintln(out, helpText)
			fmt.Fprint(out, "> ")
		case line == "/reset":
			resetCounter++
			sessionID = fmt.Sprintf("%s-reset-%d", sessionID, resetCounter)
			fmt.Fprintf(out, "started a new session %q.\n", sessionID)
			fmt.Fprint(out, "> ")
		default:
			state, runErr := runTurn(ctx, mgr, sessionID, line, arm)
			switch {
			case errors.Is(runErr, context.Canceled):
				fmt.Fprintln(out, "\n(turn cancelled)")
			case errors.Is(runErr, gantry.ErrHumanAborted):
				fmt.Fprintln(out, "(action denied — turn aborted, nothing was changed)")
			case errors.Is(runErr, session.ErrSaveFailed):
				fmt.Fprintln(out, replyText(state))
				fmt.Fprintln(out, "(warning: this turn was not saved)")
			case runErr != nil:
				fmt.Fprintf(out, "error: %v\n", runErr)
			default:
				fmt.Fprintln(out, replyText(state))
			}
			fmt.Fprint(out, "> ")
		}

		// A final line without a trailing newline returns content plus io.EOF;
		// process it above, then stop on the next read.
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

// runTurn executes one assistant turn under a cancellable child of ctx, with
// the interrupt handler armed only for the turn's duration. The child context
// and the handler are always released before returning, so a cancellation
// never leaks into the next turn.
func runTurn(ctx context.Context, mgr *session.Manager, sessionID, line string, arm armInterrupt) (*gantry.State, error) {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if arm != nil {
		disarm := arm(cancel)
		defer disarm()
	}
	return mgr.Session(sessionID).Run(turnCtx, line)
}
